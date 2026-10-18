package koto

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"koto/internal/core"
)

func TestNativeChildEventCanReopenAfterViewEviction(t *testing.T) {
	s := newSandbox(t)
	writeCodexRollout(t, s.Codex, "", nil)
	child := "01a0ec12-0000-7000-8000-000000000001"
	first := codexRecords(t, child)[0]
	pl := core.Obj(first["payload"])
	pl["forked_from_id"], pl["forked_from_ordinal_exclusive"] = CodexThread, float64(12)
	q := map[string]any{"ordinal": 12, "type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "child-only question"}}}}
	writeCodexRollout(t, s.Codex, child, []map[string]any{first, q})
	a := s.app(enable("codex", "pi"))
	api := newAPI(a)
	eq(t, len(api.Reindex(ctx).Errors), 0)
	v, err := a.OpenThread(ctx, "codex:"+child)
	must(t, err)
	head := v.Hub.Binding("codex", child, a.Registry.Get("codex").ID).Leaf
	a.Sessions.SweepViews(time.Now().Add(3 * time.Minute))
	eq(t, len(a.Sessions.Views()), 0)
	detail, err := api.Thread(ctx, head, 30, "")
	must(t, err)
	ok(t, len(detail.Events) > 0 && detail.Events[len(detail.Events)-1].ID == head, "ev selector reopened ancestor instead of child")
	a.Sessions.SweepViews(time.Now().Add(3 * time.Minute))
	result, err := api.Carry(ctx, CarryRequest{From: head, To: "pi", DryRun: true, Cwd: s.Root})
	must(t, err)
	ok(t, result.Plan.Mode != "reject" && strings.Contains(result.Plan.Content, "child-only question"), "copy lost ev-selected child after eviction")
}

func TestSelfContainedNativeForkSurvivesMissingOriginal(t *testing.T) {
	t.Run("claude", func(t *testing.T) {
		s := newSandbox(t)
		records := ccLinear(CCSession, 2, "copied")
		for i, record := range records {
			records[i] = setKey(record.(core.O), "forkedFrom", map[string]any{"sessionId": "missing-original"})
		}
		writeLines(t, filepath.Join(ccDir(s.Claude), CCSession+".jsonl"), records)
		a := s.app(enable("claude-code"))
		api := newAPI(a)
		eq(t, len(api.Reindex(ctx).Errors), 0)
		v, err := a.OpenThread(ctx, "claude-code:"+CCSession)
		must(t, err)
		eq(t, len(v.Group.Members), 1)
		eq(t, len(v.Hub.Path(v.Hub.Binding("claude-code", CCSession, a.Registry.Get("claude-code").ID).Leaf)), 4)
	})
	t.Run("pi", func(t *testing.T) {
		s := newSandbox(t)
		file := writePiSession(t, s.Pi, "")
		records := readJSONL(t, file)
		records[0]["parentSession"] = filepath.Join(s.Pi, "missing-original.jsonl")
		var values []any
		for _, record := range records {
			values = append(values, record)
		}
		writeLines(t, file, values)
		a := s.app(enable("pi"))
		api := newAPI(a)
		eq(t, len(api.Reindex(ctx).Errors), 0)
		v, err := a.OpenThread(ctx, "pi:"+PISession)
		must(t, err)
		eq(t, len(v.Group.Members), 1)
		ok(t, len(v.Hub.Path(v.Hub.Binding("pi", PISession, a.Registry.Get("pi").ID).Leaf)) > 0)
	})
}

func TestReferenceNativeForkRejectsMissingOriginal(t *testing.T) {
	s := newSandbox(t)
	first := codexRecords(t, CodexThread)[0]
	pl := core.Obj(first["payload"])
	pl["forked_from_id"], pl["forked_from_ordinal_exclusive"] = "missing-original", float64(12)
	writeCodexRollout(t, s.Codex, CodexThread, []map[string]any{first})
	a := s.app(enable("codex"))
	api := newAPI(a)
	eq(t, len(api.Reindex(ctx).Errors), 0)
	_, err := a.OpenThread(ctx, "codex:"+CodexThread)
	ok(t, err != nil && strings.Contains(err.Error(), "required parent session unavailable"), "reference fork silently omitted its history")
}
