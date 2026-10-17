package antigravity

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/planner"
)

func TestImportRecoveryAndCollision(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	conn := New(Options{Root: root})
	sink := conn.Sink.(*agy)
	tm := float64(1_790_000_000_000)
	event := &core.Event{ID: "question", Time: &tm}
	plan, err := sink.Plan(engine.PlanInput{Target: engine.DeliveryTarget{Mode: "create", Session: sink.NewSessionID("test"), Cwd: root, Title: "导入验收"}, Projection: &planner.Projection{Items: []planner.Item{{Key: "q", Event: event, Role: "user", Parts: []core.Part{{"type": "text", "text": "hello"}}}, {Key: "a", Event: event, Role: "assistant", Parts: []core.Part{{"type": "text", "text": "answer"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	// A crash after publishing the conversation, before updating the index, is recoverable.
	var ni nativeImport
	if err := json.Unmarshal([]byte(plan.Content), &ni); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sink.conv, 0700); err != nil {
		t.Fatal(err)
	}
	if err := sink.publish(ctx, plan.Target.Path, plan, ni); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(plan.Target.Path)
	if _, err := sink.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	db, err := openNative(filepath.Join(root, "conversation_summaries.db"), "rw")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE conversation_summaries SET title='renamed in native CLI' WHERE conversation_id=?", ni.Session); err != nil {
		t.Fatal(err)
	}
	if _, err := sink.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	var title string
	if err := db.QueryRow("SELECT title FROM conversation_summaries WHERE conversation_id=?", ni.Session).Scan(&title); err != nil || title != "renamed in native CLI" {
		t.Fatal(title, err)
	}
	after, _ := os.ReadFile(plan.Target.Path)
	if core.SHA256Hex(before) != core.SHA256Hex(after) {
		t.Fatal("retry rewrote the conversation")
	}
	// Changed files, foreign files and symlinks are never replaced or modified.
	target, err := openNative(plan.Target.Path, "rw")
	if err != nil {
		t.Fatal(err)
	}
	_, err = target.Exec("UPDATE steps SET step_payload=x'01' WHERE idx=0")
	target.Close()
	if err != nil {
		t.Fatal(err)
	}
	before, _ = os.ReadFile(plan.Target.Path)
	if _, err := sink.Apply(ctx, plan); err == nil {
		t.Fatal("modified prefix accepted")
	}
	after, _ = os.ReadFile(plan.Target.Path)
	if core.SHA256Hex(before) != core.SHA256Hex(after) {
		t.Fatal("changed native file was modified")
	}
	foreign := *plan
	foreign.Target.Session = sink.NewSessionID("foreign")
	foreign.Target.Path = filepath.Join(sink.conv, foreign.Target.Session+".db")
	if err := os.WriteFile(foreign.Target.Path, []byte("user-owned file"), 0600); err != nil {
		t.Fatal(err)
	}
	ni.Session = foreign.Target.Session
	foreign.Content = core.ToJSON(ni)
	if _, err := sink.Apply(ctx, &foreign); err == nil {
		t.Fatal("foreign file accepted")
	}
	data, _ := os.ReadFile(foreign.Target.Path)
	if string(data) != "user-owned file" {
		t.Fatal("foreign file changed")
	}
}
