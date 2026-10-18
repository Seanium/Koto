package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"koto/internal/core"
	"koto/internal/engine"
)

func TestIndexSummaryMatchesFullWithoutMaterial(t *testing.T) {
	file := filepath.Join(t.TempDir(), "rollout.jsonl")
	encoded := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("中文-material", 8192)))
	rows := []core.JsonObject{
		{"type": "session_meta", "payload": core.JsonObject{"id": "session", "cwd": "/project", "model_provider": "provider", "creator_account_id": "creator", "forked_from_id": "parent"}},
		{"type": "turn_context", "payload": core.JsonObject{"model": "recorded-model", "model_provider": "provider"}},
		{"type": "response_item", "payload": core.JsonObject{"type": "message", "role": "user", "content": []any{core.JsonObject{"type": "input_text", "text": "<environment_context>not a human question</environment_context>"}}}},
		{"type": "response_item", "payload": core.JsonObject{"type": "message", "role": "user", "content": []any{core.JsonObject{"type": "input_text", "text": "实际提问 中文"}, core.JsonObject{"type": "input_image", "image_url": "data:image/png;base64," + encoded}}}},
		{"type": "response_item", "payload": core.JsonObject{"type": "reasoning", "summary": []any{core.JsonObject{"text": "private reasoning"}}, "encrypted_content": encoded}},
		{"type": "response_item", "payload": core.JsonObject{"type": "function_call", "call_id": "read", "name": "read_file", "arguments": "{\"file\":\"context.txt\"}"}},
		{"type": "response_item", "payload": core.JsonObject{"type": "function_call_output", "call_id": "read", "output": []any{core.JsonObject{"type": "input_image", "image_url": "data:image/png;base64," + encoded}}}},
		{"type": "response_item", "payload": core.JsonObject{"type": "message", "role": "assistant", "phase": "final_answer", "content": []any{core.JsonObject{"type": "output_text", "text": "answer"}}}},
		{"type": "compacted", "payload": core.JsonObject{"message": "summary", "replacement_history": []any{core.JsonObject{"type": "message", "content": []any{core.JsonObject{"type": "input_image", "image_url": "data:image/png;base64," + encoded}}}}}},
		{"type": "response_item", "payload": core.JsonObject{"type": "message", "role": "user", "content": []any{core.JsonObject{"type": "input_audio", "audio_url": "data:audio/wav;base64," + encoded}}}},
	}
	var raw []byte
	for i, row := range rows {
		row["timestamp"] = "2026-10-18T10:00:0" + string(rune('0'+i)) + "Z"
		b, e := json.Marshal(row)
		if e != nil {
			t.Fatal(e)
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
		if d.Kind != l.Kind || d.Actor != l.Actor || d.NativeID != l.NativeID || !reflect.DeepEqual(d.Time, l.Time) || !reflect.DeepEqual(d.Parent, l.Parent) || !reflect.DeepEqual(d.Attrs, l.Attrs) || !reflect.DeepEqual(d.Replaces, l.Replaces) {
			t.Fatalf("event %d changed list semantics", i)
		}
		if d.Actor == "user" && !reflect.DeepEqual(indexPrompt(d.Parts), indexPrompt(l.Parts)) {
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

func TestSelectiveIndexParserMatchesOrdinaryParse(t *testing.T) {
	rows := []string{
		`{"type":"session_meta","ordinal":0,"timestamp":"2026-10-18T00:00:00Z","payload":{"id":"id","cwd":"/x","source":"cli","creator_account_id":"creator","creator_user_id":"user","model_provider":"p","forked_from_id":"parent","forked_from_ordinal_exclusive":4,"base_instructions":{"text":"skip"}}}`,
		`{"type":"session_meta","payload":{"id":"id","history_base":{"thread_id":"base","end_ordinal_exclusive":7}}}`,
		`{"type":"session_meta","payload":{"id":"id","parent_thread_id":"parent"}}`,
		`{"type":"turn_context","payload":{"model":"model","cwd":"/x","model_provider":"p","effort":"low","developer_instructions":"skip"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":"full 中文 text"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>x</environment_context>"},{"type":"input_image","image_url":"data:image/png;base64,YQ=="},{"type":"input_audio","audio_url":"data:audio/wav;base64,Yg=="}],"internal_chat_message_metadata_passthrough":{"content_item_kinds":["context.environment"]}}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"text","text":"<environment_context>literal user</environment_context>"}],"internal_chat_message_metadata_passthrough":{"content_item_kinds":["user.text"]}}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[],"internal_chat_message_metadata_passthrough":[]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"skip"}]}}`,
		`{"type":"response_item","payload":{"type":"function_call_output","call_id":"c","output":[{"type":"input_image","image_url":"skip"}]}}`,
		`{"type":"response_item","payload":{"type":"reasoning","encrypted_content":"skip","summary":[{"text":"skip"}]}}`,
		`{"type":"compacted","payload":{"message":"skip","replacement_history":[{"content":"skip"}]}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"duplicated user prompt"}}`,
		`{"payload":null}`, `[]`, `null`,
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
		base := engine.DecodeContext{IndexOnly: true, Index: i, Ref: core.NativeRef{Session: "id_rollout"}}
		a, b := base, base
		a.State = stateA
		b.State = stateB
		want := dec.Decode(ordinary, []byte(raw), &a)
		got := dec.Decode(selective, []byte(raw), &b)
		if !reflect.DeepEqual(want, got) || !reflect.DeepEqual(stateA, stateB) {
			t.Fatalf("record %d differs\nwant %+v\ngot %+v", i, want, got)
		}
	}
	if _, err := dec.ParseIndex([]byte(`{"type":`)); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}

func BenchmarkSelectiveIndexParserLargeBodies(b *testing.B) {
	for _, kind := range []string{"tool", "image"} {
		payload := core.JsonObject{"type": "function_call_output", "output": strings.Repeat("x", 2<<20)}
		if kind == "image" {
			payload = core.JsonObject{"type": "message", "role": "user", "content": []any{core.JsonObject{"type": "input_text", "text": "完整提问"}, core.JsonObject{"type": "input_image", "image_url": "data:image/png;base64," + strings.Repeat("A", 2<<20)}}}
		}
		raw, _ := json.Marshal(core.JsonObject{"type": "response_item", "payload": payload})
		b.Run(kind+"/ordinary", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				var out any
				if err := json.Unmarshal(raw, &out); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(kind+"/selective", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, err := (Decoder{}).ParseIndex(raw); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
