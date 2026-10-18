package claudecode

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

func TestNativeTextSnapshotWriter(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		rangeInfo  core.JsonObject
		mime       string
	}{
		{"unicode-crlf", "中文🐙\r\nsecond\r\n", nil, "text/plain"},
		{"empty", "", nil, "text/plain"},
		{"newline", "\n", nil, "text/plain"},
		{"no-final-newline", "中文🐙", nil, "text/plain; charset=utf-8"},
		{"markdown", "# 中文🐙\n", nil, "text/markdown"},
		{"python", "print('中文🐙')\n", nil, "text/x-python"},
		{"partial", "仅快照\r\n", core.JsonObject{"startLine": 5, "numLines": 2, "totalLines": 19}, "text/plain"},
		{"unknown-range", "snapshot", core.JsonObject{}, "text/plain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			h := hub.New()
			defer h.Close()
			part := core.Part{"type": "file", "name": "说明 中文🐙.txt", "mediaType": tc.mime, "blob": h.PutBlob([]byte(tc.text))}
			if tc.rangeInfo != nil {
				part["snapshot"] = tc.rangeInfo
			}
			input := engine.PlanInput{Hub: h, Projection: &planner.Projection{Items: []planner.Item{
				{Key: "u", Role: "user", Event: &core.Event{ID: "u", Kind: "message", Actor: "user"}, Parts: []core.Part{{"type": "text", "text": "question"}, part}},
				{Key: "a", Role: "assistant", Event: &core.Event{ID: "a", Kind: "message", Actor: "assistant"}, Parts: []core.Part{{"type": "text", "text": "answer"}}},
			}}, Target: engine.DeliveryTarget{Mode: "create", Session: "11111111-1111-4111-8111-111111111111", Cwd: base}}
			c := New(Options{Root: filepath.Join(base, "claude")})
			plan, err := c.Sink.Plan(input)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(plan.Content, `"type":"document"`) || strings.Contains(plan.Content, "ccSnapshotPath") {
				t.Fatal("plain text file was not encoded as a native snapshot")
			}
			lines := strings.Split(strings.TrimSpace(plan.Content), "\n")
			if len(lines) != 3 {
				t.Fatal("snapshot created extra prompt or lost answer", len(lines))
			}
			rows := make([]core.JsonObject, len(lines))
			for i, line := range lines {
				if err := json.Unmarshal([]byte(line), &rows[i]); err != nil {
					t.Fatal(err)
				}
				if i > 0 && rows[i]["parentUuid"] != rows[i-1]["uuid"] {
					t.Fatal("broken native parent chain", i)
				}
			}
			file := core.Obj(core.Obj(core.Obj(rows[1]["attachment"])["content"])["file"])
			if rows[1]["type"] != "attachment" || file["content"] != tc.text || rows[1]["renderedRole"] != "system" {
				t.Fatal("native snapshot changed bytes or role", rows[1])
			}
			for key, want := range tc.rangeInfo {
				if key != "notice" && core.Stringify(file[key]) != core.Stringify(want) {
					t.Fatal("source range changed", key, file[key], want)
				}
			}
			if tc.rangeInfo != nil && len(tc.rangeInfo) == 0 && file["totalLines"] != nil {
				t.Fatal("unknown range promoted to complete")
			}
			if tc.rangeInfo == nil {
				want := strings.Count(tc.text, "\n") + 1
				if tc.text == "" {
					want = 0
				}
				if file["numLines"] != float64(want) || file["totalLines"] != float64(want) {
					t.Fatal("native Read line count changed", file)
				}
			}
			if _, err := c.Sink.Apply(context.Background(), plan); err != nil {
				t.Fatal(err)
			}
			ownedPath := core.S(file["filePath"])
			data, err := os.ReadFile(ownedPath)
			if err != nil || string(data) != tc.text || !strings.HasPrefix(ownedPath, strings.TrimSuffix(plan.Target.Path, ".jsonl")+string(filepath.Separator)) {
				t.Fatal("snapshot has no independent owned file", ownedPath, err)
			}
			// Loading the saved snapshot must not depend on the asset still existing.
			if err := os.Remove(ownedPath); err != nil {
				t.Fatal(err)
			}
			var decoded core.SourceBatch
			err = engine.TailJSONL(context.Background(), core.NativeRef{Path: plan.Target.Path}, nil, Decoder{}, func(b core.SourceBatch) error { decoded = b; return nil }, 2000, false)
			if err != nil || len(decoded.Drafts) != 3 || decoded.Drafts[1].Kind != "attachment" {
				t.Fatal("readback lost native attachment", err)
			}
			readParts := decoded.Drafts[1].Parts
			got := readParts[len(readParts)-1]
			if core.S(core.Obj(got["blob"])["sha256"]) != core.SHA256Hex(tc.text) {
				t.Fatal("readback changed UTF8/CRLF/empty bytes", got)
			}
		})
	}
}

func TestTextSnapshotPreparationLeavesDocumentsAndToolResults(t *testing.T) {
	h := hub.New()
	defer h.Close()
	plain := core.Part{"type": "file", "mediaType": "text/plain", "name": "a.txt", "blob": h.PutBlob([]byte("text"))}
	document := core.Part{"type": "file", "document": true, "mediaType": "text/plain", "blob": plain["blob"]}
	pdf := core.Part{"type": "file", "mediaType": "application/pdf", "blob": h.PutBlob([]byte("%PDF"))}
	tool := core.Part{"type": "tool_result", "parts": core.PartsJSON([]core.Part{plain})}
	input := engine.PlanInput{Hub: h, Projection: &planner.Projection{Items: []planner.Item{
		{Role: "user", Event: &core.Event{Kind: "message", Actor: "user"}, Parts: []core.Part{document, pdf}},
		{Role: "tool", Event: &core.Event{Kind: "message", Actor: "tool"}, Parts: []core.Part{tool}},
		{Role: "assistant", Event: &core.Event{Kind: "message", Actor: "assistant"}, Parts: []core.Part{plain}},
		{Role: "user", Event: &core.Event{Kind: "notification", Actor: "user"}, Parts: []core.Part{plain}},
	}}}
	got, files := prepareTextSnapshots(input, t.TempDir())
	if len(files) != 0 || !reflect.DeepEqual(got.Projection, input.Projection) {
		t.Fatal("unrelated native document or role changed")
	}
	rows := renderCC(got.Projection.Items[3], got, ccCtx{})
	if len(rows) != 1 || !strings.Contains(core.Stringify(rows), `"type":"document"`) || !strings.Contains(core.Stringify(rows), `"turnOrigin":"task_notification"`) {
		t.Fatal("task notification lost file or became a human prompt", rows)
	}
}

func TestTextSnapshotNoticeIsNotDuplicatedOrRemovedFromOrdinaryText(t *testing.T) {
	h := hub.New()
	defer h.Close()
	note := "[File snapshot: a.txt, lines 5–6 of 19. Only this recorded range is included.]"
	file := core.Part{"type": "file", "mediaType": "text/plain", "name": "a.txt", "blob": h.PutBlob([]byte("x\n")), "snapshot": core.JsonObject{"startLine": 5, "numLines": 2, "totalLines": 19, "notice": note}}
	parts := []core.Part{{"type": "text", "text": note}, {"type": "text", "text": note, "attachmentSnapshotNotice": true}, file}
	input := engine.PlanInput{Hub: h, Projection: &planner.Projection{Items: []planner.Item{{Role: "user", Event: &core.Event{Kind: "message", Actor: "user"}, Parts: parts}}}}
	prepared, _ := prepareTextSnapshots(input, t.TempDir())
	kept, records := textSnapshotRecords(prepared, prepared.Projection.Items[0].Parts)
	if len(kept) != 1 || kept[0]["text"] != note || len(records) != 1 || core.S(core.Obj(parts[2]["snapshot"])["notice"]) != note {
		t.Fatal("notice duplicated, ordinary text removed, or input mutated", kept)
	}
}
