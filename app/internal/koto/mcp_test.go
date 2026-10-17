package koto

import (
	"bufio"
	"encoding/json"
	"io"
	"regexp"
	"testing"
)

func TestMCP(t *testing.T) {
	s := newSandbox(t)
	writeCcSession(t, s.Claude, "", false)
	writeCodexRollout(t, s.Codex, "", nil)
	a := s.app(enable("claude-code", "codex"))
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- a.ServeMCP(ctx, inR, outW); outW.Close() }()
	lines := bufio.NewScanner(outR)
	lines.Buffer(make([]byte, 1<<20), 64<<20)
	id := 0
	rpc := func(method string, params any) map[string]any {
		id++
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		_, _ = inW.Write(append(b, '\n'))
		ok(t, lines.Scan(), "reply")
		var r map[string]any
		must(t, json.Unmarshal(lines.Bytes(), &r))
		eq(t, r["id"], any(float64(id)))
		return r
	}
	text := func(r map[string]any) string {
		return r["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	}
	init := rpc("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "0"}})
	eq(t, init["result"].(map[string]any)["protocolVersion"], any("2025-06-18"))
	_, _ = inW.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")) // no reply expected
	tools := rpc("tools/list", map[string]any{})
	var names []string
	for _, x := range tools["result"].(map[string]any)["tools"].([]any) {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	jsonEq(t, names, []string{"list_sessions", "get_session"})
	var rows []map[string]any
	must(t, json.Unmarshal([]byte(text(rpc("tools/call", map[string]any{"name": "list_sessions", "arguments": map[string]any{"harness": "codex"}}))), &rows))
	eq(t, len(rows), 1)
	eq(t, rows[0]["selector"], any("codex:"+CodexThread))
	cc := text(rpc("tools/call", map[string]any{"name": "get_session", "arguments": map[string]any{"selector": "claude-code:" + CCSession}}))
	ok(t, regexp.MustCompile(`## User\nList the files and read the README`).MatchString(cc))
	ok(t, regexp.MustCompile(`\$ ls -la`).MatchString(cc), "tool calls rendered as text")
	ok(t, regexp.MustCompile(`# Proj\nA demo project\.`).MatchString(cc), "tool results included")
	small := text(rpc("tools/call", map[string]any{"name": "get_session", "arguments": map[string]any{"selector": "codex:" + CodexThread, "max_tokens": 40}}))
	ok(t, regexp.MustCompile(`Session handoff checkpoint`).MatchString(small), "older turns summarized under a tight budget")
	bad := rpc("tools/call", map[string]any{"name": "get_session", "arguments": map[string]any{"selector": "nope:x"}})
	eq(t, bad["result"].(map[string]any)["isError"], any(true))
	eq(t, rpc("nope", map[string]any{})["error"].(map[string]any)["code"], any(float64(-32601)))
	inW.Close()
	must(t, <-done)
}
