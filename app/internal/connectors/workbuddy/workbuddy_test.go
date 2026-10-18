package workbuddy

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/hub"
	"koto/internal/planner"
)

//go:embed testdata/schema-5.8.sql
var nativeSchema string

func fixture(t *testing.T, edition string) *connector {
	t.Helper()
	c := New(Options{Root: t.TempDir(), Edition: edition}).Source.(*connector)
	db, err := sql.Open("sqlite", c.db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(nativeSchema); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO sessions(id,cwd,user_id,status,created_at,updated_at,transport) VALUES('account-anchor','','test-account','completed',0,0,'cloud')`); err != nil {
		t.Fatal(err)
	}
	return c
}
func input(c *connector, key string) *engine.PlanInput {
	h := hub.New()
	img := h.PutBlob([]byte("image exact bytes"))
	file := h.PutBlob([]byte("附件\n字节🐙"))
	ev := func(id string) *core.Event { return &core.Event{ID: id, Kind: "message"} }
	return &engine.PlanInput{Hub: h, Target: engine.DeliveryTarget{Mode: "create", Session: c.NewSessionID(key), Cwd: c.root, Title: "复制测试"}, Projection: &planner.Projection{Items: []planner.Item{
		{Key: "u", Role: "user", Event: ev("u"), Parts: []core.Part{{"type": "text", "text": "问题一 <user_query> literal </user_query>"}, {"type": "image", "name": "中文.png", "mediaType": "image/png", "blob": img}, {"type": "file", "name": "../文件.txt", "mediaType": "text/plain", "blob": file}}},
		{Key: "a", Role: "assistant", Event: ev("a"), Parts: []core.Part{{"type": "text", "text": "回复一"}, {"type": "tool_call", "callId": "call-1", "rawName": "Read", "args": core.JsonObject{"file_path": "/sample"}, "tool": core.JsonObject{"op": "read", "path": "/sample"}}}},
		{Key: "t", Role: "tool", Event: ev("t"), Parts: []core.Part{{"type": "tool_result", "callId": "call-1", "outcome": "success", "parts": []any{core.Part{"type": "text", "text": "工具结果"}}}}},
		{Key: "u2", Role: "user", Event: ev("u2"), Parts: []core.Part{{"type": "text", "text": "问题二"}}},
		{Key: "a2", Role: "assistant", Event: ev("a2"), Parts: []core.Part{{"type": "text", "text": "回复二"}}},
	}}}
}
func read(t *testing.T, c *connector, id string) []core.SourceBatch {
	t.Helper()
	refs, e := c.Discover(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range refs {
		if r.Session == id {
			var b []core.SourceBatch
			e = c.Tail(context.Background(), r, nil, func(x core.SourceBatch) error { b = append(b, x); return nil })
			if e != nil {
				t.Fatal(e)
			}
			return b
		}
	}
	t.Fatal("session not discovered")
	return nil
}
func TestRoundTripRegionsMaterialsAndSelection(t *testing.T) {
	for _, edition := range []string{"cn", "international"} {
		t.Run(edition, func(t *testing.T) {
			c := fixture(t, edition)
			in := input(c, "one")
			p, e := c.Plan(*in)
			if e != nil {
				t.Fatal(e)
			}
			for i := 0; i < 2; i++ {
				if _, e := c.Apply(context.Background(), p); e != nil {
					t.Fatal(e)
				}
			}
			b := read(t, c, p.Target.Session)
			types := map[string]int{}
			texts := []string{}
			blobs := map[string]bool{}
			for _, batch := range b {
				for _, data := range batch.Blobs {
					blobs[string(data)] = true
				}
				for _, d := range batch.Drafts {
					if d.Kind == "context" {
						continue
					}
					for _, part := range d.Parts {
						types[core.S(part["type"])]++
						if part["type"] == "text" {
							texts = append(texts, core.S(part["text"]))
						}
					}
				}
			}
			if types["image"] != 1 || types["file"] != 1 || types["tool_call"] != 1 || types["tool_result"] != 1 || !blobs["image exact bytes"] || !blobs["附件\n字节🐙"] {
				t.Fatal(types, blobs)
			}
			if strings.Join(texts, "|") != "问题一 <user_query> literal </user_query>|回复一|问题二|回复二" {
				t.Fatal(texts)
			}
			// Selecting a middle/reply-only excerpt must not pull any preceding or trailing message in.
			in.Target.Session = c.NewSessionID("reply-only")
			in.Projection.Items = in.Projection.Items[4:]
			p, e = c.Plan(*in)
			if e != nil {
				t.Fatal(e)
			}
			if strings.Contains(p.Content, "问题") || strings.Contains(p.Content, "回复一") {
				t.Fatal("selection leaked outside content")
			}
			if _, e = c.Apply(context.Background(), p); e != nil {
				t.Fatal(e)
			}
			read(t, c, p.Target.Session)
		})
	}
}
func TestTargetOwnershipChangesRejected(t *testing.T) {
	c := fixture(t, "cn")
	p, e := c.Plan(*input(c, "one"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Apply(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	db, _ := c.open(true)
	db.Exec(`INSERT INTO sessions(id,cwd,user_id,status,created_at,updated_at,transport) VALUES('other-account','','different','completed',0,0,'cloud')`)
	db.Close()
	if _, e = c.Plan(*input(c, "two")); e == nil {
		t.Fatal("guessed ambiguous target account")
	}
	db, _ = c.open(true)
	db.Exec(`UPDATE sessions SET user_id='different' WHERE id=?`, p.Target.Session)
	db.Close()
	if _, e = c.Apply(context.Background(), p); e == nil {
		t.Fatal("account change not detected")
	}
}

func TestExplicitAccountsRemainBoundToProfileAndEdition(t *testing.T) {
	c := fixture(t, "cn")
	db, _ := c.open(true)
	_, err := db.Exec(`INSERT INTO sessions(id,cwd,user_id,status,created_at,updated_at,transport) VALUES('second-owner','','second-account','completed',0,0,'cloud')`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := c.TargetAccounts(context.Background())
	if err != nil || len(accounts) != 2 {
		t.Fatalf("accounts: %v %v", accounts, err)
	}
	for _, owner := range []string{"test-account", "second-account"} {
		in := input(c, owner)
		in.Target.Profile, in.Target.Account = c.id, owner
		p, err := c.Plan(*in)
		if err != nil {
			t.Fatal(err)
		}
		if p.Target.Account != owner || p.Target.Root != c.root || p.Target.Profile != c.id {
			t.Fatal("target identity was not frozen", p.Target)
		}
		if _, err := c.Apply(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	// A configured target preference must not hide another owner's history.
	c.userID = "test-account"
	refs, err := c.Discover(context.Background())
	if err != nil || len(refs) != 2 {
		t.Fatalf("all local owners must remain readable: %d %v", len(refs), err)
	}
	for _, ref := range refs {
		identity := core.ReadIdentity(ref.Meta)
		if identity.Status != "known" || len(identity.Claims) != 1 || identity.Claims[0].ID != core.S(ref.Meta["userId"]) || identity.Claims[0].Scope != "storage" {
			t.Fatal("incorrect historical account claim", identity)
		}
	}
	for _, owner := range []string{"not-in-this-root", "second-account"} {
		other := fixture(t, "international")
		in := input(other, "refuse-"+owner)
		in.Target.Account = owner
		if _, err := other.Plan(*in); err == nil {
			t.Fatal("account from another root or edition accepted")
		}
	}
	in := input(c, "wrong-profile")
	in.Target.Profile, in.Target.Account = "workbuddy-ai", "test-account"
	if _, err := c.Plan(*in); err == nil {
		t.Fatal("foreign profile accepted")
	}
	in = input(c, "removed-after-preview")
	in.Target.Account = "second-account"
	p, err := c.Plan(*in)
	if err != nil {
		t.Fatal(err)
	}
	db, _ = c.open(true)
	db.Exec(`UPDATE sessions SET user_id='replacement' WHERE user_id='second-account'`)
	db.Close()
	if _, err := c.Apply(context.Background(), p); err == nil {
		t.Fatal("account removed after preview accepted")
	}
	if _, err := os.Stat(p.Target.Path); !os.IsNotExist(err) {
		t.Fatal("failed target validation wrote transcript", err)
	}
}
func TestPublishFailureRetryNeverOverwrites(t *testing.T) {
	c := fixture(t, "cn")
	p, e := c.Plan(*input(c, "one"))
	if e != nil {
		t.Fatal(e)
	}
	db, _ := c.open(true)
	db.Exec(`CREATE TRIGGER reject_import BEFORE INSERT ON sessions BEGIN SELECT RAISE(ABORT,'test failure'); END`)
	db.Close()
	if _, e = c.Apply(context.Background(), p); e == nil {
		t.Fatal("expected failed index publication")
	}
	refs, _ := c.Discover(context.Background())
	if len(refs) != 0 {
		t.Fatal("uncommitted history became visible")
	}
	db, _ = c.open(true)
	db.Exec(`DROP TRIGGER reject_import`)
	db.Close()
	if _, e = c.Apply(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(p.Target.Path)
	os.WriteFile(p.Target.Path, append(before, []byte("native change\n")...), 0600)
	if _, e = c.Apply(context.Background(), p); e == nil {
		t.Fatal("modified target overwritten")
	}
}
func TestBlobTamperMissingFilesAndUnknownRecords(t *testing.T) {
	c := fixture(t, "cn")
	in := input(c, "one")
	// Native Read image results remain blob references; user images are inline
	// so WorkBuddy's manual compaction can safely consume their string value.
	in.Projection.Items[2].Parts[0]["parts"] = []any{in.Projection.Items[0].Parts[1]}
	p, e := c.Plan(*in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Apply(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	var blobPath string
	for _, v := range core.Arr(p.Extra["attachments"]) {
		a := core.Obj(v)
		if strings.Contains(core.S(a["path"]), string(filepath.Separator)+"blobs"+string(filepath.Separator)) {
			blobPath = core.S(a["path"])
			if err := os.WriteFile(blobPath, []byte("tampered"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if blobPath == "" || !strings.Contains(p.Content, "image_blob_ref") {
		t.Fatal("fixture must exercise a referenced native tool-image blob")
	}
	refs, _ := c.Discover(context.Background())
	if e = c.Tail(context.Background(), refs[0], nil, func(core.SourceBatch) error { return nil }); e == nil || !strings.Contains(e.Error(), "checksum changed") {
		t.Fatalf("changed image accepted or wrong failure: %v", e)
	}
	if err := os.Remove(blobPath); err != nil {
		t.Fatal(err)
	}
	if e = c.Tail(context.Background(), refs[0], nil, func(core.SourceBatch) error { return nil }); e == nil || !strings.Contains(e.Error(), "image blob is missing") {
		t.Fatalf("missing image accepted or wrong failure: %v", e)
	}
	r := core.JsonObject{"id": "x", "type": "unknown-control"}
	raw, _ := json.Marshal(r)
	path := c.transcript(c.root, "unknown")
	os.MkdirAll(filepath.Dir(path), 0700)
	os.WriteFile(path, append(raw, '\n'), 0600)
	if e = c.Tail(context.Background(), core.NativeRef{Harness: c.harness, Connector: c.id, Session: "unknown", Path: path}, nil, func(core.SourceBatch) error { return nil }); e == nil {
		t.Fatal("unknown native control silently lost")
	}
}

func TestInlineImageChangesAreReadFromTranscript(t *testing.T) {
	c := fixture(t, "cn")
	in := input(c, "inline-image")
	in.Projection.Items = in.Projection.Items[:1]
	in.Projection.Items[0].Parts = in.Projection.Items[0].Parts[:2]
	p, err := c.Plan(*in)
	if err != nil {
		t.Fatal(err)
	}
	if len(core.Arr(p.Extra["attachments"])) != 0 || strings.Contains(p.Content, "image_blob_ref") {
		t.Fatal("native user image should be self-contained")
	}
	if _, err := c.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	before := read(t, c, p.Target.Session)
	changed := []byte("different inline image bytes")
	content := strings.Replace(p.Content, base64.StdEncoding.EncodeToString([]byte("image exact bytes")), base64.StdEncoding.EncodeToString(changed), 1)
	if content == p.Content {
		t.Fatal("inline fixture did not change")
	}
	if err := os.WriteFile(p.Target.Path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	after := read(t, c, p.Target.Session)
	if len(before[0].Blobs) != 1 || len(after[0].Blobs) != 1 || string(after[0].Blobs[0]) != string(changed) {
		t.Fatal("inline bytes were ignored or mistaken for an external blob mutation")
	}
	if core.SHA256Hex(before[0].Blobs[0]) == core.SHA256Hex(after[0].Blobs[0]) {
		t.Fatal("changed inline content kept the previous hash")
	}
}

func TestCompletedNativeToolFailurePreservesOutcome(t *testing.T) {
	// Native Read can finish normally while reporting a failed operation in
	// providerData.toolResult, as observed for a missing file in a subagent.
	r := core.JsonObject{"id": "failed-read", "type": "function_call_result", "name": "Read", "callId": "read-1", "status": "completed", "output": core.JsonObject{"type": "text", "text": "<tool_use_error>File does not exist.</tool_use_error>"}, "providerData": core.JsonObject{"toolResult": core.JsonObject{"error": "Error reading file", "rawResponse": core.JsonObject{"is_error": true}}}}
	out, err := (decoder{}).DecodeMany(r, nil, &engine.DecodeContext{State: core.JsonObject{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Draft.Parts[0]["outcome"] != "error" {
		t.Fatal("native failed tool operation was converted to a success")
	}
}

func TestParentContextKeepsAgentResultWithoutChildTranscript(t *testing.T) {
	c := fixture(t, "cn")
	in := input(c, "parent-context")
	in.Projection.Items = in.Projection.Items[:3]
	in.Projection.Items[0].Parts = in.Projection.Items[0].Parts[:1]
	in.Projection.Items[1].Parts = []core.Part{{"type": "tool_call", "callId": "delegate", "rawName": "Agent", "args": core.JsonObject{"subagent_type": "general-purpose", "prompt": "read fixture"}, "tool": core.JsonObject{"op": "other", "name": "Agent"}}}
	in.Projection.Items[2].Parts = []core.Part{{"type": "tool_result", "callId": "delegate", "outcome": "success", "parts": []any{core.Part{"type": "text", "text": "Child returned: orange; 23 dolphins."}}}}
	p, err := c.Plan(*in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(strings.TrimSuffix(p.Target.Path, ".jsonl"), "subagents")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	// A sidecar is not part of the parent's model history. Its availability or
	// validity must not affect a copy of the actual parent request context.
	if err := os.WriteFile(filepath.Join(dir, "agent-child.jsonl"), []byte("CHILD-PRIVATE-REASONING\n{invalid child image record\n"), 0600); err != nil {
		t.Fatal(err)
	}
	refs, err := c.Discover(context.Background())
	if err != nil || len(refs) != 1 {
		t.Fatalf("child sidecar changed parent discovery: %v %v", refs, err)
	}
	batches := read(t, c, p.Target.Session)
	var calls, results int
	for _, b := range batches {
		if len(b.Blobs) != 0 {
			t.Fatal("child attachments leaked into parent context")
		}
		for _, d := range b.Drafts {
			if strings.Contains(core.Stringify(d.Parts), "CHILD-PRIVATE") {
				t.Fatal("child execution detail leaked into parent context")
			}
			for _, p := range d.Parts {
				if p["type"] == "tool_call" && p["rawName"] == "Agent" {
					calls++
				}
				if p["type"] == "tool_result" && strings.Contains(core.Stringify(p["parts"]), "orange; 23 dolphins") {
					results++
				}
			}
		}
	}
	if calls != 1 || results != 1 {
		t.Fatalf("parent delegation context lost: calls=%d results=%d", calls, results)
	}
}
func TestProjectKeyAndContextSafety(t *testing.T) {
	if ProjectKey(`C:\Users\中文\folder`) != "C-Users-中文-folder" {
		t.Fatal(ProjectKey(`C:\Users\中文\folder`))
	}
	long := strings.Repeat("🐙", 100)
	if len(ProjectKey(long)) > 255 {
		t.Fatal("long path exceeded file limit")
	}
	c := fixture(t, "cn")
	in := input(c, "bad")
	in.Projection.Items[0].Event.Kind = "context"
	if _, e := c.Plan(*in); e == nil {
		t.Fatal("copied target system prompt")
	}
	in = input(c, "badpath")
	in.Target.Session = "../escape"
	if _, e := c.Plan(*in); e == nil {
		t.Fatal("session path traversal")
	}
}

func TestCompactionPreservesHistoryAndNativeBoundary(t *testing.T) {
	for _, edition := range []string{"cn", "international"} {
		t.Run(edition, func(t *testing.T) {
			c := fixture(t, edition)
			in := input(c, "compaction")
			in.Projection.Items = in.Projection.Items[3:]
			summary := "<cb_summary>问题二，回复二，保留的结论。</cb_summary>"
			in.Projection.Items = append(in.Projection.Items, planner.Item{Role: "user", Event: &core.Event{ID: "compact", Kind: "compaction", Actor: "harness", Attrs: core.JsonObject{"workbuddyCompaction": core.JsonObject{"type": "user-command", "summary": false, "kind": "manual", "trigger": "manual"}}}, Parts: []core.Part{{"type": "text", "text": summary}}})
			p, err := c.Plan(*in)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = c.Apply(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(p.Target.Path)
			if err != nil {
				t.Fatal(err)
			}
			var records []core.JsonObject
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				var r core.JsonObject
				if err := json.Unmarshal([]byte(line), &r); err != nil {
					t.Fatal(err)
				}
				records = append(records, r)
			}
			boundary := records[len(records)-1]
			pd := core.Obj(boundary["providerData"])
			if pd["isCompacted"] != true || pd["isSummary"] != false || pd["compactionId"] != boundary["id"] || boundary["logicalParentId"] != records[len(records)-2]["id"] {
				t.Fatalf("invalid native boundary: %#v", boundary)
			}
			if core.S(core.Obj(core.Arr(boundary["content"])[0])["text"]) != summary {
				t.Fatal("summary changed or wrapped as a user question")
			}
			var drafts []core.DraftEvent
			for _, b := range read(t, c, p.Target.Session) {
				drafts = append(drafts, b.Drafts...)
			}
			if len(drafts) != 3 {
				t.Fatalf("historical records lost: %d", len(drafts))
			}
			d := drafts[2]
			if d.Kind != "compaction" || d.Actor != "harness" || d.Replaces == nil || d.Replaces.To.Native != drafts[1].NativeID {
				t.Fatalf("missing logical replacement: %#v", d)
			}
		})
	}
}

func TestNativeResumeRootIdentity(t *testing.T) {
	for _, edition := range []string{"cn", "international"} {
		c := New(Options{Edition: edition}).Source.(*connector)
		if args, _ := c.NativeResume("abc-123"); len(args) == 0 {
			t.Fatal("default root missing resume")
		}
		if args, _ := c.NativeResume("../escape"); args != nil {
			t.Fatal("invalid ID accepted")
		}
		c.root = t.TempDir()
		if args, _ := c.NativeResume("abc-123"); args != nil {
			t.Fatal("custom root advertised unroutable URI")
		}
		target := c.root
		link := filepath.Join(t.TempDir(), "profile-link")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		c.root = link
		if !c.hasDefaultRoot(target) {
			t.Fatal("same profile symlink not recognized")
		}
		if c.hasDefaultRoot(t.TempDir()) {
			t.Fatal("different profile accepted")
		}
	}
}

func TestNativeReasoningAndToolImage(t *testing.T) {
	c := fixture(t, "cn")
	in := input(c, "tool-media")
	in.Projection.Items[1].Parts = append([]core.Part{{"type": "reasoning", "text": "可见推理内容"}}, in.Projection.Items[1].Parts...)
	image := in.Projection.Items[0].Parts[1]
	in.Projection.Items[2].Parts[0]["parts"] = []any{core.Part{"type": "text", "text": "图片结果"}, image}
	p, err := c.Plan(*in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	var reasoning, toolImage bool
	for _, batch := range read(t, c, p.Target.Session) {
		for _, d := range batch.Drafts {
			for _, part := range d.Parts {
				if part["type"] == "reasoning" && part["text"] == "可见推理内容" {
					reasoning = true
				}
				if part["type"] == "tool_result" {
					for _, p := range core.Parts(part["parts"]) {
						if p["type"] == "image" && core.S(core.Obj(p["blob"])["sha256"]) == core.S(core.Obj(image["blob"])["sha256"]) {
							toolImage = true
						}
					}
				}
			}
		}
	}
	if !reasoning || !toolImage {
		t.Fatalf("native content lost: reasoning=%v toolImage=%v", reasoning, toolImage)
	}
}
func TestMaterialPathRelocatedFromOriginalEvent(t *testing.T) {
	c := fixture(t, "cn")
	in := input(c, "path")
	file := in.Projection.Items[0].Parts[2]
	old := filepath.Join(t.TempDir(), "原始文件.txt")
	in.Projection.Items[0].Event.Parts = []core.Part{{"type": "file", "blob": file["blob"], "uri": "file://" + filepath.ToSlash(old)}}
	in.Projection.Items[0].Parts[0]["text"] = `读取 @"` + old + `"`
	p, err := c.Plan(*in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.Content, old) {
		t.Fatal("source file badge survived copying")
	}
	if !strings.Contains(p.Content, ".assets") {
		t.Fatal("independent target attachment missing")
	}
}

// Expected keys were generated by executing compressWorkspacePathName from the
// official WorkBuddy 5.8.3 runtime, not by the Go implementation under test.
//
//go:embed testdata/project-key-oracle.json
var projectKeyOracle []byte

func TestProjectKeyOfficialRuntimeOracle(t *testing.T) {
	var cases []struct{ Path, Key string }
	if err := json.Unmarshal(projectKeyOracle, &cases); err != nil {
		t.Fatal(err)
	}
	for _, item := range cases {
		if got := ProjectKey(item.Path); got != item.Key {
			t.Errorf("ProjectKey(%q) = %q; native runtime = %q", item.Path, got, item.Key)
		}
	}
}

func TestDatabaseURLReservedPathCharacters(t *testing.T) {
	c := fixture(t, "cn")
	path := filepath.Join(t.TempDir(), "O'Brien # 中文 & spaces.db")
	if err := os.Rename(c.db, path); err != nil {
		t.Fatal(err)
	}
	c.db = path
	for _, write := range []bool{false, true} {
		db, err := c.open(write)
		if err != nil {
			t.Fatal(err)
		}
		var count int
		err = db.QueryRow("SELECT count(*) FROM sessions").Scan(&count)
		db.Close()
		if err != nil || count != 1 {
			t.Fatalf("write=%v count=%d err=%v", write, count, err)
		}
	}
}
