package koto

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"koto/internal/core"
	"koto/internal/engine"
)

type convTurn struct {
	Actor     string `json:"actor"`
	Text      string `json:"text"`
	Calls     []any  `json:"calls"`
	Reasoning int    `json:"reasoning"`
}

// conversation is a harness-neutral view of a conversation, used to compare across round trips.
func conversation(a *App, head string) []convTurn {
	var textOf func(ps []core.Part) string
	textOf = func(ps []core.Part) string {
		var out []string
		for _, p := range ps {
			var s string
			switch p["type"] {
			case "text":
				s = core.S(p["text"])
			case "tool_result":
				s = textOf(core.Parts(p["parts"]))
			case "file":
				s = "[file " + core.S(core.Obj(p["blob"])["sha256"])[:8] + "]"
			}
			if s != "" {
				out = append(out, s)
			}
		}
		return strings.Join(out, "\n")
	}
	var turns []convTurn
	for _, e := range a.Hub.Path(head) {
		if e.Kind != "message" && e.Kind != "tool_result" {
			continue
		}
		t := convTurn{Actor: e.Actor, Text: textOf(e.Parts), Calls: []any{}}
		for _, p := range e.Parts {
			switch p["type"] {
			case "tool_call":
				t.Calls = append(t.Calls, p["tool"])
			case "reasoning":
				t.Reasoning++
			}
		}
		// consecutive assistant records are one reply (CC splits per block; sinks fold them)
		if n := len(turns); n > 0 && turns[n-1].Actor == "assistant" && t.Actor == "assistant" {
			prev := turns[n-1]
			var txt []string
			for _, x := range []string{prev.Text, t.Text} {
				if x != "" {
					txt = append(txt, x)
				}
			}
			turns[n-1] = convTurn{Actor: "assistant", Text: strings.Join(txt, "\n"), Calls: append(prev.Calls, t.Calls...), Reasoning: prev.Reasoning + t.Reasoning}
		} else {
			turns = append(turns, t)
		}
	}
	return turns
}

func piUsage() map[string]any {
	return map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}
}

func piMsg(id, parent string, at time.Time, msg core.O) core.O {
	return core.O{{K: "type", V: "message"}, {K: "id", V: id}, {K: "parentId", V: parent}, {K: "timestamp", V: at.Format("2006-01-02T15:04:05.000Z")}, {K: "message", V: msg}}
}

func piUser(text string, at time.Time) core.O {
	return core.O{{K: "role", V: "user"}, {K: "content", V: text}, {K: "timestamp", V: at.UnixMilli()}}
}

func piAssistant(text string, at time.Time) core.O {
	return core.O{{K: "role", V: "assistant"}, {K: "content", V: []any{map[string]any{"type": "text", "text": text}}}, {K: "api", V: "openai-responses"}, {K: "provider", V: "acme"}, {K: "model", V: "acme-large"}, {K: "usage", V: piUsage()}, {K: "stopReason", V: "stop"}, {K: "timestamp", V: at.UnixMilli()}}
}

func at12(n int) time.Time { return time.Date(2026, 10, 15, 12, 0, n, 0, time.UTC) }

func lastID(t *testing.T, f string) string {
	r := readJSONL(t, f)
	return core.S(r[len(r)-1]["id"])
}

func TestSyncRoundTrip(t *testing.T) {
	s := newSandbox(t)
	writeCcSession(t, s.Claude, "", true)
	a := s.app(enable("claude-code", "pi"))
	scan(t, a)
	ccHead := binding(t, a.Hub, "claude-code", CCSession).Leaf
	toPi := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi"})
	eq(t, toPi.Plan.Level, "L1")
	for _, loss := range toPi.Plan.Losses {
		eq(t, loss.Kind, "referenced", "tools remain structured and side files retain original bytes")
	}

	// a fresh data dir that only sees the pi output
	b := s.app(enable("pi", "claude-code"), func(c *Config) { c.DataDir = s.Root + "/hub2"; c.ClaudeRoot = s.Root + "/claude2" })
	scan(t, b)
	piSession := core.S(toPi.Receipt["session"])
	orig, viaPi := conversation(a, ccHead), conversation(b, binding(t, b.Hub, "pi", piSession).Leaf)
	eq(t, len(viaPi), len(orig))
	for i := range orig {
		eq(t, viaPi[i].Actor, orig[i].Actor)
		jsonEq(t, viaPi[i].Calls, orig[i].Calls, "canonical tool ops survive ", i)
		eq(t, viaPi[i].Reasoning, orig[i].Reasoning)
		// the large persisted output was inlined as text for pi (CC's preview + full file)
		if !strings.Contains(orig[i].Text, "[file") {
			eq(t, viaPi[i].Text, orig[i].Text)
		}
	}
	// pi → CC (second hop) then CC → hub3: fixpoint — a further round trip changes nothing
	toCc := carry(t, b, engine.CarryOptions{Selector: "pi:" + piSession, To: "claude-code"})
	c := s.app(enable("claude-code"), func(c *Config) { c.DataDir = s.Root + "/hub3"; c.ClaudeRoot = s.Root + "/claude2" })
	scan(t, c)
	jsonEq(t, conversation(c, binding(t, c.Hub, "claude-code", core.S(toCc.Receipt["session"])).Leaf), viaPi)
}

func TestCopiesRemainIndependent(t *testing.T) {
	s := newSandbox(t)
	file := writeCcSession(t, s.Claude, "", true)
	a := s.app(enable("claude-code", "pi"))
	scan(t, a)
	r1 := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi"})
	eq(t, r1.Plan.Mode, "create")
	n0 := a.Hub.EventCount()
	scan(t, a)
	eq(t, a.Hub.EventCount(), n0, "scanning the pi output adds nothing")
	appendLines(t, file, ccTurn(CCSession, "a5", 1, ""))
	r2 := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi"})
	eq(t, r2.Plan.Mode, "create")
	ok(t, r2.Receipt["session"] != r1.Receipt["session"])
	ok(t, len(r2.Plan.Records) > 2)
	ls := readJSONL(t, r2.Plan.Target.Path)
	eq(t, ls[len(ls)-1]["parentId"], ls[len(ls)-2]["id"], "appended entries chain onto the delivered tail")
	eq(t, carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi"}).Plan.Mode, "create")
	scan(t, a)
	ok(t, a.Hub.EventCount() > n0+2, "each new copy is decoded independently")
}

func continueInPi(t *testing.T, piFile string) {
	appendLines(t, piFile, []any{
		piMsg("n1", lastID(t, piFile), at12(0), piUser("continue in pi", at12(0))),
		piMsg("n2", "n1", at12(1), piAssistant("pi answer", at12(1))),
	})
}

func TestSyncCarryBack(t *testing.T) {
	s := newSandbox(t)
	ccFile := writeCcSession(t, s.Claude, "", true)
	a := s.app(enable("claude-code", "pi"))
	scan(t, a)
	r1 := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi"})
	continueInPi(t, r1.Plan.Target.Path)
	scan(t, a)
	thread := binding(t, a.Hub, "claude-code", CCSession).Thread
	piSession := core.S(r1.Receipt["session"])
	ok(t, binding(t, a.Hub, "pi", piSession).Thread != thread, "copy is independent")
	before := len(readJSONL(t, ccFile))
	back := carry(t, a, engine.CarryOptions{Selector: "pi:" + piSession, To: "claude-code"})
	eq(t, back.Plan.Mode, "create")
	ok(t, back.Plan.Target.Session != CCSession)
	eq(t, len(readJSONL(t, ccFile)), before, "source unchanged")
	after := readJSONL(t, back.Plan.Target.Path)
	ok(t, len(after) > 2)
	eq(t, core.Obj(after[len(after)-1]["message"])["model"], any("acme-large"), "records the model that answered")
	n := a.Hub.EventCount()
	scan(t, a)
	eq(t, a.Hub.EventCount(), n, "no echo from the CC append")
}

func TestSyncDivergence(t *testing.T) {
	s := newSandbox(t)
	ccFile := writeCcSession(t, s.Claude, "", true)
	a := s.app(enable("claude-code", "pi"))
	scan(t, a)
	r1 := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi"})
	piFile := r1.Plan.Target.Path
	appendLines(t, piFile, []any{piMsg("z1", lastID(t, piFile), at12(0), core.O{{K: "role", V: "user"}, {K: "content", V: "pi side"}, {K: "timestamp", V: 1}})})
	appendLines(t, ccFile, ccTurn(CCSession, "a5", 1, ""))
	scan(t, a)
	thread := binding(t, a.Hub, "claude-code", CCSession).Thread
	eq(t, len(a.Hub.Leaves(thread)), 1)
	r2 := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi"})
	eq(t, r2.Plan.Mode, "create")
	ok(t, r2.Receipt["session"] != r1.Receipt["session"])
}

func TestSyncTargetChanged(t *testing.T) {
	s := newSandbox(t)
	writeCcSession(t, s.Claude, "", true)
	piSrc := writePiSession(t, s.Pi, "")
	a := s.app(enable("claude-code", "pi"))
	scan(t, a)
	r1 := carry(t, a, engine.CarryOptions{Selector: "pi:" + PISession, To: "claude-code"})
	_ = r1
	appendLines(t, piSrc, []any{piMsg("q1", "si", time.Date(2026, 10, 15, 13, 0, 0, 0, time.UTC), core.O{{K: "role", V: "user"}, {K: "content", V: "more"}, {K: "timestamp", V: 2}})})
	scan(t, a)
	pl, err := a.Engine.Plan(ctx, engine.CarryOptions{Selector: "pi:" + PISession, To: "claude-code"})
	must(t, err)
	eq(t, pl.Plan.Mode, "create")
	target := pl.Plan.Target.Path
	must(t, os.MkdirAll(filepath.Dir(target), 0700))
	must(t, os.WriteFile(target, []byte("unrelated data\n"), 0600))
	appendLines(t, target, []any{core.O{{K: "type", V: "mode"}, {K: "mode", V: "normal"}, {K: "sessionId", V: "x"}}}) // CC wrote meanwhile
	st, _ := os.Stat(target)
	_, err = a.Engine.ApplyPlan(ctx, pl.Sink, pl.Plan)
	ok(t, err != nil, err)
	st2, _ := os.Stat(target)
	eq(t, st2.Size(), st.Size())
}

func TestSyncTrailingToolCall(t *testing.T) {
	s := newSandbox(t)
	now := time.Now().UTC()
	b := core.O{{K: "isSidechain", V: false}, {K: "userType", V: "external"}, {K: "entrypoint", V: "cli"}, {K: "cwd", V: "/w"}, {K: "sessionId", V: CCSession}, {K: "version", V: "2.1.296"}, {K: "gitBranch", V: ""}}
	f := func(t time.Time) string { return t.Format("2006-01-02T15:04:05.000Z") }
	file := filepath.Join(s.Claude, "projects", "-w", CCSession+".jsonl")
	writeLines(t, file, []any{
		rec(core.O{{K: "parentUuid", V: nil}}, b, core.O{{K: "type", V: "user"}, {K: "message", V: core.O{{K: "role", V: "user"}, {K: "content", V: "run it"}}}, {K: "uuid", V: "u1"}, {K: "timestamp", V: f(now)}}),
		rec(core.O{{K: "parentUuid", V: "u1"}}, b, core.O{{K: "type", V: "assistant"}, {K: "message", V: core.O{{K: "model", V: "m"}, {K: "id", V: "msg"}, {K: "type", V: "message"}, {K: "role", V: "assistant"},
			{K: "content", V: []any{ccToolUse("toolu_x", "Bash", core.O{{K: "command", V: "sleep 100"}})}}, {K: "stop_reason", V: "tool_use"}}}, {K: "uuid", V: "a1"}, {K: "timestamp", V: f(now.Add(time.Millisecond))}}),
	})
	a := s.app(enable("claude-code", "pi"))
	scan(t, a)
	live := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi", DryRun: true})
	eq(t, len(live.Plan.Records), 1, "only the user prompt; the pending call waits")
	ok(t, strings.Contains(strings.Join(live.Plan.Notes, "\n"), "deferred"))
	no := false
	stale := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi", DryRun: true, DeferIncomplete: &no})
	eq(t, len(stale.Plan.Records), 3)
	ok(t, slices.ContainsFunc(stale.Plan.Losses, func(l core.Loss) bool { return l.Kind == "approximated" && strings.Contains(l.Note, "synthetic") }))
}

func TestSyncFoldsSplitReply(t *testing.T) {
	s := newSandbox(t)
	writeCcSession(t, s.Claude, "", false)
	a := s.app(enable("claude-code", "pi"))
	scan(t, a)
	r := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi"})
	var msgs []map[string]any
	for _, x := range readJSONL(t, r.Plan.Target.Path) {
		if x["type"] == "message" {
			msgs = append(msgs, core.Obj(x["message"]))
		}
	}
	i := slices.IndexFunc(msgs, func(m map[string]any) bool { return m["role"] == "assistant" })
	var types []any
	for _, c := range msgs[i]["content"].([]any) {
		types = append(types, core.Obj(c)["type"])
	}
	jsonEq(t, types, []string{"thinking", "toolCall", "toolCall"})
	jsonEq(t, []any{msgs[i+1]["role"], msgs[i+2]["role"]}, []string{"toolResult", "toolResult"})
	n := a.Hub.EventCount()
	scan(t, a) // re-reading the folded record is a no-op
	eq(t, a.Hub.EventCount(), n)
}
