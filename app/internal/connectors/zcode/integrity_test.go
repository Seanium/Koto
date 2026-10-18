package zcode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/hub"
	"koto/internal/planner"
)

func TestActiveBranchMatchesNativeRewind(t *testing.T) {
	ids := []string{"u1", "a1", "u2", "a2", "u3", "a3"}
	cases := []struct {
		name string
		r    core.JsonObject
		want []string
	}{
		{"unchanged", nil, ids},
		{"new branch cut", core.JsonObject{"kind": "conversation_rewind", "targetMessageID": "u2", "branchCutAfterMessageID": "a2"}, []string{"u1", "a1", "u3", "a3"}},
		{"legacy created", core.JsonObject{"kind": "conversation_rewind", "targetMessageID": "u2", "createdMessageID": "u3"}, []string{"u1", "a1", "u3", "a3"}},
		{"explicit kept", core.JsonObject{"kind": "conversation_rewind", "targetMessageID": "u2", "keptMessageIDs": []any{"a1", "absent", "u1"}, "branchCutAfterMessageID": "a2"}, []string{"a1", "u1", "u3", "a3"}},
		{"missing cut", core.JsonObject{"kind": "conversation_rewind", "targetMessageID": "u2", "branchCutAfterMessageID": "missing"}, []string{"u1", "a1"}},
		{"empty kept", core.JsonObject{"kind": "conversation_rewind", "targetMessageID": "u1", "keptMessageIDs": []any{}, "createdMessageID": "u3"}, []string{"u3", "a3"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := activeMessages(ids, tc.r)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatal(got, err)
			}
		})
	}
	if _, err := activeMessages(ids, core.JsonObject{"kind": "future-rewind"}); err == nil {
		t.Fatal("unknown rewind accepted")
	}
}

func TestUnsupportedStructureFailsBeforeAnyBatch(t *testing.T) {
	for _, typ := range []string{"compaction", "subtask", "future-content"} {
		t.Run(typ, func(t *testing.T) {
			c := fixture(t)
			ctx := context.Background()
			p := planFor(t, c, typ)
			if _, err := c.Apply(ctx, p); err != nil {
				t.Fatal(err)
			}
			db, _ := c.open(ctx, true)
			defer db.Close()
			if _, err := db.Exec(`UPDATE part SET data=json_set(data,'$.type',?) WHERE id=(SELECT id FROM part ORDER BY rowid DESC LIMIT 1)`, typ); err != nil {
				t.Fatal(err)
			}
			refs, _ := c.Discover(ctx)
			yielded := 0
			err := c.Tail(ctx, refs[0], nil, func(core.SourceBatch) error { yielded++; return nil })
			if err == nil || !strings.Contains(err.Error(), "unsupported native part") || yielded != 0 {
				t.Fatal(err, yielded)
			}
		})
	}
}

func TestSelectedContentAndToolIntegrity(t *testing.T) {
	ctx := context.Background()
	item := func(id, role, text string) planner.Item {
		return planner.Item{Key: id, Role: role, Event: &core.Event{ID: id, Kind: "message"}, Parts: []core.Part{{"type": "text", "text": text}}}
	}
	all := []planner.Item{item("u1", "user", "中文🐙 FIRST"), item("a1", "assistant", "REPLY1"), item("u2", "user", "MIDDLE"), item("a2", "assistant", "REPLY2"), item("u3", "user", "LAST-EXCLUDE"), item("a3", "assistant", "FINAL-EXCLUDE")}
	for _, tc := range []struct {
		name           string
		items          []planner.Item
		wanted, absent string
	}{{"prefix", all[:4], "REPLY2", "LAST-EXCLUDE"}, {"noncontiguous", []planner.Item{all[0], all[3]}, "REPLY2", "MIDDLE"}, {"reply-only", []planner.Item{all[3]}, "REPLY2", "中文🐙 FIRST"}, {"question-only", all[:1], "中文🐙 FIRST", "REPLY1"}} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixture(t)
			p, err := c.Plan(engine.PlanInput{Target: engine.DeliveryTarget{Mode: "create", Session: c.NewSessionID(tc.name), Cwd: c.root}, Projection: &planner.Projection{Items: tc.items}, Hub: hub.New()})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = c.Apply(ctx, p); err != nil {
				t.Fatal(err)
			}
			refs, _ := c.Discover(ctx)
			var content strings.Builder
			err = c.Tail(ctx, refs[0], nil, func(b core.SourceBatch) error {
				for _, d := range b.Drafts {
					for _, p := range d.Parts {
						content.WriteString(core.S(p["text"]))
					}
				}
				return nil
			})
			if err != nil || !strings.Contains(content.String(), tc.wanted) || strings.Contains(content.String(), tc.absent) {
				t.Fatal(content.String(), err)
			}
		})
	}
	c := fixture(t)
	call := item("a", "assistant", "")
	call.Parts = []core.Part{{"type": "tool_call", "callId": "x", "rawName": "Bash", "args": core.JsonObject{"command": "echo hi"}}}
	result := item("t", "tool", "")
	result.Parts = []core.Part{{"type": "tool_result", "callId": "x", "outcome": "success", "parts": []any{core.JsonObject{"type": "text", "text": "hi"}}}}
	for _, items := range [][]planner.Item{{call}, {result}, {call, call, result}, {call, result, result}, {item("s", "system", "SYSTEM")}} {
		if _, err := c.Plan(engine.PlanInput{Target: engine.DeliveryTarget{Mode: "create", Session: c.NewSessionID("invalid"), Cwd: c.root}, Projection: &planner.Projection{Items: items}}); err == nil {
			t.Fatal("invalid projection accepted", items)
		}
	}
}

func TestTargetModelNeverCopiesSourceSelection(t *testing.T) {
	c := fixture(t)
	os.MkdirAll(filepath.Join(c.root, "v2"), 0700)
	cfg := core.JsonObject{"schemaVersion": 1, "config": core.JsonObject{"defaultModelSelection": core.JsonObject{"providerId": "target", "modelId": "chosen/model", "options": core.JsonObject{"reasoningLevel": "low"}}}}
	b, _ := json.Marshal(cfg)
	os.WriteFile(filepath.Join(c.root, "v2", "provider_config.json"), b, 0600)
	p := planFor(t, c, "target-model")
	if _, err := c.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	db, _ := c.open(context.Background(), false)
	defer db.Close()
	var raw string
	if err := db.QueryRow(`SELECT data FROM session_entry WHERE session_id=?`, p.Target.Session).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"modelId":"chosen/model"`) || !strings.Contains(raw, `"reasoningLevel":"low"`) {
		t.Fatal(raw)
	}
}

func TestAttachmentBytesLayoutRollbackAndMissing(t *testing.T) {
	c := fixture(t)
	ctx := context.Background()
	idx := addIndex(t, c)
	p := planFor(t, c, "assets")
	if len(core.Arr(p.Extra["attachments"])) != 1 {
		t.Fatal("native text file was not materialized")
	}
	path := core.S(core.Obj(core.Arr(p.Extra["attachments"])[0])["path"])
	idx.Exec(`CREATE TRIGGER refuse_import BEFORE INSERT ON tasks BEGIN SELECT RAISE(ABORT,'test'); END`)
	if _, err := c.Apply(ctx, p); err == nil {
		t.Fatal("expected index error")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("failed copy left owned attachment", err)
	}
	idx.Exec(`DROP TRIGGER refuse_import`)
	if _, err := c.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "attachment body" {
		t.Fatal("file bytes changed", err)
	}
	refs, _ := c.Discover(ctx)
	blobs := map[string]bool{}
	imageHash := ""
	var delivery delivery
	if err = json.Unmarshal([]byte(p.Content), &delivery); err != nil {
		t.Fatal(err)
	}
	for _, m := range delivery.Messages {
		for _, part := range m.Parts {
			if core.S(part["mime"]) == "image/png" {
				imageHash = core.S(core.Obj(part["metadata"])["sha256"])
			}
		}
	}
	err = c.Tail(ctx, refs[0], nil, func(b core.SourceBatch) error {
		for _, v := range b.Blobs {
			blobs[core.SHA256Hex(v)] = true
		}
		return nil
	})
	if err != nil || !blobs[core.SHA256Hex([]byte("attachment body"))] || !blobs[imageHash] {
		t.Fatal("blob content changed", blobs, err)
	}
	os.WriteFile(path, []byte("modified"), 0600)
	if _, err = c.decodePart(core.JsonObject{"type": "file", "url": path, "metadata": core.JsonObject{"sha256": core.SHA256Hex([]byte("attachment body"))}}, func([]byte) core.JsonObject { t.Fatal("changed bytes published"); return nil }); err == nil {
		t.Fatal("decode ignored change after preflight")
	}
	if err = c.Tail(ctx, refs[0], nil, func(core.SourceBatch) error { t.Fatal("corrupt attachment yielded"); return nil }); err == nil {
		t.Fatal("attachment integrity ignored")
	}
	os.Remove(path)
	if err = c.Tail(ctx, refs[0], nil, func(core.SourceBatch) error { t.Fatal("missing attachment yielded"); return nil }); err == nil {
		t.Fatal("missing attachment silently lost")
	}
}

func TestUnknownContentFieldsAndToolLayout(t *testing.T) {
	c := fixture(t)
	if err := c.validateNativeMessage(nativeMessage{ID: "u", Info: core.JsonObject{"role": "user"}, Parts: []core.JsonObject{{"type": "text", "text": "visible", "futurePayload": "secret"}}}); err == nil {
		t.Fatal("unknown native field lost")
	}
	for _, layout := range []any{[]any{core.JsonObject{"type": "attachment", "attachmentIndex": 9}}, []any{core.JsonObject{"type": "future"}}} {
		if err := validateToolLayout(core.JsonObject{"metadata": core.JsonObject{"modelContentLayout": layout}}); err == nil {
			t.Fatal("invalid layout accepted")
		}
	}
	if err := validateToolLayout(core.JsonObject{"attachments": []any{core.JsonObject{"type": "file"}}, "metadata": core.JsonObject{"modelContentLayout": []any{core.JsonObject{"type": "text", "text": "file omitted"}}}}); err == nil {
		t.Fatal("attachment omitted by layout")
	}
}

func TestToolRetryDeclarationOrderAndTextLikeFile(t *testing.T) {
	parts := []core.JsonObject{{"id": "old", "type": "tool", "callID": "a", "declarationIndex": 0}, {"id": "b", "type": "tool", "callID": "b", "declarationIndex": 1}, {"id": "text", "type": "text", "text": "explanation"}, {"id": "a", "type": "tool", "callID": "a", "declarationIndex": 0}}
	active, old := orderedToolParts(parts)
	if len(old) != 1 || core.S(old[0]["id"]) != "old" || len(active) != 3 || core.S(active[1]["id"]) != "a" || core.S(active[2]["id"]) != "b" {
		t.Fatal(active, old)
	}
	c := fixture(t)
	path := filepath.Join(c.root, "literal.txt")
	content := []byte("data:image/png;base64,bGl0ZXJhbA==")
	os.WriteFile(path, content, 0600)
	got, ok := c.attachmentBytes(path)
	if !ok || string(got) != string(content) {
		t.Fatal("ordinary file misread as encoded artifact")
	}
}

func TestLegacyStoreAndRewindRevision(t *testing.T) {
	c := New(Options{Root: t.TempDir()}).Source.(*connector)
	dir := filepath.Join(c.root, "v2", "sessions")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "legacy.json"), []byte(`{}`), 0600)
	if _, err := c.Discover(context.Background()); err == nil || !strings.Contains(err.Error(), "legacy") {
		t.Fatal("legacy sessions silently absent", err)
	}
	c = fixture(t)
	ctx := context.Background()
	p := planFor(t, c, "rewind-revision")
	if _, err := c.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	refs, _ := c.Discover(ctx)
	var off core.SourceOffset
	if err := c.Tail(ctx, refs[0], nil, func(b core.SourceBatch) error { off = b.Offset; return nil }); err != nil {
		t.Fatal(err)
	}
	db, _ := c.open(ctx, true)
	defer db.Close()
	var id string
	db.QueryRow(`SELECT id FROM message ORDER BY sequence DESC LIMIT 1`).Scan(&id)
	raw, _ := json.Marshal(core.JsonObject{"kind": "conversation_rewind", "targetMessageID": id})
	db.Exec(`UPDATE session SET revert=? WHERE id=?`, string(raw), p.Target.Session)
	changed := false
	if err := c.Tail(ctx, refs[0], &off, func(b core.SourceBatch) error {
		changed = true
		for _, d := range b.Drafts {
			for _, part := range d.Parts {
				if core.S(part["text"]) == "Completed." {
					t.Fatal("rewound reply copied")
				}
			}
		}
		return nil
	}); err != nil || !changed {
		t.Fatal("missed rewind-only update", err)
	}
}

func TestRuntimePromptsStayOutsideConversation(t *testing.T) {
	if runtimeContext(core.JsonObject{"role": "assistant", "semantics": core.JsonObject{"origin": "agent_runtime", "kind": "assistant_response", "providerVisibility": "visible"}}) {
		t.Fatal("native assistant response classified as context")
	}
	c := fixture(t)
	ctx := context.Background()
	p := planFor(t, c, "runtime-context")
	if _, err := c.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	db, err := c.open(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Native system reminders may be marked on a part in older SQLite stores.
	if _, err = db.Exec(`UPDATE part SET data=json_set(data,'$.synthetic',json('true'),'$.text','SYSTEM-PART') WHERE id=(SELECT p.id FROM part p JOIN message m ON m.id=p.message_id WHERE json_extract(m.data,'$.role')='user' AND json_extract(p.data,'$.type')='text' LIMIT 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE message SET data=json_set(data,'$.semantics.providerVisibility','hidden') WHERE json_extract(data,'$.role')='assistant'`); err != nil {
		t.Fatal(err)
	}
	refs, err := c.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seenContext := false
	if err = c.Tail(ctx, refs[0], nil, func(b core.SourceBatch) error {
		for _, d := range b.Drafts {
			if d.Kind == "context" {
				seenContext = true
			}
			if d.Kind == "message" && d.Actor == "assistant" {
				t.Fatal("hidden assistant exposed")
			}
			for _, v := range d.Parts {
				if core.S(v["text"]) == "SYSTEM-PART" && d.Kind != "context" {
					t.Fatal("system part exposed")
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !seenContext {
		t.Fatal("native context disappeared")
	}
	// Explicit real user text is never classified by its content or synthetic hint.
	if syntheticContextPart(core.JsonObject{"semantics": core.JsonObject{"origin": "real_user"}}, core.JsonObject{"synthetic": true}) {
		t.Fatal("real user misclassified")
	}
}

func TestExistingTargetSequenceCannotBeOverwritten(t *testing.T) {
	for _, table := range []string{"message", "part"} {
		t.Run(table, func(t *testing.T) {
			c := fixture(t)
			ctx := context.Background()
			p := planFor(t, c, "modified-sequence")
			if _, err := c.Apply(ctx, p); err != nil {
				t.Fatal(err)
			}
			db, err := c.open(ctx, true)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err = db.Exec(`UPDATE `+table+` SET sequence=sequence+100 WHERE session_id=?`, p.Target.Session); err != nil {
				t.Fatal(err)
			}
			if _, err = c.Apply(ctx, p); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
				t.Fatal("reordered history overwritten", err)
			}
		})
	}
}

func TestUnknownMessageSemanticsAndMalformedModel(t *testing.T) {
	c := fixture(t)
	for _, info := range []core.JsonObject{
		{"role": "user", "futureContent": "unrepresented"},
		{"role": "assistant", "structured": core.JsonObject{"answer": 42}},
		{"role": "assistant", "semantics": core.JsonObject{"kind": "compact_summary"}},
		{"role": "assistant", "summary": true},
	} {
		if err := c.validateNativeMessage(nativeMessage{ID: "unknown", Info: info}); err == nil {
			t.Fatal("unrepresented content accepted", info)
		}
	}
	if err := validateToolLayout(core.JsonObject{"metadata": core.JsonObject{"modelContentLayout": "not-an-array"}}); err == nil {
		t.Fatal("invalid layout accepted")
	}
	os.MkdirAll(filepath.Join(c.root, "v2"), 0700)
	for _, options := range []any{"invalid", core.JsonObject{"reasoningLevel": 1}} {
		b, _ := json.Marshal(core.JsonObject{"schemaVersion": 1, "config": core.JsonObject{"defaultModelSelection": core.JsonObject{"providerId": "target", "modelId": "m", "options": options}}})
		if err := os.WriteFile(filepath.Join(c.root, "v2", "provider_config.json"), b, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := c.targetModel(); err == nil {
			t.Fatal("invalid model options accepted")
		}
	}
}
