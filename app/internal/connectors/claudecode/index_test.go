package claudecode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
)

func TestIndexSummaryMatchesFullWithoutMaterial(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "session.jsonl")
	material := []byte(strings.Repeat("中文-material", 8192))
	encoded := base64.StdEncoding.EncodeToString(material)
	asset := filepath.Join(root, "session", "attachments", core.SHA256Hex(material), "note.txt")
	if err := os.MkdirAll(filepath.Dir(asset), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(asset, material, 0600); err != nil {
		t.Fatal(err)
	}
	link := "[附件：note.txt](" + shared.FileURL(asset).String() + ")"
	rows := []core.JsonObject{
		{"type": "custom-title", "customTitle": "Index 中文"},
		{"type": "user", "uuid": "u0", "parentUuid": nil, "message": core.JsonObject{"content": "<system-reminder>environment</system-reminder>\n真实提问 中文"}},
		{"type": "assistant", "uuid": "a0", "parentUuid": "u0", "message": core.JsonObject{"model": "recorded-model", "content": []any{core.JsonObject{"type": "text", "text": "response"}, core.JsonObject{"type": "image", "source": core.JsonObject{"type": "base64", "data": encoded}}}}},
		{"type": "user", "uuid": "tool", "parentUuid": "a0", "message": core.JsonObject{"content": []any{core.JsonObject{"type": "tool_result", "tool_use_id": "read", "content": []any{core.JsonObject{"type": "text", "text": "parent tool result"}, core.JsonObject{"type": "image", "source": core.JsonObject{"type": "base64", "data": encoded}}}}}}},
		{"type": "user", "uuid": "notice", "parentUuid": "tool", "origin": core.JsonObject{"kind": "task-notification"}, "message": core.JsonObject{"content": "<task-notification>PARENT_RESULT</task-notification>"}},
		{"type": "user", "uuid": "u1", "parentUuid": "notice", "message": core.JsonObject{"content": []any{core.JsonObject{"type": "image", "source": core.JsonObject{"type": "base64", "data": encoded}}, core.JsonObject{"type": "document", "title": "doc", "source": core.JsonObject{"type": "base64", "data": encoded}}, core.JsonObject{"type": "text", "text": link}}}},
	}
	var raw []byte
	for i, row := range rows {
		row["timestamp"] = "2026-10-18T10:00:0" + string(rune('0'+i)) + "Z"
		row["cwd"] = root
		b, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(append(raw, b...), '\n')
	}
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	read := func(ctx context.Context) core.SourceBatch {
		var b core.SourceBatch
		if err := engine.TailJSONL(ctx, core.NativeRef{Path: file, Session: "session"}, nil, Decoder{}, func(x core.SourceBatch) error { b = x; return nil }, 2000, false); err != nil {
			t.Fatal(err)
		}
		return b
	}
	full, light := read(context.Background()), read(engine.WithIndexOnly(context.Background()))
	if len(full.Blobs) < 3 || len(light.Blobs) != 0 {
		t.Fatal("index retained material or fixture lacked it")
	}
	if !reflect.DeepEqual(full.Meta, light.Meta) || !reflect.DeepEqual(full.Offset, light.Offset) || !reflect.DeepEqual(full.Lineage, light.Lineage) {
		t.Fatal("metadata/checkpoint changed")
	}
	if len(full.Drafts) != len(light.Drafts) {
		t.Fatal("event count changed")
	}
	for i, d := range full.Drafts {
		l := light.Drafts[i]
		if d.Kind != l.Kind || d.Actor != l.Actor || d.NativeID != l.NativeID || !reflect.DeepEqual(d.Time, l.Time) || !reflect.DeepEqual(d.Parent, l.Parent) || !reflect.DeepEqual(d.Attrs["model"], l.Attrs["model"]) || !reflect.DeepEqual(d.Replaces, l.Replaces) {
			t.Fatalf("event %d changed list semantics", i)
		}
		if d.Actor == "user" && d.Kind == "message" && !reflect.DeepEqual(indexPrompt(d.Parts), indexPrompt(l.Parts)) {
			t.Fatalf("prompt %d changed", i)
		}
	}
	for i, r := range full.Records {
		r.Raw = nil
		if !reflect.DeepEqual(r, light.Records[i]) {
			t.Fatal("record identity changed")
		}
	}
	if after := read(context.Background()); !reflect.DeepEqual(full, after) {
		t.Fatal("index altered subsequent full material read")
	}
}

func indexPrompt(parts []core.Part) []any {
	visible, first := false, ""
	for _, p := range parts {
		if p["type"] == "text" || p["type"] == "image" || p["type"] == "file" || p["type"] == "opaque" {
			visible = true
		}
	}
	for _, p := range parts {
		if p["type"] == "text" {
			first = core.S(p["text"])
			break
		}
	}
	return []any{visible, first}
}

func TestSelectiveIndexParserPreservesNativeBindings(t *testing.T) {
	rows := []string{
		`{"type":"user","uuid":"u0","parentUuid":null,"timestamp":"2026-10-18T00:00:00Z","cwd":"/p","entrypoint":"claude-desktop","sessionId":"new","forkedFrom":{"sessionId":"original"},"message":{"content":"<system-reminder>x</system-reminder>完整提问"}}`,
		`{"type":"assistant","uuid":"a0","parentUuid":"u0","message":{"id":"m","model":"recorded-model","stop_reason":"end_turn","usage":{"input_tokens":5},"content":[{"type":"text","text":"skip"}]}}`,
		`{"type":"user","uuid":"result","parentUuid":"a0","message":{"content":[{"type":"tool_result","tool_use_id":"tool","content":[{"type":"image","source":{"type":"base64","data":"YQ=="}}]}]}}`,
		`{"type":"user","uuid":"notice","parentUuid":"result","origin":{"kind":"task-notification"},"message":{"content":"parent notification"}}`,
		`{"type":"user","uuid":"u1","parentUuid":"notice","message":{"content":[{"type":"image","source":{"type":"base64","data":""}},{"type":"image","source":{"type":"url","url":"https://example.com/image"}},{"type":"image","source":{"type":"url","url":""}},{"type":"image","source":{"type":"base64","data":null}},{"type":"document","source":{"type":"base64","data":"YQ=="}},{"type":"text","text":"完整正文"}]}}`,
		`{"type":"system","uuid":"boundary","subtype":"compact_boundary","logicalParentUuid":"u1","parentUuid":null}`,
		`{"type":"user","uuid":"compact","parentUuid":"u1","isCompactSummary":true,"message":{"content":"summary"}}`,
		`{"type":"user","uuid":"side","isSidechain":true,"isMeta":true,"message":{"content":"context"}}`,
		`{"type":"relocated","relocatedCwd":"/new"}`, `{"type":"custom-title","customTitle":"标题"}`,
		`{"type":"ai-title","aiTitle":"generated"}`, `{"type":"summary","summary":"short"}`,
		`{"type":"bridge-session","bridgeSessionId":"bridge"}`, `{"type":"unknown","uuid":"alias","parentUuid":"u1"}`,
		`{"type":"unknown","uuid":"no-parent"}`, `{"type":"attachment","rendered":[{"content":"skip"}]}`,
	}
	dec := Decoder{}
	stateA, stateB := dec.InitialState(), dec.InitialState()
	for i, raw := range rows {
		var ordinary any
		if err := json.Unmarshal([]byte(raw), &ordinary); err != nil {
			t.Fatal(err)
		}
		selective, err := dec.ParseIndex([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		base := engine.DecodeContext{IndexOnly: true, Index: i}
		a, b := base, base
		a.State = stateA
		b.State = stateB
		want := dec.Decode(ordinary, []byte(raw), &a)
		got := dec.Decode(selective, []byte(raw), &b)
		if !reflect.DeepEqual(want, got) || !reflect.DeepEqual(stateA, stateB) {
			t.Fatalf("record %d differs\nwant %+v\ngot %+v", i, want, got)
		}
	}
}
