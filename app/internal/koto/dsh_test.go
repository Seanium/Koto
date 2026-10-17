package koto

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/connectors/dsh"
	"koto/internal/core"
	"koto/internal/engine"
)

var dshV4 = []any{
	map[string]any{"type": "session", "version": 4, "id": "demo", "createdAt": 0, "cwd": "/work/proj", "isSeeded": false, "delegationDepth": 0},
	map[string]any{"type": "turn/start", "seq": 0, "time": 1, "data": map[string]any{"turn": 1}},
	map[string]any{"type": "user/message", "seq": 1, "time": 1, "data": map[string]any{"id": "u", "role": "user", "source": map[string]any{"kind": "user"}, "content": []any{map[string]any{"type": "text", "text": "Check."}}}, "surfaceOp": "append"},
	map[string]any{"type": "step/start", "seq": 2, "time": 1, "data": map[string]any{"turn": 1, "step": 1}},
	map[string]any{"type": "assistant/message", "seq": 3, "time": 2, "data": map[string]any{"turn": 1, "step": 1, "message": map[string]any{"id": "a", "role": "assistant", "source": map[string]any{"kind": "model", "provider": "demo", "model": "demo-1"},
		"content": []any{map[string]any{"type": "reasoning", "text": "Check first."}, map[string]any{"type": "text", "text": "Checking."}, map[string]any{"type": "tool-call", "id": "c", "name": "bash", "arguments": `{"command":"ls"}`}}}, "stream": []any{}}, "surfaceOp": "append"},
	map[string]any{"type": "tool/call", "seq": 4, "time": 2, "data": map[string]any{"turn": 1, "step": 1, "callId": "c", "name": "bash", "arguments": `{"command":"ls"}`}},
	map[string]any{"type": "tool/result", "seq": 5, "time": 3, "data": map[string]any{"turn": 1, "step": 1, "message": map[string]any{"id": "r", "role": "tool", "source": map[string]any{"kind": "tool", "callId": "c"}, "toolCallId": "c", "content": []any{map[string]any{"type": "text", "text": "OK"}}}}, "surfaceOp": "append"},
	map[string]any{"type": "step/end", "seq": 6, "time": 3, "data": map[string]any{"turn": 1, "step": 1}},
	map[string]any{"type": "assistant/message", "seq": 7, "time": 4, "data": map[string]any{"turn": 1, "step": 2, "message": map[string]any{"id": "b", "role": "assistant", "source": map[string]any{"kind": "model", "provider": "demo", "model": "demo-1"}, "content": []any{map[string]any{"type": "text", "text": "Done."}}}, "stream": []any{}}, "surfaceOp": "append"},
	map[string]any{"type": "turn/end", "seq": 8, "time": 4, "data": map[string]any{"turn": 1, "reason": map[string]any{"kind": "completed"}}},
}

func jsonlBytes(recs []any) []byte {
	var b bytes.Buffer
	for _, r := range recs {
		b.WriteString(core.Stringify(r) + "\n")
	}
	return b.Bytes()
}

// writeDsh writes as dsh does: one frame for the header, then one frame per append batch.
func writeDsh(t *testing.T, home string, recs []any) string {
	dir := filepath.Join(home, "sessions", dsh.ProjectKey("/work/proj"), dsh.EncodeSegment("demo"))
	must(t, os.MkdirAll(dir, 0o755))
	file := filepath.Join(dir, "session.v4.jsonl.zstd")
	var out []byte
	out = append(out, core.ZstdCompress(jsonlBytes(recs[:1]))...)
	out = append(out, core.ZstdCompress(jsonlBytes(recs[1:5]))...)
	out = append(out, core.ZstdCompress(jsonlBytes(recs[5:]))...)
	must(t, os.WriteFile(file, out, 0o644))
	return file
}

var dshTools = filepath.Join("..", "..", "..", ".tools", "dsh", "node_modules", "@deepseek-ai", "dsh-session-persistence-jsonl")

func haveDsh() bool {
	_, err := os.Stat(dshTools)
	_, nerr := exec.LookPath("node")
	return err == nil && nerr == nil
}

// dshVerify runs dsh's own log verifier (installed project-locally) on a session file.
func dshVerify(t *testing.T, file string) {
	t.Helper()
	if !haveDsh() {
		return
	}
	raw, _ := os.ReadFile(file)
	text, err := core.ZstdDecompressAll(raw)
	must(t, err)
	var lines []string
	for _, l := range strings.Split(string(text), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	var head map[string]any
	must(t, json.Unmarshal([]byte(lines[0]), &head))
	out, err := exec.Command("node", "testdata/dsh-verify.mjs", file, core.S(head["id"]), itoa(len(lines)-1)).CombinedOutput()
	ok(t, err == nil && strings.TrimSpace(string(out)) == `{"ok":true}`, "dsh's own verifier: ", string(out))
}

func itoa(n int) string    { return strings.TrimSpace(strings.Repeat(" ", 0) + jsonNum(n)) }
func jsonNum(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestDshDecode(t *testing.T) {
	s := newSandbox(t)
	file := writeDsh(t, s.Dsh, dshV4)
	a := s.app(enable("dsh"))
	scan(t, a)
	b := binding(t, a.Hub, "dsh", "demo")
	p := a.Hub.Path(b.Leaf)
	jsonEq(t, kindsActors(p), []string{"message:user", "message:assistant", "tool_result:tool", "message:assistant"}, "all three zstd frames decoded")
	jsonEq(t, p[1].Attrs["model"], map[string]any{"provider": "demo", "api": nil, "id": "demo-1"})
	// a new frame appended later: a compaction that replaces seq 1..7
	f, _ := os.OpenFile(file, os.O_WRONLY|os.O_APPEND, 0)
	_, _ = f.Write(core.ZstdCompress(jsonlBytes([]any{map[string]any{"type": "user/message", "seq": 9, "time": 5, "data": map[string]any{"id": "s", "role": "user", "source": map[string]any{"kind": "user"}, "content": []any{map[string]any{"type": "text", "text": "Summary: checked, OK."}}}, "surfaceOp": map[string]any{"op": "replace", "startSeq": 1, "endSeq": 7}}})))
	f.Close()
	scan(t, a)
	last := a.Hub.GetEvent(binding(t, a.Hub, "dsh", "demo").Leaf)
	eq(t, last.Kind, "compaction")
	jsonEq(t, last.Replaces, map[string]any{"from": p[0].ID, "to": p[3].ID})
}

func TestDshSinkWritesV4(t *testing.T) {
	s := newSandbox(t)
	writeCcSession(t, s.Claude, "", false)
	a := s.app(enable("claude-code", "dsh"))
	scan(t, a)
	r := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "dsh"})
	ok(t, r.Applied)
	a2 := s.app(enable("dsh"), func(c *Config) { c.DataDir = s.Root + "/hub2" })
	scan(t, a2)
	b := binding(t, a2.Hub, "dsh", core.S(r.Receipt["session"]))
	// CC's three records of one reply (thinking, two tool calls) are written as one assistant turn
	jsonEq(t, kindsActors(a2.Hub.Path(b.Leaf)), []string{"context:system", "message:user", "message:assistant", "tool_result:tool", "tool_result:tool", "message:assistant", "message:user", "message:assistant"})
	raw, _ := os.ReadFile(r.Plan.Target.Path)
	end, err := core.ZstdFrameEnd(raw, 0)
	must(t, err)
	first, err := core.ZstdDecompressAll(raw[:end]) // first frame = header only
	must(t, err)
	var head map[string]any
	must(t, json.Unmarshal(bytes.TrimSpace(first), &head))
	eq(t, head["version"], any(float64(4)))
	dshVerify(t, r.Plan.Target.Path)
}

func TestDshCopiesLeavePriorSessionUntouched(t *testing.T) {
	s := newSandbox(t)
	file := writeCcSession(t, s.Claude, "", false)
	a := s.app(enable("claude-code", "dsh"))
	scan(t, a)
	first := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "dsh"})
	before := text(t, first.Plan.Target.Path)
	appendLines(t, file, ccTurn(CCSession, "a5", 1, ""))
	scan(t, a)
	second := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "dsh"})
	eq(t, second.Plan.Mode, "create")
	ok(t, first.Plan.Target.Session != second.Plan.Target.Session)
	eq(t, text(t, first.Plan.Target.Path), before)
	dshVerify(t, second.Plan.Target.Path)
}

func TestDshIndependentProcessesCreateSeparateSessions(t *testing.T) {
	s := newSandbox(t)
	ccFile := writeCcSession(t, s.Claude, "", false)
	a1 := s.app(enable("claude-code", "dsh"))
	scan(t, a1)
	sel := "claude-code:" + CCSession
	carry(t, a1, engine.CarryOptions{Selector: sel, To: "dsh", RequestID: "racing"})
	appendLines(t, ccFile, ccTurn(CCSession, "a5", 1, ""))
	scan(t, a1)
	a2 := s.app(enable("claude-code", "dsh")) // a second process on the same data
	p1, err := a1.Engine.Plan(ctx, engine.CarryOptions{Selector: sel, To: "dsh", RequestID: "shared"})
	must(t, err)
	p2, err := a2.Engine.Plan(ctx, engine.CarryOptions{Selector: sel, To: "dsh", RequestID: "shared"})
	must(t, err)
	eq(t, p1.Plan.Mode, "create")
	// same head → same bytes (compared decoded: the frames are compressed the same way)
	ok(t, p1.Plan.ID != p2.Plan.ID, "separate process attempts have separate identities")
	_, err = a1.Engine.ApplyPlan(ctx, p1.Sink, p1.Plan)
	must(t, err)
	// the other process plans only now, from its stale view: still the same bytes, not a second turn on top
	p3, err := a2.Engine.Plan(ctx, engine.CarryOptions{Selector: sel, To: "dsh", RequestID: "shared"})
	must(t, err)
	ok(t, p3.Plan.Mode == "noop" || p3.Plan.ID == p2.Plan.ID, "no second turn")
	_, err = a2.Engine.ApplyPlan(ctx, p2.Sink, p2.Plan)
	must(t, err)
	raw, _ := os.ReadFile(p1.Plan.Target.Path)
	text, _ := core.ZstdDecompressAll(raw)
	eq(t, strings.Count(string(text), "x question 1"), 1, "the turn is in the log once")
	dshVerify(t, p1.Plan.Target.Path)

}
