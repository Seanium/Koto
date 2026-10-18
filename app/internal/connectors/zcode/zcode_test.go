package zcode

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/hub"
	"koto/internal/planner"
)

// These are the native columns used by CLI 0.16.9 migrations 0001/0007/0014/0015.
//
//go:embed testdata/schema-0.16.9.sql
var schema string

const indexSchema = `CREATE TABLE tasks(workspace_key TEXT NOT NULL,workspace_path TEXT NOT NULL,task_id TEXT NOT NULL,title TEXT,task_status TEXT,provider TEXT,mode TEXT,created_at INTEGER,updated_at INTEGER,meta_json TEXT,archived INTEGER DEFAULT 0,PRIMARY KEY(workspace_key,task_id));`

func fixture(t *testing.T) *connector {
	t.Helper()
	c := New(Options{Root: t.TempDir()}).Source.(*connector)
	if err := os.MkdirAll(filepath.Dir(c.db), 0700); err != nil {
		t.Fatal(err)
	}
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
		{Key: "u", Event: ev("u"), Role: "user", Parts: []core.Part{{"type": "text", "text": "Remember KOTO-ZC-4917"}, {"type": "image", "mediaType": "image/png", "name": "image.png", "blob": image}, {"type": "file", "mediaType": "text/plain", "name": "note.txt", "blob": file}}},
		{Key: "a", Event: ev("a"), Role: "assistant", Parts: []core.Part{{"type": "text", "text": "KOTO-ZC-4917"}, {"type": "tool_call", "callId": "read1", "rawName": "read", "args": core.JsonObject{"filePath": "sample.txt"}, "tool": core.JsonObject{"op": "read", "path": "sample.txt"}}}},
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

func addIndex(t *testing.T, c *connector) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(c.index), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", c.index)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(indexSchema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
func TestDesktopIndexTransactionAndUnrelatedRows(t *testing.T) {
	c := fixture(t)
	idx := addIndex(t, c)
	ctx := context.Background()
	p := planFor(t, c, "desktop")
	if _, err := idx.Exec(`INSERT INTO tasks(workspace_key,workspace_path,task_id,title,archived) VALUES('untouched','untouched','old','original',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Exec(`CREATE TRIGGER refuse_import BEFORE INSERT ON tasks BEGIN SELECT RAISE(ABORT,'test index error'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Apply(ctx, p); err == nil {
		t.Fatal("index failure not returned")
	}
	refs, _ := c.Discover(ctx)
	if len(refs) != 0 {
		t.Fatal("failed index write left transcript")
	}
	idx.Exec(`DROP TRIGGER refuse_import`)
	if _, err := c.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	var title, provider string
	var archived int
	if err := idx.QueryRow(`SELECT title,provider,archived FROM tasks WHERE task_id=?`, p.Target.Session).Scan(&title, &provider, &archived); err != nil || title != "Test copy" || provider != "glm" || archived != 0 {
		t.Fatal(title, provider, archived, err)
	}
	receipt, err := c.Archive(ctx, engine.SessionRef{Session: p.Target.Session})
	if err != nil {
		t.Fatal(err)
	}
	idx.QueryRow(`SELECT archived FROM tasks WHERE task_id=?`, p.Target.Session).Scan(&archived)
	if archived != 1 {
		t.Fatal("desktop not archived")
	}
	if err = c.Unarchive(ctx, engine.SessionRef{Session: p.Target.Session}, receipt); err != nil {
		t.Fatal(err)
	}
	idx.QueryRow(`SELECT title,archived FROM tasks WHERE task_id='old'`).Scan(&title, &archived)
	if title != "original" || archived != 1 {
		t.Fatal("unrelated task changed")
	}
	// An interrupted cross-WAL commit can leave the native transcript without its
	// index row. Reapplying the identical plan repairs only that missing row.
	idx.Exec(`DELETE FROM tasks WHERE task_id=?`, p.Target.Session)
	if _, err = c.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	var n int
	idx.QueryRow(`SELECT count(*) FROM tasks WHERE task_id=?`, p.Target.Session).Scan(&n)
	if n != 1 {
		t.Fatal("index repair failed")
	}
}
func TestSequenceContextAndUnknownParts(t *testing.T) {
	c := fixture(t)
	ctx := context.Background()
	p := planFor(t, c, "sequence")
	if _, err := c.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	db, _ := c.open(ctx, true)
	defer db.Close()
	var first string
	db.QueryRow(`SELECT id FROM message WHERE session_id=? ORDER BY sequence LIMIT 1`, p.Target.Session).Scan(&first)
	db.Exec(`UPDATE message SET time_created=9999999999999 WHERE id=?`, first)
	refs, _ := c.Discover(ctx)
	var firstDraft string
	if err := c.Tail(ctx, refs[0], nil, func(b core.SourceBatch) error {
		if firstDraft == "" && len(b.Drafts) > 0 {
			firstDraft = b.Drafts[0].NativeID
		}
		return nil
	}); err != nil || firstDraft != first {
		t.Fatal("native sequence changed", firstDraft, first, err)
	}
	db.Exec(`UPDATE message SET data=json_set(data,'$.synthetic',json('true'),'$.source','todo_reminder') WHERE id=?`, first)
	var contextSeen bool
	if err := c.Tail(ctx, refs[0], nil, func(b core.SourceBatch) error {
		for _, d := range b.Drafts {
			if d.NativeID == first {
				contextSeen = d.Kind == "context"
			}
		}
		return nil
	}); err != nil || !contextSeen {
		t.Fatal("runtime context became user prompt", err)
	}
}
func TestArtifactReferencesPreserveBytesAndContainment(t *testing.T) {
	c := fixture(t)
	dir := filepath.Join(c.root, "cli", "artifacts", "ses_fixture")
	os.MkdirAll(dir, 0700)
	data := []byte("文件\x00原始字节")
	file := filepath.Join(dir, "call-tool-result-asset.bin")
	os.WriteFile(file, data, 0600)
	p, err := c.decodePart(core.JsonObject{"type": "file", "mime": "application/octet-stream", "url": "zcode-artifact://ses_fixture/tool-result-asset", "filename": "sample.bin"}, func(b []byte) core.JsonObject {
		if !bytes.Equal(data, b) {
			t.Fatal("artifact bytes changed")
		}
		return core.JsonObject{"sha256": core.SHA256Hex(b)}
	})
	if err != nil || len(p) != 1 || core.S(core.Obj(p[0]["blob"])["sha256"]) != core.SHA256Hex(data) {
		t.Fatal(p, err)
	}
	for _, uri := range []string{"zcode-artifact://ses_fixture/../../secret", "zcode-artifact://ses_fixture/%2fetc%2fpasswd", "zcode-artifact://ses_fixture/absent"} {
		if _, ok := c.attachmentBytes(uri); ok {
			t.Fatal("invalid artifact accepted", uri)
		}
	}
	if err := os.Symlink(file, filepath.Join(dir, "ambiguous-tool-result-asset.bin")); err == nil {
		if _, ok := c.attachmentBytes("zcode-artifact://ses_fixture/tool-result-asset"); ok {
			t.Fatal("ambiguous artifact accepted")
		}
	}
}
func TestIndependentCopiesAndAssistantOnly(t *testing.T) {
	c := fixture(t)
	ctx := context.Background()
	p := planFor(t, c, "first")
	q := planFor(t, c, "second")
	if p.Target.Session == q.Target.Session {
		t.Fatal("duplicate new identity")
	}
	for _, plan := range []*engine.DeliveryPlan{p, q} {
		if _, err := c.Apply(ctx, plan); err != nil {
			t.Fatal(err)
		}
	}
	refs, _ := c.Discover(ctx)
	if len(refs) != 2 {
		t.Fatal(refs)
	}
	pr := &planner.Projection{Items: []planner.Item{{Key: "a", Event: &core.Event{ID: "a"}, Role: "assistant", Parts: []core.Part{{"type": "text", "text": "Selected reply only"}}}}}
	only, err := c.Plan(engine.PlanInput{Target: engine.DeliveryTarget{Mode: "create", Session: c.NewSessionID("reply"), Cwd: c.root}, Projection: pr, Hub: hub.New()})
	if err != nil {
		t.Fatal(err)
	}
	if len(only.Losses) != 1 {
		t.Fatal("missing explicit synthetic-user notice")
	}
	if _, err = c.Apply(ctx, only); err != nil {
		t.Fatal(err)
	}
}
