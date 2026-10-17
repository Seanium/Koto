package koto

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"koto/internal/core"
	"koto/internal/planner"
)

// MCP pull delivery: a stdio MCP server, so any harness that speaks MCP — including ones koto cannot
// write, like Antigravity — can list the sessions of the other harnesses and pull one in as a
// readable transcript (older turns summarized to fit a token budget). Read-only on harness files.
//
//	claude mcp add koto -- koto mcp

const mcpProtocol = "2025-06-18"

// What an MCP client can take: text only, tool use rendered as text.
var mcpCaps = core.Capabilities{Read: core.ReadCaps{Reasoning: "none"}, Write: &core.WriteCaps{Mode: "none", Reasoning: "none", Tools: "text"}, FormatVersions: []string{}}

var mcpTools = []any{
	map[string]any{
		"name":        "list_sessions",
		"description": "List coding-agent sessions from every harness on this machine (Claude Code, Codex, pi, Antigravity, …), newest first. Each has a selector to pass to get_session.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"query":   map[string]any{"type": "string", "description": "Only sessions whose title, working directory or selector contains this text."},
			"harness": map[string]any{"type": "string", "description": "Only sessions from this harness, e.g. claude-code, codex, pi, antigravity."},
			"limit":   map[string]any{"type": "number", "description": "Maximum rows (default 20)."},
		}},
	},
	map[string]any{
		"name":        "get_session",
		"description": "Get one session as a readable transcript (user / assistant turns, tool calls and results as text). Older turns are summarized when the transcript exceeds max_tokens.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"selector":   map[string]any{"type": "string", "description": "A selector from list_sessions (harness:session, th:… thread id, or event id)."},
			"max_tokens": map[string]any{"type": "number", "description": "Approximate token budget for the transcript (default 20000)."},
		}, "required": []any{"selector"}},
	},
}

// SessionRow is one conversation for MCP: selected by its first native session.
type SessionRow struct {
	Selector  string   `json:"selector"`
	Title     *string  `json:"title"`
	Cwd       *string  `json:"cwd"`
	Harnesses []string `json:"harnesses"`
	Turns     int      `json:"turns"`
	Updated   *string  `json:"updated"`
}

// SessionRows from the index (nothing parsed).
func (a *App) SessionRows(query, harness string, limit int) []SessionRow {
	if limit <= 0 {
		limit = 20
	}
	q := strings.ToLower(query)
	out := []SessionRow{}
	for _, t := range a.ListThreads() {
		bs := t.Bindings
		var origin *BindingView
		if len(bs) > 0 {
			origin = &bs[0]
		}
		if origin == nil {
			continue
		}
		var title, cwd *string
		var hs []string
		seen := map[string]bool{}
		hasH := false
		for _, b := range bs {
			if title == nil {
				for _, k := range []string{"title", "aiTitle", "name"} {
					if s, ok := b.Meta[k].(string); ok && s != "" {
						title = &s
						break
					}
				}
			}
			if cwd == nil {
				if s, ok := b.Meta["cwd"].(string); ok && s != "" {
					cwd = &s
				}
			}
			if !seen[b.Harness] {
				seen[b.Harness] = true
				hs = append(hs, b.Harness)
			}
			if b.Harness == harness {
				hasH = true
			}
		}
		sel := origin.Harness + ":" + origin.Session
		if harness != "" && !hasH {
			continue
		}
		if q != "" {
			match := strings.Contains(strings.ToLower(sel), q)
			for _, x := range []*string{title, cwd} {
				if x != nil && strings.Contains(strings.ToLower(*x), q) {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		r := SessionRow{Selector: sel, Title: title, Cwd: cwd, Harnesses: hs, Turns: t.Turns}
		if t.Updated != nil {
			s := time.UnixMilli(int64(*t.Updated)).UTC().Format("2006-01-02T15:04:05.000Z")
			r.Updated = &s
		}
		out = append(out, r)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// Transcript is a conversation as text for another Agent, parsed on demand from its native files.
func (a *App) Transcript(ctx context.Context, selector string, maxTokens int) (string, error) {
	if maxTokens <= 0 {
		maxTokens = 20000
	}
	v, err := a.OpenThread(ctx, selector)
	if err != nil {
		return "", err
	}
	var out string
	v.Engine.Read(func() {
		var head string
		if strings.HasPrefix(selector, "th:") {
			head = v.Hub.ThreadHead(v.Thread)
		} else {
			_, head, _ = v.Hub.ResolveHead(selector)
		}
		if head == "" {
			err = fmt.Errorf("nothing matches %s", selector)
			return
		}
		f := false
		p := planner.Project(v.Hub.Path(head), planner.Options{Sink: "mcp", SinkHarness: "mcp", Capabilities: mcpCaps, Level: "L2", MaxTokens: maxTokens, DeferIncomplete: &f})
		text := func(parts []core.Part) string {
			var b []string
			for _, x := range parts {
				if core.S(x["type"]) == "text" {
					b = append(b, core.S(x["text"]))
				} else {
					b = append(b, "["+core.S(x["type"])+"]")
				}
			}
			return strings.TrimSpace(strings.Join(b, "\n"))
		}
		var hs []string
		seen := map[string]bool{}
		for _, b := range v.Hub.Bindings(v.Thread) {
			k := b.Harness + ":" + b.Session
			if !seen[k] {
				seen[k] = true
				hs = append(hs, k)
			}
		}
		top := []string{"# Session " + selector, "Harnesses: " + strings.Join(hs, ", ")}
		if p.Hints.Cwd != "" {
			top = append(top, "Working directory: "+p.Hints.Cwd)
		}
		for _, n := range p.Notes {
			top = append(top, "Note: "+n)
		}
		parts := []string{strings.Join(top, "\n")}
		for _, it := range p.Items {
			role := "User"
			switch it.Role {
			case "assistant":
				role = "Assistant"
			case "tool":
				role = "Tool"
			}
			parts = append(parts, "## "+role+"\n"+text(it.Parts))
		}
		out = strings.Join(parts, "\n\n")
	})
	return out, err
}

// ServeMCP answers MCP on newline-delimited JSON-RPC 2.0 (stdin/stdout).
func (a *App) ServeMCP(ctx context.Context, in io.Reader, out io.Writer) error {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	send := func(m any) { _ = enc.Encode(m) }
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if json.Unmarshal([]byte(line), &msg) != nil {
			send(map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "parse error"}})
			continue
		}
		if msg.ID == nil {
			continue // notifications (initialized, cancelled, …)
		}
		id := msg.ID
		reply := func(r any) { send(map[string]any{"jsonrpc": "2.0", "id": id, "result": r}) }
		fail := func(code int, m string) {
			send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": m}})
		}
		switch msg.Method {
		case "initialize":
			pv, ok := msg.Params["protocolVersion"].(string)
			if !ok {
				pv = mcpProtocol
			}
			reply(map[string]any{"protocolVersion": pv, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "koto", "version": "0.1.0"}})
		case "ping":
			reply(map[string]any{})
		case "tools/list":
			reply(map[string]any{"tools": mcpTools})
		case "tools/call":
			name, _ := msg.Params["name"].(string)
			args := core.Obj(msg.Params["arguments"])
			a.Sessions.Scan(ctx, nil, nil)
			var text string
			var err error
			switch name {
			case "list_sessions":
				limit := 0
				if n, ok := core.Num(args["limit"]); ok {
					limit = int(n)
				}
				b, _ := json.MarshalIndent(a.SessionRows(core.S(args["query"]), core.S(args["harness"]), limit), "", "  ")
				text = string(b)
			case "get_session":
				sel, ok := args["selector"].(string)
				if !ok {
					err = fmt.Errorf("selector is required")
					break
				}
				mt := 0
				if n, ok := core.Num(args["max_tokens"]); ok {
					mt = int(n)
				}
				text, err = a.Transcript(ctx, sel, mt)
			default:
				fail(-32602, "unknown tool "+name)
				continue
			}
			if err != nil {
				reply(map[string]any{"content": []any{map[string]any{"type": "text", "text": err.Error()}}, "isError": true})
				continue
			}
			reply(map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}})
		default:
			fail(-32601, "method not found: "+msg.Method)
		}
	}
	return sc.Err()
}
