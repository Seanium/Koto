package opencode

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/hub"
	"koto/internal/planner"
)

const schema = `CREATE TABLE project(id TEXT PRIMARY KEY,worktree TEXT NOT NULL,time_created INTEGER NOT NULL,time_updated INTEGER NOT NULL,sandboxes TEXT NOT NULL);
CREATE TABLE session(id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES project(id),slug TEXT NOT NULL,directory TEXT NOT NULL,title TEXT NOT NULL,version TEXT NOT NULL,time_created INTEGER NOT NULL,time_updated INTEGER NOT NULL,time_archived INTEGER,model TEXT);
CREATE TABLE message(id TEXT PRIMARY KEY,session_id TEXT NOT NULL REFERENCES session(id),time_created INTEGER NOT NULL,time_updated INTEGER NOT NULL,data TEXT NOT NULL);
CREATE TABLE part(id TEXT PRIMARY KEY,message_id TEXT NOT NULL REFERENCES message(id),session_id TEXT NOT NULL,time_created INTEGER NOT NULL,time_updated INTEGER NOT NULL,data TEXT NOT NULL);
CREATE TABLE session_message(id TEXT PRIMARY KEY,session_id TEXT NOT NULL,data TEXT);`

func fixture(t *testing.T) *connector {
	t.Helper()
	c := New(Options{Root: t.TempDir()}).Source.(*connector)
	c.modelFixture = core.JsonObject{"providerID": "fixture", "id": "fixture"}
	db, err := sql.Open("sqlite", c.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	db.Close()
	return c
}
func planFor(t *testing.T, c *connector, key string) *engine.DeliveryPlan {
	t.Helper()
	h := hub.New()
	var pngBytes bytes.Buffer
	png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 64, 64)))
	image := h.PutBlob(pngBytes.Bytes())
	file := h.PutBlob([]byte("attachment body"))
	ev := func(id string) *core.Event { return &core.Event{ID: id, Attrs: core.JsonObject{}} }
	pr := &planner.Projection{Items: []planner.Item{
		{Key: "u", Event: ev("u"), Role: "user", Parts: []core.Part{{"type": "text", "text": "Remember KOTO-OC-4917"}, {"type": "image", "mediaType": "image/png", "name": "image.png", "blob": image}, {"type": "file", "mediaType": "text/plain", "name": "note.txt", "blob": file}}},
		{Key: "a", Event: ev("a"), Role: "assistant", Parts: []core.Part{{"type": "text", "text": "KOTO-OC-4917"}, {"type": "tool_call", "callId": "read1", "rawName": "read", "args": core.JsonObject{"filePath": "sample.txt"}, "tool": core.JsonObject{"op": "read", "path": "sample.txt"}}}},
		{Key: "t", Event: ev("t"), Role: "tool", Parts: []core.Part{{"type": "tool_result", "callId": "read1", "outcome": "success", "parts": []any{core.JsonObject{"type": "text", "text": "tool output"}, core.JsonObject{"type": "image", "mediaType": "image/png", "blob": image}}}}},
		{Key: "a2", Event: ev("a2"), Role: "assistant", Parts: []core.Part{{"type": "text", "text": "Completed."}}},
	}}
	p, err := c.Plan(engine.PlanInput{Target: engine.DeliveryTarget{Mode: "create", Session: c.NewSessionID(key), Cwd: c.root, Title: "Test copy"}, Projection: pr, Hub: h})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestRoundTripIncrementalAttachmentsAndTrash(t *testing.T) {
	c := fixture(t)
	ctx := context.Background()
	p := planFor(t, c, "one")
	if _, err := c.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	if r, err := c.Apply(ctx, p); err != nil || r["noop"] != true {
		t.Fatal(r, err)
	}
	refs, err := c.Discover(ctx)
	if err != nil || len(refs) != 1 {
		t.Fatal(refs, err)
	}
	var batches []core.SourceBatch
	err = c.Tail(ctx, refs[0], nil, func(b core.SourceBatch) error { batches = append(batches, b); return nil })
	if err != nil {
		t.Fatal(err)
	}
	var images, files, calls, results int
	for _, b := range batches {
		for _, d := range b.Drafts {
			for _, p := range d.Parts {
				switch core.S(p["type"]) {
				case "image":
					images++
				case "file":
					files++
				case "tool_call":
					calls++
				case "tool_result":
					results++
				}
			}
		}
	}
	if images != 1 || files != 1 || calls != 1 || results != 1 {
		t.Fatalf("media/tool preservation: %d %d %d %d", images, files, calls, results)
	}
	offset := batches[len(batches)-1].Offset
	err = c.Tail(ctx, refs[0], &offset, func(core.SourceBatch) error { t.Fatal("unchanged session reread"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := c.Archive(ctx, engine.SessionRef{Session: p.Target.Session})
	if err != nil {
		t.Fatal(err)
	}
	refs, err = c.Discover(ctx)
	if err != nil || len(refs) != 0 {
		t.Fatal(refs, err)
	}
	if err = c.Unarchive(ctx, engine.SessionRef{Session: p.Target.Session}, receipt); err != nil {
		t.Fatal(err)
	}
	db, _ := c.open(ctx, true)
	defer db.Close()
	if _, err = db.Exec(`UPDATE part SET data=json_set(data,'$.text','changed'),time_updated=time_updated+1 WHERE id=(SELECT id FROM part WHERE json_extract(data,'$.type')='text' LIMIT 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Apply(ctx, p); err == nil {
		t.Fatal("overwrote modified conversation")
	}
	refs, _ = c.Discover(ctx)
	batches = nil
	if err = c.Tail(ctx, refs[0], &offset, func(b core.SourceBatch) error { batches = append(batches, b); return nil }); err != nil || len(batches) == 0 {
		t.Fatal("missed native edit", err)
	}
}
func TestAtomicCollisionAndV2Rejection(t *testing.T) {
	c := fixture(t)
	ctx := context.Background()
	p := planFor(t, c, "one")
	if _, err := c.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	q := *p
	q.Target.Session = c.NewSessionID("other")
	if _, err := c.Apply(ctx, &q); err == nil {
		t.Fatal("colliding message accepted")
	}
	refs, _ := c.Discover(ctx)
	if len(refs) != 1 {
		t.Fatal("failed transaction leaked session")
	}
	db, _ := c.open(ctx, true)
	db.Exec(`INSERT INTO session_message VALUES('v2',?,'{}')`, p.Target.Session)
	db.Close()
	if err := c.Tail(ctx, refs[0], nil, func(core.SourceBatch) error { return nil }); err == nil || !strings.Contains(err.Error(), "v2") {
		t.Fatal(err)
	}
}
func TestRootOverridesGlobalDB(t *testing.T) {
	t.Setenv("OPENCODE_DB", "/do/not/read.db")
	root := t.TempDir()
	c := New(Options{Root: root}).Source.(*connector)
	if c.db != filepath.Join(root, "opencode.db") {
		t.Fatal(c.db)
	}
}

// This opt-in test initializes with the installed OpenCode, then makes a real model
// request against a NEW imported session. It never uses native historical sessions.
func TestNativeInitializeResume(t *testing.T) {
	if os.Getenv("KOTO_OPENCODE_NATIVE_TEST") != "1" {
		t.Skip("set KOTO_OPENCODE_NATIVE_TEST=1 for native OpenCode/model validation")
	}
	root, err := filepath.Abs(os.Getenv("KOTO_OPENCODE_TEST_ROOT"))
	if err != nil || os.Getenv("KOTO_OPENCODE_TEST_ROOT") == "" {
		t.Fatal("set an isolated KOTO_OPENCODE_TEST_ROOT")
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := New(Options{Root: filepath.Join(root, "opencode")}).Source.(*connector)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e := &core.Event{ID: "native", Attrs: core.JsonObject{}}
	p, err := c.Plan(engine.PlanInput{Target: engine.DeliveryTarget{Mode: "create", Session: c.NewSessionID("native-" + time.Now().String()), Cwd: root, Title: "koto isolated native import"}, Projection: &planner.Projection{Items: []planner.Item{{Key: "q", Event: e, Role: "user", Parts: []core.Part{{"type": "text", "text": "Remember exactly KOTO-OC-4917."}}}, {Key: "a", Event: e, Role: "assistant", Parts: []core.Part{{"type": "text", "text": "KOTO-OC-4917"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "opencode", "run", "--pure", "--dir", root, "--session", p.Target.Session, "--format", "json", "What exact code did I ask you to remember? Reply only with the code. Do not use tools.")
	cmd.Env = append(os.Environ(), "OPENCODE_DB="+c.db, "XDG_DATA_HOME="+filepath.Join(root, "runtime", "data"), "XDG_CACHE_HOME="+filepath.Join(root, "runtime", "cache"), "XDG_STATE_HOME="+filepath.Join(root, "runtime", "state"))
	out, err := cmd.CombinedOutput()
	os.WriteFile(filepath.Join(root, "resume.jsonl"), out, 0600)
	if err != nil {
		t.Fatalf("resume: %v: %s", err, out)
	}
	found := false
	for _, line := range strings.Split(string(out), "\n") {
		var event core.JsonObject
		if json.Unmarshal([]byte(line), &event) == nil && core.S(event["type"]) == "text" && strings.Contains(core.S(core.Obj(event["part"])["text"]), "KOTO-OC-4917") {
			found = true
		}
	}
	if !found {
		t.Fatalf("resumed agent did not retain imported context: %s", out)
	}

	rich := planFor(t, c, "native-materials-"+time.Now().String())
	if _, err := c.Apply(ctx, rich); err != nil {
		t.Fatal(err)
	}
	export := exec.CommandContext(ctx, "opencode", "export", rich.Target.Session)
	export.Env = c.nativeEnv()
	exported, err := export.Output()
	if err != nil {
		t.Fatal("native export:", err)
	}
	exportPath := filepath.Join(root, "materials-export.json")
	os.WriteFile(exportPath, exported, 0600)
	// The official importer validates every message and nested attachment against
	// OpenCode's own schema, independently of our decoder.
	verify := exec.CommandContext(ctx, "opencode", "import", exportPath, "--pure")
	verify.Dir = root
	verify.Env = append(c.nativeEnv(), "OPENCODE_DB="+filepath.Join(root, "official-validation.db"))
	if out, err := verify.CombinedOutput(); err != nil {
		t.Fatalf("official schema import: %v: %s", err, out)
	}
	resume := exec.CommandContext(ctx, "opencode", "run", "--pure", "--dir", rich.Target.Cwd, "--session", rich.Target.Session, "--format", "json", "Give the remembered code, the exact content under [Attachment: note.txt], and the past read tool output. Do not use tools.")
	resume.Env = c.nativeEnv()
	richOutput, err := resume.CombinedOutput()
	os.WriteFile(filepath.Join(root, "materials-resume.jsonl"), richOutput, 0600)
	if err != nil || !strings.Contains(string(richOutput), "KOTO-OC-4917") || !strings.Contains(string(richOutput), "attachment body") {
		t.Fatalf("native materials resume: %v: %s", err, richOutput)
	}

	t.Logf("native imported session %s; materials %s at %s", p.Target.Session, rich.Target.Session, c.db)
}

func TestCompletionStatusAndMetadataRefresh(t *testing.T) {
	cases := []struct {
		name, finish string
		completed    bool
		want         string
	}{
		{"streaming", "", false, "in_progress"}, {"streaming with early finish", "stop", false, "in_progress"}, {"complete", "stop", true, "stop"}, {"tools", "tool-calls", true, "toolUse"}, {"length", "length", true, "length"}, {"unknown", "unknown", true, "in_progress"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			info := core.JsonObject{"finish": tt.finish, "time": core.JsonObject{"created": 1}}
			if tt.completed {
				core.Obj(info["time"])["completed"] = 2
			}
			parts := []core.JsonObject{{"type": "reasoning", "text": "thinking"}, {"type": "text", "text": "answer"}, {"type": "step-finish"}}
			if got := messageStop(info, parts, 1); got != tt.want {
				t.Fatal(got, tt.want)
			}
			if messageStop(info, parts, 0) != "in_progress" {
				t.Fatal("early part marked complete")
			}
		})
	}
	for _, name := range []string{"MessageAbortedError", "APIError"} {
		want := "error"
		if name == "MessageAbortedError" {
			want = "aborted"
		}
		if got := messageStop(core.JsonObject{"error": core.JsonObject{"name": name}}, []core.JsonObject{{"type": "text"}}, 0); got != want {
			t.Fatal(got)
		}
	}
	c := fixture(t)
	ctx := context.Background()
	p := planFor(t, c, "status")
	if _, err := c.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	db, _ := c.open(ctx, true)
	defer db.Close()
	var id string
	if err := db.QueryRow(`SELECT id FROM message WHERE json_extract(data,'$.role')='assistant' ORDER BY time_created DESC LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	db.Exec(`UPDATE message SET data=json_remove(data,'$.finish','$.time.completed'),time_updated=time_updated+1 WHERE id=?`, id)
	refs, _ := c.Discover(ctx)
	h := hub.New()
	var offset core.SourceOffset
	ingest := func(b core.SourceBatch) error {
		offset = b.Offset
		h.Ingest(b, hub.ConnectorInfo{ID: c.id, Version: Version})
		return nil
	}
	if err := c.Tail(ctx, refs[0], nil, ingest); err != nil {
		t.Fatal(err)
	}
	before := h.Binding("opencode", p.Target.Session).Leaf
	db.Exec(`UPDATE message SET data=json_set(data,'$.finish','stop','$.time.completed',100),time_updated=time_updated+1 WHERE id=?`, id)
	if err := c.Tail(ctx, refs[0], &offset, ingest); err != nil {
		t.Fatal(err)
	}
	after := h.Binding("opencode", p.Target.Session).Leaf
	if before == after {
		t.Fatal("completion metadata ignored because text did not change")
	}
	events := h.Path(after)
	if events[len(events)-1].Attrs["stopReason"] != "stop" {
		t.Fatal(events[len(events)-1].Attrs)
	}
}

func TestBinaryFilePreservedAsIndependentAsset(t *testing.T) {
	c := fixture(t)
	h := hub.New()
	data := []byte("PK\x03\x04\x00\x01binary archive")
	blob := h.PutBlob(data)
	ev := &core.Event{ID: "binary"}
	p, err := c.Plan(engine.PlanInput{Target: engine.DeliveryTarget{Mode: "create", Session: c.NewSessionID("binary"), Cwd: c.root}, Hub: h, Projection: &planner.Projection{Items: []planner.Item{{Key: "binary", Event: ev, Role: "user", Parts: []core.Part{{"type": "file", "mediaType": "application/zip", "name": "archive.zip", "blob": blob}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(core.Arr(p.Extra["attachments"])) != 1 {
		t.Fatal("binary bytes not preserved")
	}
	if _, err = c.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	refs, _ := c.Discover(context.Background())
	found := false
	if err = c.Tail(context.Background(), refs[0], nil, func(b core.SourceBatch) error {
		for _, bytes := range b.Blobs {
			if string(bytes) == string(data) {
				found = true
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("independent asset failed to round trip")
	}
}
