package koto

// Read native ancestry and revisions without maintaining a product version tree.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"koto/internal/connectors/dsh"
	"koto/internal/core"
	"koto/internal/engine"
)

const (
	CCA = "aaaaaaaa-2222-4333-8444-555555555555"
	CCF = "ffffffff-2222-4333-8444-555555555555"
)

func ccDir(root string) string {
	return filepath.Join(root, "projects", nonAlnum.ReplaceAllString(CWD, "-"))
}

// ccLinear is a linear CC session: n turns, prefix-named uuids.
func ccLinear(session string, n int, prefix string) []any {
	var recs []any
	var parent any
	for i := 1; i <= n; i++ {
		tn := ccTurn(session, "", i, prefix)
		tn[0] = setKey(tn[0].(core.O), "parentUuid", parent)
		recs = append(recs, tn...)
		parent = getKey(tn[1].(core.O), "uuid")
	}
	return recs
}

func setKey(o core.O, k string, v any) core.O {
	out := slices.Clone(o)
	for i := range out {
		if out[i].K == k {
			out[i].V = v
			return out
		}
	}
	return append(out, core.KV{K: k, V: v})
}

func getKey(o core.O, k string) any {
	for _, kv := range o {
		if kv.K == k {
			return kv.V
		}
	}
	return nil
}

func delKey(o core.O, k string) core.O {
	return slices.DeleteFunc(slices.Clone(o), func(kv core.KV) bool { return kv.K == k })
}

// ccFork is what CC's /branch (and --fork-session) writes: the chain copied with the same uuids, a new sessionId, forkedFrom.
func ccFork(from, to string, recs []any) []any {
	var parent any
	var out []any
	for _, r := range recs {
		o := r.(core.O)
		c := setKey(setKey(o, "sessionId", to), "parentUuid", parent)
		c = append(c, core.KV{K: "forkedFrom", V: core.O{{K: "sessionId", V: from}, {K: "messageUuid", V: getKey(o, "uuid")}}})
		parent = getKey(o, "uuid")
		out = append(out, c)
	}
	return out
}

func eventOf(t *testing.T, a *App, harness, session, native string) string {
	t.Helper()
	r := a.Hub.LookupNative(harness, session, native)
	if r == nil || r.Event == "" {
		t.Fatalf("no node for %s:%s %s", harness, session, native)
	}
	return r.Event
}

func at(path []*core.Event, i int) *core.Event { return path[len(path)+i] }

func ageFile(t *testing.T, f string) {
	old := time.Now().Add(-time.Minute)
	must(t, os.Chtimes(f, old, old))
}

func TestVersionModelCcFork(t *testing.T) {
	for _, withFrom := range []bool{true, false} {
		s := newSandbox(t)
		orig := ccLinear(CCA, 3, "x")
		writeLines(t, filepath.Join(ccDir(s.Claude), CCA+".jsonl"), orig)
		a := s.app(enable("claude-code"))
		scan(t, a)
		th := binding(t, a.Hub, "claude-code", CCA).Thread
		before := len(a.Hub.ThreadEvents(th))
		// fork after turn 2, then a new turn in the fork
		fork := ccFork(CCA, CCF, orig[:4])
		if !withFrom {
			for i := range fork {
				fork[i] = delKey(fork[i].(core.O), "forkedFrom")
			}
		}
		ageFile(t, filepath.Join(ccDir(s.Claude), CCA+".jsonl"))
		writeLines(t, filepath.Join(ccDir(s.Claude), CCF+".jsonl"), append(fork, ccTurn(CCF, "xa2", 9, "f")...))
		scan(t, a)
		f := binding(t, a.Hub, "claude-code", CCF)
		eq(t, f.Thread, th, "the fork joins the original thread")
		eq(t, len(a.Hub.ThreadEvents(th)), before+2, "only the new turn added nodes")
		eq(t, at(a.Hub.Path(f.Leaf), -3).ID, eventOf(t, a, "claude-code", CCA, "xa2"), "the new turn grows from the fork point")
	}
}

func writeDshSession(t *testing.T, home, id string, recs []any) {
	dir := filepath.Join(home, "sessions", dsh.ProjectKey(CWD), dsh.EncodeSegment(id))
	must(t, os.MkdirAll(dir, 0o755))
	var b bytes.Buffer
	for _, r := range recs {
		b.WriteString(core.Stringify(r) + "\n")
	}
	must(t, os.WriteFile(filepath.Join(dir, "session.v4.jsonl.zstd"), core.ZstdCompress(b.Bytes()), 0o644))
}

func dshTurn(seq, n int, p string) []any {
	return []any{
		map[string]any{"type": "turn/start", "seq": seq, "time": seq, "data": map[string]any{"turn": n}},
		map[string]any{"type": "user/message", "seq": seq + 1, "time": seq, "data": map[string]any{"id": p + "u" + itoa(n), "role": "user", "source": map[string]any{"kind": "user"}, "content": []any{map[string]any{"type": "text", "text": p + " question " + itoa(n)}}}, "surfaceOp": "append"},
		map[string]any{"type": "assistant/message", "seq": seq + 2, "time": seq, "data": map[string]any{"turn": n, "step": 1, "message": map[string]any{"id": p + "a" + itoa(n), "role": "assistant", "source": map[string]any{"kind": "model", "provider": "demo", "model": "demo-1"}, "content": []any{map[string]any{"type": "text", "text": p + " answer " + itoa(n)}}}, "stream": []any{}}, "surfaceOp": "append"},
		map[string]any{"type": "turn/end", "seq": seq + 3, "time": seq, "data": map[string]any{"turn": n, "reason": map[string]any{"kind": "completed"}}},
	}
}

func TestVersionModelDshSeededFork(t *testing.T) {
	s := newSandbox(t)
	head := map[string]any{"type": "session", "version": 4, "id": "par", "createdAt": 0, "cwd": CWD, "isSeeded": false, "delegationDepth": 0}
	body := slices.Concat(dshTurn(0, 1, "x"), dshTurn(4, 2, "x"), dshTurn(8, 3, "x"))
	writeDshSession(t, s.Dsh, "par", append([]any{head}, body...))
	a := s.app(enable("dsh"))
	scan(t, a)
	th := binding(t, a.Hub, "dsh", "par").Thread
	before := len(a.Hub.ThreadEvents(th))
	// fork after turn 2: inherited events seq 0..7, the cut marker, then the fork's own turn
	child := map[string]any{"type": "session", "version": 4, "id": "kid", "createdAt": 9, "cwd": CWD, "parentSession": "par", "isSeeded": true, "origin": "fork", "delegationDepth": 0}
	recs := slices.Concat([]any{child}, body[:8], []any{map[string]any{"type": "session/end-seed", "seq": 8, "time": 9, "data": map[string]any{"inherited": true}}}, dshTurn(9, 3, "k"))
	writeDshSession(t, s.Dsh, "kid", recs)
	scan(t, a)
	k := binding(t, a.Hub, "dsh", "kid")
	eq(t, k.Thread, th)
	eq(t, len(a.Hub.ThreadEvents(th)), before+2, "only the fork's own turn added nodes")
	eq(t, at(a.Hub.Path(k.Leaf), -3).ID, eventOf(t, a, "dsh", "par", "s6"))
}

func TestVersionModelPiFork(t *testing.T) {
	s := newSandbox(t)
	file := writePiSession(t, s.Pi, "")
	a := s.app(enable("pi"))
	scan(t, a)
	th := binding(t, a.Hub, "pi", PISession).Thread
	before := len(a.Hub.ThreadEvents(th))
	ls := readJSONL(t, file)
	const kid = "01a13866-f891-73d8-b45a-90076c4d7dd5"
	// path to pa2 copied (pi drops labels and re-chains), then a new turn
	recs := []any{map[string]any{"type": "session", "version": 3, "id": kid, "timestamp": "2026-10-15T11:00:00.000Z", "cwd": CWD, "parentSession": file}}
	var prev any
	for _, r := range ls[1:7] {
		c := map[string]any{}
		for k, v := range r {
			c[k] = v
		}
		c["parentId"] = prev
		prev = r["id"]
		recs = append(recs, c)
	}
	recs = append(recs, map[string]any{"type": "message", "id": "ku1", "parentId": "pa2", "timestamp": "2026-10-15T11:00:01.000Z", "message": map[string]any{"role": "user", "content": "fork question", "timestamp": 1}})
	writeLines(t, filepath.Join(filepath.Dir(file), "2026-10-15T11-00-00-000Z_"+kid+".jsonl"), recs)
	scan(t, a)
	kb := binding(t, a.Hub, "pi", kid)
	eq(t, kb.Thread, th)
	eq(t, len(a.Hub.ThreadEvents(th)), before+1)
	eq(t, at(a.Hub.Path(kb.Leaf), -2).ID, eventOf(t, a, "pi", PISession, "pa2"))
}

// ------------------------------------------------------------------ tree, any-node carry, graft

func msgTexts(t *testing.T, f string) []string {
	var out []string
	for _, r := range readJSONL(t, f) {
		if r["type"] != "message" {
			continue
		}
		switch c := core.Obj(r["message"])["content"].(type) {
		case string:
			out = append(out, c)
		case []any:
			var b strings.Builder
			for _, x := range c {
				b.WriteString(core.S(core.Obj(x)["text"]))
			}
			out = append(out, b.String())
		}
	}
	return out
}

// branchedCc: a CC session with 3 turns, copying on to pi and dsh.
func branchedCc(t *testing.T, s *sandbox) (*App, string, string) {
	file := filepath.Join(ccDir(s.Claude), CCA+".jsonl")
	writeLines(t, file, ccLinear(CCA, 3, "x"))
	a := s.app(enable("claude-code", "pi", "dsh"))
	scan(t, a)
	return a, file, binding(t, a.Hub, "claude-code", CCA).Thread
}

// ------------------------------------------------------------------ §5: nothing is lost

func TestVersionModelNothingLost(t *testing.T) {
	s := newSandbox(t)
	a, file, thread := branchedCc(t, s)
	h := a.Hub
	rows := func() map[string]string {
		out := map[string]string{}
		for _, e := range h.ThreadEvents(thread) {
			b, _ := json.Marshal(e)
			out[e.ID] = string(b)
		}
		return out
	}
	before := rows()

	// #3 the source is rewritten (CC drops turn 3 and writes another)
	xa3 := eventOf(t, a, "claude-code", CCA, "xa3")
	kept := strings.SplitAfterN(text(t, file), "\n", 5)[:4]
	must(t, os.WriteFile(file, []byte(strings.Join(kept, "")), 0o644))
	appendLines(t, file, ccTurn(CCA, "xa2", 3, "r"))
	scan(t, a)
	ok(t, h.HasEvent(xa3), "the old turn 3 is still a node")
	xa2 := eventOf(t, a, "claude-code", CCA, "xa2")
	eq(t, len(h.Children(xa2)), 2, "the rewrite grew a sibling branch")

	// #4 deliveries: create, then an interior-node carry
	p1 := carry(t, a, engine.CarryOptions{Head: xa3, To: "pi"})
	b1 := text(t, p1.Plan.Target.Path)
	carry(t, a, engine.CarryOptions{Selector: thread, To: "pi"})
	carry(t, a, engine.CarryOptions{Head: xa2, To: "dsh"})
	eq(t, text(t, p1.Plan.Target.Path), b1)
	scan(t, a)

	// #1 every node from before is still there, byte for byte
	now := rows()
	for id, r := range before {
		eq(t, now[id], r, "node ", id, " unchanged")
	}
	// #2 every node is an ancestor of a leaf (all parents)
	seen := map[string]bool{}
	stack := h.Leaves(thread)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		stack = append(stack, h.GetEvent(id).Parents...)
	}
	eq(t, len(seen), len(h.ThreadEvents(thread)))
	for _, e := range h.ThreadEvents(thread) {
		ok(t, seen[e.ID], e.ID, " unreachable")
	}
}
