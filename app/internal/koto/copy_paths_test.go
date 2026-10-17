package koto

// Copy selected native paths into independent sessions; never append to earlier targets.

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"koto/internal/core"
	"koto/internal/engine"
)

func jsonlFiles(t *testing.T, root string, skip string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".jsonl") && (skip == "" || !strings.Contains(p, skip)) {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func text(t *testing.T, f string) string {
	b, err := os.ReadFile(f)
	must(t, err)
	return string(b)
}

func ccRec(uuid, parent string, sec int, rest core.O) core.O {
	return rec(core.O{{K: "parentUuid", V: parent}}, ccBase(CCSession), core.O{{K: "uuid", V: uuid}, {K: "timestamp", V: time.Date(2026, 10, 15, 10, 30, sec, 0, time.UTC).Format("2006-01-02T15:04:05.000Z")}}, rest)
}

func ccUser(content any) core.O {
	return core.O{{K: "type", V: "user"}, {K: "message", V: core.O{{K: "role", V: "user"}, {K: "content", V: content}}}}
}

func ccAssistant(id string, content []any, stop string) core.O {
	return core.O{{K: "type", V: "assistant"}, {K: "message", V: core.O{{K: "model", V: "claude-opus-5-5"}, {K: "id", V: id}, {K: "type", V: "message"}, {K: "role", V: "assistant"}, {K: "content", V: content}, {K: "stop_reason", V: stop}}}}
}

func ccText(s string) core.O { return core.O{{K: "type", V: "text"}, {K: "text", V: s}} }

func ccToolUse(id, name string, input core.O) core.O {
	return core.O{{K: "type", V: "tool_use"}, {K: "id", V: id}, {K: "name", V: name}, {K: "input", V: input}}
}

// withTime replaces a record's timestamp.
func withTime(r core.O, ts string) core.O {
	out := slices.Clone(r)
	for i := range out {
		if out[i].K == "timestamp" {
			out[i].V = ts
		}
	}
	return out
}

func TestBranchCopyPathTail(t *testing.T) {
	s := newSandbox(t)
	ccFile := writeCcSession(t, s.Claude, "", true)
	parent := "a5"
	var recs []any
	for n := 0; n < 30; n++ {
		recs = append(recs, ccTurn(CCSession, parent, n, "")...)
		parent = "xa" + itoa(n)
	}
	appendLines(t, ccFile, recs)
	a := s.app(enable("claude-code"))
	scan(t, a)
	_, head, err := a.Hub.ResolveHead("claude-code:" + CCSession)
	must(t, err)
	all := a.Hub.Path(head)
	last, off, total := a.Hub.PathTail(head, 5, "", 0)
	eq(t, total, len(all))
	eq(t, off+len(last), len(all))
	for i, e := range last {
		eq(t, e.ID, all[off+i].ID)
	}
	users := 0
	for _, e := range last {
		if e.Kind == "message" && e.Actor == "user" && slices.ContainsFunc(e.Parts, func(p core.Part) bool { return p["type"] == "text" }) {
			users++
		}
	}
	eq(t, users, 5)
	before, boff, _ := a.Hub.PathTail(head, 5, last[0].ID, 0)
	eq(t, boff+len(before), off)
	_, roff, _ := a.Hub.PathTail(head, 500, before[0].ID, 0)
	eq(t, roff, 0)
	ok(t, a.Hub.IsAncestor(all[0].ID, head) && !a.Hub.IsAncestor(head, all[0].ID))
}

func TestBranchCopyHeaderOnly(t *testing.T) {
	s := newSandbox(t)
	a := s.app(enable("claude-code"))
	f := writeCcSession(t, s.Claude, "", true)
	other := "11111111-1111-4111-8111-111111111111"
	must(t, os.WriteFile(filepath.Join(filepath.Dir(f), other+".jsonl"), []byte(core.Stringify(core.O{{K: "type", V: "permission-mode"}, {K: "permissionMode", V: "default"}, {K: "sessionId", V: other}})+"\n"), 0o644))
	scan(t, a)
	eq(t, len(a.Hub.Threads()), 1)
}

func TestBranchCopyHoldsUnansweredQuestion(t *testing.T) {
	s := newSandbox(t)
	ccFile := writeCcSession(t, s.Claude, "", true)
	appendLines(t, ccFile, []any{ccRec("q1", "a5", 0, ccUser("still thinking about this"))})
	a := s.app(enable("claude-code", "pi"))
	scan(t, a)
	yes := true
	live := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi", DeferIncomplete: &yes})
	ok(t, !strings.Contains(text(t, live.Plan.Target.Path), "still thinking"))
	appendLines(t, ccFile, []any{ccRec("q1a", "q1", 5, ccAssistant("m_q1", []any{ccText("done thinking")}, "end_turn"))})
	scan(t, a)
	r := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi", DeferIncomplete: &yes})
	eq(t, r.Plan.Mode, "create")
	body := text(t, r.Plan.Target.Path)
	ok(t, strings.Contains(body, "still thinking") && strings.Index(body, "still thinking") < strings.Index(body, "done thinking"))
}
