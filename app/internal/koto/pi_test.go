package koto

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"koto/internal/core"
	"koto/internal/engine"
)

func TestPiDecode(t *testing.T) {
	s := newSandbox(t)
	writePiSession(t, s.Pi, "")
	a := s.app(enable("pi"))
	r := scan(t, a)
	eq(t, len(r.Errors), 0)
	b := binding(t, a.Hub, "pi", PISession)
	eq(t, b.Meta["title"], any("line counting"))
	eq(t, b.Meta["cwd"], any("/work/proj"))
	eq(t, len(a.Hub.Leaves(b.Thread)), 2, "pu2 and pu2b are siblings (retry)")
	path := a.Hub.Path(b.Leaf)
	jsonEq(t, kindsActors(path), []string{"meta:harness", "context:system", "message:user", "message:assistant", "tool_result:tool", "message:assistant", "message:user"})
	a1 := path[3]
	jsonEq(t, a1.Attrs["model"], map[string]any{"provider": "acme", "api": "openai-responses", "id": "acme-large"})
	ok(t, a1.Parts[0]["type"] == "reasoning" && a1.Parts[0]["signature"] == "opaque-sig")
	ok(t, a1.Parts[1]["type"] == "tool_call" && core.Obj(a1.Parts[1]["tool"])["op"] == "shell")
}

func TestPiResidualsAndFork(t *testing.T) {
	s := newSandbox(t)
	src := writePiSession(t, s.Pi, "")
	a := s.app(enable("pi"))
	scan(t, a)
	for _, e := range a.Hub.ThreadEvents(binding(t, a.Hub, "pi", PISession).Thread) {
		ok(t, a.Engine.Residual(e) != nil, "residual")
	}
	res := carry(t, a, engine.CarryOptions{Selector: "pi:" + PISession, To: "pi"})
	eq(t, res.Plan.Level, "L0")
	out := readJSONL(t, res.Plan.Target.Path)
	eq(t, out[0]["type"], any("session"))
	// context (system prompt) and meta are not migrated; conversation entries are verbatim except id/parentId
	var roles []string
	for _, r := range out[1:] {
		roles = append(roles, core.S(core.Obj(r["message"])["role"]))
	}
	jsonEq(t, roles, []string{"user", "assistant", "toolResult", "assistant", "user"})
	var srcA1 map[string]any
	for _, r := range readJSONL(t, src) {
		if r["id"] == "pa1" {
			srcA1 = r
		}
	}
	jsonEq(t, out[2]["message"], srcA1["message"])
	// the fork was read back as delivered: no new events, same thread
	before := a.Hub.EventCount()
	scan(t, a)
	eq(t, a.Hub.EventCount(), before, "no new events")
	ok(t, binding(t, a.Hub, "pi", core.S(res.Receipt["session"])).Thread != binding(t, a.Hub, "pi", PISession).Thread)
}

func TestPiCompactionAndContextEdit(t *testing.T) {
	s := newSandbox(t)
	file := writePiSession(t, s.Pi, "")
	tt := func(n int) string {
		return time.Date(2026, 10, 15, 11, 0, n, 0, time.UTC).Format("2006-01-02T15:04:05.000Z")
	}
	t2, _ := time.Parse(time.RFC3339, tt(2))
	appendLines(t, file, []any{
		map[string]any{"type": "compaction", "id": "c1", "parentId": "pa3", "timestamp": tt(0), "summary": "User counted lines in main.ts (42).", "firstKeptEntryId": "pu2", "tokensBefore": 1000},
		map[string]any{"type": "context_edit", "id": "ce1", "parentId": "c1", "timestamp": tt(1), "targetId": "pa3", "replacement": "Edited answer."},
		map[string]any{"type": "message", "id": "pu3", "parentId": "ce1", "timestamp": tt(2), "message": map[string]any{"role": "user", "content": "continue", "timestamp": t2.UnixMilli()}},
	})
	a := s.app(enable("pi", "claude-code"))
	scan(t, a)
	res := carry(t, a, engine.CarryOptions{Selector: "pi:" + PISession, To: "claude-code", DryRun: true})
	var texts []string
	for _, l := range strings.Split(res.Plan.Content, "\n") {
		if l == "" {
			continue
		}
		m := readLine(t, l)
		texts = append(texts, core.Stringify(core.Obj(m["message"])["content"]))
	}
	ok(t, strings.Contains(texts[0], "[Summary of the earlier conversation]"), "history before firstKept replaced by summary")
	has := func(s string) bool {
		for _, x := range texts {
			if strings.Contains(x, s) {
				return true
			}
		}
		return false
	}
	ok(t, !has("Count lines in main.ts"), "replaced range dropped")
	ok(t, has("And in util.ts?"), "kept range retained")
	ok(t, has("Edited answer.") && !has("I did not check util.ts yet."), "context edit applied")
	found := false
	for _, l := range res.Plan.Losses {
		if l.Field == "history" && l.Kind == "approximated" {
			found = true
		}
	}
	ok(t, found, "history loss")
	_ = filepath.Join
	_ = os.Stat
}
