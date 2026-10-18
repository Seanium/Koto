package koto

import (
	"bytes"
	"os"
	"sort"
	"strings"
	"testing"

	"koto/internal/connectors/claudecode"
	"koto/internal/core"
	"koto/internal/engine"
)

func TestClaudeCodeDecode(t *testing.T) {
	s := newSandbox(t)
	writeCcSession(t, s.Claude, "", true)
	a := s.app(enable("claude-code"))
	r := scan(t, a)
	eq(t, len(r.Errors), 0, "errors")
	b := binding(t, a.Hub, "claude-code", CCSession)
	eq(t, b.Meta["title"], any("demo session"), "title")
	path := a.Hub.Path(b.Leaf)
	jsonEq(t, kindsActors(path), []string{"message:user", "context:harness", "message:assistant", "message:assistant", "message:assistant",
		"tool_result:tool", "tool_result:tool", "message:assistant", "message:user", "message:assistant"}, "path")
	// both parallel results are on the single linear path (CC parents them on sibling tool_use records)
	eq(t, len(a.Hub.Leaves(b.Thread)), 1, "branches")
	// the persisted large output was pulled in as a file part
	r1 := path[5].Parts[0]
	eq(t, core.S(r1["type"]), "tool_result")
	var file core.Part
	for _, p := range core.Parts(r1["parts"]) {
		if p["type"] == "file" {
			file = p
		}
	}
	ok(t, file != nil, "file part")
	blob, _ := a.Hub.Blob(core.S(core.Obj(file["blob"])["sha256"]))
	eq(t, string(blob), strings.Repeat("line\n", 5000), "side file")
	call := core.Obj(path[3].Parts[0]["tool"])
	ok(t, call["op"] == "shell" && call["command"] == "ls -la", "shell")
	read := core.Obj(path[4].Parts[0]["tool"])
	rg := core.Arr(read["range"])
	ok(t, read["op"] == "read" && rg[0] == float64(1) && rg[1] == float64(50), "read range")
	// the user prompt after a system (hook) record attaches through the alias
	eq(t, path[8].Parents[0], path[7].ID, "alias parent")
	jsonEq(t, path[2].Attrs["model"], map[string]any{"provider": nil, "api": "anthropic-messages", "id": "claude-opus-5-5"}, "model")
}

func TestClaudeCodeResidualsReread(t *testing.T) {
	s := newSandbox(t)
	file := writeCcSession(t, s.Claude, "", true)
	a := s.app(enable("claude-code"))
	scan(t, a)
	raw, _ := os.ReadFile(file)
	lines := strings.Split(string(raw), "\n")
	for _, e := range a.Hub.ThreadEvents(binding(t, a.Hub, "claude-code", CCSession).Thread) {
		r := a.Engine.Residual(e)
		ok(t, r != nil, "residual for ", e.Provenance.Native.ID)
		found := false
		for _, l := range lines {
			if l == string(r) {
				found = true
			}
		}
		ok(t, found, "residual is a line")
		eq(t, core.SHA256Hex(r), e.Provenance.Residual.SHA256)
	}
}

func TestClaudeCodeL0CarryToAnotherRoot(t *testing.T) {
	s := newSandbox(t)
	other := newSandbox(t)
	file := writeCcSession(t, s.Claude, "", true)
	// two connector instances = two storage roots (e.g. the CLI and a Cowork session dir)
	a := s.app(enable("claude-code"), func(c *Config) {
		c.Extra = []*engine.Connector{claudecode.New(claudecode.Options{Root: other.Claude, ID: "claude-code@other"})}
	})
	_, err := a.Engine.Scan(ctx, engine.ScanOptions{Connectors: []string{"claude-code"}})
	must(t, err)
	res := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "claude-code@other"})
	eq(t, res.Plan.Level, "L0")
	eq(t, len(res.Plan.Losses), 0)
	out := readJSONL(t, res.Plan.Target.Path)
	var src []map[string]any
	for _, r := range readJSONL(t, file) {
		if r["type"] == "user" || r["type"] == "assistant" {
			src = append(src, r)
		}
	}
	eq(t, len(out), len(src), "records")
	for i := range out {
		strip := func(m map[string]any) map[string]any {
			c := map[string]any{}
			for k, v := range m {
				if k != "uuid" && k != "parentUuid" && k != "sessionId" {
					c[k] = v
				}
			}
			return c
		}
		if core.Obj(src[i]["toolUseResult"])["persistedOutputPath"] != nil {
			body := core.ToJSON(out[i])
			ok(t, strings.Contains(body, "document"), "side-file content is embedded independently")
			ok(t, strings.Contains(body, strings.Repeat("line\\n", 100)), "full result is preserved")
		} else {
			jsonEq(t, strip(out[i]), strip(src[i]), "record ", i)
		}
		if i == 0 {
			eq(t, out[i]["parentUuid"], nil)
		} else {
			eq(t, out[i]["parentUuid"], out[i-1]["uuid"])
		}
	}
}

func TestClaudeCodeIncrementalTail(t *testing.T) {
	s := newSandbox(t)
	full := writeCcSession(t, s.Claude, "", true, append(ccTurn(CCSession, "a5", 1, ""), ccTurn(CCSession, "xa1", 2, "")...)...)
	data, _ := os.ReadFile(full)
	// B watches the file grow in arbitrary chunks (including mid-line cuts)
	must(t, os.WriteFile(full, nil, 0o644))
	b := s.app(enable("claude-code"), func(c *Config) { c.DataDir = s.Root + "/hubB" })
	pos := 0
	for _, cut := range []int{37, 700, 701, 2000, 2400, 5100, len(data)} {
		if cut > len(data) {
			cut = len(data)
		}
		f, _ := os.OpenFile(full, os.O_WRONLY|os.O_APPEND, 0)
		_, _ = f.Write(data[pos:cut])
		f.Close()
		pos = cut
		scan(t, b)
	}
	// A reads the finished file once
	a := s.app(enable("claude-code"), func(c *Config) { c.DataDir = s.Root + "/hubA" })
	scan(t, a)
	ids := func(x *App) []string {
		var out []string
		for _, e := range x.Hub.ThreadEvents(binding(t, x.Hub, "claude-code", CCSession).Thread) {
			out = append(out, e.ID)
		}
		sort.Strings(out)
		return out
	}
	jsonEq(t, ids(b), ids(a), "ids")
	ob, _ := b.Hub.Offset(b.Registry.Get("claude-code").ID, CCSession)
	oa, _ := a.Hub.Offset(a.Registry.Get("claude-code").ID, CCSession)
	eq(t, ob.PrefixHash, oa.PrefixHash, "prefix")
}

func TestClaudeCodeAppendAndRewrite(t *testing.T) {
	s := newSandbox(t)
	file := writeCcSession(t, s.Claude, "", true)
	a := s.app(enable("claude-code"))
	scan(t, a)
	thread := binding(t, a.Hub, "claude-code", CCSession).Thread
	n0 := len(a.Hub.ThreadEvents(thread))
	appendLines(t, file, ccTurn(CCSession, "a5", 1, ""))
	r := scan(t, a)
	eq(t, r.Added, 2, "added")
	eq(t, len(a.Hub.ThreadEvents(thread)), n0+2)
	// rewrite: drop the last turn and replace it with a different one (rewind/edit)
	raw, _ := os.ReadFile(file)
	var lines [][]byte
	for _, l := range bytes.Split(raw, []byte("\n")) {
		if len(l) > 0 {
			lines = append(lines, l)
		}
	}
	must(t, os.WriteFile(file, append(bytes.Join(lines[:len(lines)-2], []byte("\n")), '\n'), 0o644))
	appendLines(t, file, ccTurn(CCSession, "a5", 1, "y"))
	scan(t, a)
	eq(t, len(a.Hub.Leaves(thread)), 2, "old branch kept, new branch from the divergence point")
	eq(t, len(a.Hub.ThreadEvents(thread)), n0+4)
}

func TestMixedReminderKeepsUserRequest(t *testing.T) {
	s := newSandbox(t)
	file := writeCcSession(t, s.Claude, "", false)
	rows := readJSONL(t, file)
	for _, r := range rows {
		if r["type"] == "user" {
			r["message"] = map[string]any{"role": "user", "content": "<system-reminder>DO NOT COPY ENVIRONMENT</system-reminder>\n真正的用户问题"}
			break
		}
	}
	all := make([]any, len(rows))
	for i, r := range rows {
		all[i] = r
	}
	writeLines(t, file, all)
	a := s.app(enable("claude-code", "pi"))
	scan(t, a)
	for _, to := range []string{"claude-code", "pi"} {
		r := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: to})
		content := text(t, r.Plan.Target.Path)
		ok(t, strings.Contains(content, "真正的用户问题"), "must preserve mixed user request")
		ok(t, !strings.Contains(content, "DO NOT COPY ENVIRONMENT"), "must not replay injected reminder")
	}
}
