package koto

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"koto/internal/core"
	"koto/internal/engine"
)

func TestCodexDecode(t *testing.T) {
	s := newSandbox(t)
	writeCodexRollout(t, s.Codex, "", nil)
	a := s.app(enable("codex"))
	r := scan(t, a)
	eq(t, len(r.Errors), 0)
	b := binding(t, a.Hub, "codex", CodexThread)
	eq(t, b.Meta["frontend"], any("cli"))
	p := a.Hub.Path(b.Leaf)
	jsonEq(t, kindsActors(p), []string{
		"meta:harness", "context:system", "context:user", "message:user", "reasoning:assistant", "tool_call:assistant", "tool_result:tool", "message:assistant",
		"meta:harness", "context:system", "message:user", "tool_call:assistant", "tool_result:tool", "message:assistant",
	})
	// model is per turn, not per session
	jsonEq(t, p[7].Attrs["model"], map[string]any{"provider": "acme", "api": "openai-responses", "id": "acme-coder"})
	jsonEq(t, p[13].Attrs["model"], map[string]any{"provider": "acme", "api": "openai-responses", "id": "acme-coder-mini"})
	rs := p[4].Parts[0]
	ok(t, rs["type"] == "reasoning" && rs["text"] == "Running the test suite." && core.Obj(rs["sealed"])["by"] == "openai-responses", "reasoning")
	call := core.Obj(p[5].Parts[0]["tool"])
	ok(t, call["op"] == "shell" && call["command"] == "npm test", "shell")
	ok(t, core.Obj(p[11].Parts[0]["tool"])["op"] == "patch", "patch")
}

func TestCodexCompressionIsAContainerChange(t *testing.T) {
	s := newSandbox(t)
	file := writeCodexRollout(t, s.Codex, "", nil)
	a := s.app(enable("codex"))
	scan(t, a)
	n := a.Hub.EventCount()
	raw, _ := os.ReadFile(file)
	must(t, os.WriteFile(file+".zst", core.ZstdCompress(raw), 0o644))
	must(t, os.Remove(file))
	r := scan(t, a)
	eq(t, len(r.Errors), 0)
	eq(t, a.Hub.EventCount(), n, "no new events")
	off, _ := a.Hub.Offset(a.Registry.Get("codex").ID, CodexThread)
	eq(t, off.Records, len(codexRecords(t, "")))
	// and it still tails: Codex re-materializes plain JSONL before appending
	recs := codexRecords(t, "")
	var more []any
	for i, r := range recs[len(recs)-2:] {
		c := map[string]any{}
		for k, v := range r {
			c[k] = v
		}
		c["ordinal"] = float64(100 + i)
		pl := map[string]any{}
		for k, v := range core.Obj(r["payload"]) {
			pl[k] = v
		}
		pl["id"] = "z" + string(rune('0'+i))
		c["payload"] = pl
		more = append(more, c)
	}
	z, _ := os.ReadFile(file + ".zst")
	plain, err := core.ZstdDecompressAll(z)
	must(t, err)
	must(t, os.WriteFile(file, plain, 0o644))
	must(t, os.Remove(file+".zst"))
	appendLines(t, file, more)
	scan(t, a)
	eq(t, a.Hub.EventCount(), n+2)
	// residuals survive the container change
	for _, e := range a.Hub.ThreadEvents(binding(t, a.Hub, "codex", CodexThread).Thread) {
		ok(t, a.Engine.Residual(e) != nil, "residual ", e.Provenance.Native.ID)
	}
}

func TestCodexForkLineage(t *testing.T) {
	s := newSandbox(t)
	writeCodexRollout(t, s.Codex, "", nil)
	child := "01a0ec12-0000-7000-8000-000000000001"
	first := codexRecords(t, child)[0]
	pl := core.Obj(first["payload"])
	pl["forked_from_id"], pl["forked_from_ordinal_exclusive"] = CodexThread, float64(12)
	q := map[string]any{"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "ordinal": 12, "type": "response_item", "payload": map[string]any{"type": "message", "role": "user",
		"content": []any{map[string]any{"type": "input_text", "text": "forked question"}}, "internal_chat_message_metadata_passthrough": map[string]any{"content_item_kinds": []any{"user.text"}}}}
	writeCodexRollout(t, s.Codex, child, []map[string]any{first, q})
	a := s.app(enable("codex"))
	scan(t, a)
	parent := binding(t, a.Hub, "codex", CodexThread)
	kid := binding(t, a.Hub, "codex", child)
	eq(t, kid.Thread, parent.Thread, "same thread")
	p := a.Hub.Path(kid.Leaf)
	eq(t, p[len(p)-2].Provenance.Native.ID, "o11", "forked after ordinal 11 of the parent")
}

func TestCodexNativeCopy(t *testing.T) {
	if !hasCodex() {
		t.Skip("codex CLI not installed")
	}
	s := newSandbox(t)
	ccFile := writeCcSession(t, s.Claude, "", false)
	a := s.app(enable("claude-code", "codex"))
	scan(t, a)
	sel := "claude-code:" + CCSession
	// The fixture's cwd (/work/proj) does not exist here: Codex would silently skip → refused.
	missing := carry(t, a, engine.CarryOptions{Selector: sel, To: "codex"})
	eq(t, missing.Plan.Mode, "reject")
	ok(t, !missing.Applied)
	ok(t, strings.Contains(strings.Join(missing.Plan.Notes, "\n"), "does not exist"))
	cwd := filepath.Join(s.Root, "work")
	must(t, os.Mkdir(cwd, 0o755))
	plan := carry(t, a, engine.CarryOptions{Selector: sel, To: "codex", DryRun: true, Cwd: cwd})
	eq(t, plan.Plan.Mode, "create")
	hasLoss := func(f func(core.Loss) bool) bool {
		for _, l := range plan.Plan.Losses {
			if f(l) {
				return true
			}
		}
		return false
	}
	ok(t, !hasLoss(func(l core.Loss) bool { return strings.HasPrefix(l.Field, "tool_call") }), "native tool calls remain structured")
	ok(t, hasLoss(func(l core.Loss) bool { return l.Field == "reasoning" }), "cross-provider thinking becomes readable text")

	r1 := carry(t, a, engine.CarryOptions{Selector: sel, To: "codex", Cwd: cwd})
	ok(t, r1.Applied)
	threadID := core.S(r1.Receipt["threadId"])
	ok(t, regexp.MustCompile(`^[0-9a-f-]{36}$`).MatchString(threadID), "thread id")
	// the user's ~/.claude is untouched; the shadow lives under the configured shadow root
	ok(t, core.S(r1.Receipt["shadowPath"]) == "", "no intermediate transcript")
	rollout := core.S(r1.Receipt["path"])
	_, err := os.Stat(rollout)
	ok(t, strings.HasPrefix(rollout, s.Codex) && err == nil)
	texts := func() string {
		var b strings.Builder
		for _, r := range readJSONL(t, rollout) {
			if r["type"] == "response_item" {
				x, _ := json.Marshal(r["payload"])
				b.Write(x)
			}
		}
		return b.String()
	}
	ok(t, strings.Contains(texts(), "List the files and read the README"))
	// scanning Codex now must not create events from our own import (anti-echo)
	n := a.Hub.EventCount()
	scan(t, a)
	eq(t, a.Hub.EventCount(), n, "anti-echo")
	ok(t, binding(t, a.Hub, "codex", threadID).Thread != binding(t, a.Hub, "claude-code", CCSession).Thread)
	// A second click creates an independent native thread.
	second := carry(t, a, engine.CarryOptions{Selector: sel, To: "codex", Cwd: cwd})
	ok(t, second.Applied && second.Receipt["threadId"] != r1.Receipt["threadId"])
	// More source turns still create an independent native session.
	appendLines(t, ccFile, ccTurn(CCSession, "a5", 1, ""))
	r2 := carry(t, a, engine.CarryOptions{Selector: sel, To: "codex", Cwd: cwd})
	eq(t, r2.Plan.Mode, "create")
	ok(t, core.S(r2.Receipt["threadId"]) != threadID)
	rollout = core.S(r2.Receipt["path"])
	ok(t, strings.Contains(texts(), "x question 1"))
	n2 := a.Hub.EventCount()
	scan(t, a)
	eq(t, a.Hub.EventCount(), n2)
	_, err = os.Stat(filepath.Join(s.Codex, "external_agent_session_imports.json"))
	ok(t, os.IsNotExist(err), "no native importer ledger")
}

func TestCodexThreadNames(t *testing.T) {
	s := newSandbox(t)
	a := s.app(enable("codex"))
	writeCodexRollout(t, s.Codex, "", nil)
	idx := filepath.Join(s.Codex, "session_index.jsonl")
	line := func(id, name, at string) string {
		b, _ := json.Marshal(map[string]any{"id": id, "thread_name": name, "updated_at": at})
		return string(b)
	}
	must(t, os.WriteFile(idx, []byte(strings.Join([]string{
		line(CodexThread, "旧名字", "2026-10-15T10:00:00Z"), line("other", "x", "2026-10-15T10:00:00Z"), line(CodexThread, "修登录页", "2026-10-15T11:00:00Z"), `{"id": "torn`,
	}, "\n")), 0o644))
	scan(t, a)
	eq(t, binding(t, a.Hub, "codex", CodexThread).Meta["title"], any("修登录页"))
	f, _ := os.OpenFile(idx, os.O_WRONLY|os.O_APPEND, 0)
	_, _ = f.WriteString("\n" + line(CodexThread, "改名了", "2026-10-15T12:00:00Z") + "\n")
	f.Close()
	r := scan(t, a)
	eq(t, r.Changed, 0, "the rollout itself did not change")
	eq(t, binding(t, a.Hub, "codex", CodexThread).Meta["title"], any("改名了"))
}
