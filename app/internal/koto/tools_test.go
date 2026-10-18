package koto

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"koto/internal/core"
	"koto/internal/engine"
)

func TestPatchAsEdit(t *testing.T) {
	jsonEq(t, core.PatchAsEdit("*** Begin Patch\n*** Update File: src/a.ts\n@@ function f\n keep\n-x\n+y\n*** End Patch"),
		map[string]any{"op": "edit", "path": "src/a.ts", "edits": []any{map[string]any{"old": "keep\nx", "new": "keep\ny"}}})
	jsonEq(t, core.PatchAsEdit("*** Begin Patch\n*** Update File: a\n@@\n-1\n+2\n@@\n-3\n*** End Patch"),
		map[string]any{"op": "edit", "path": "a", "edits": []any{map[string]any{"old": "1", "new": "2"}, map[string]any{"old": "3", "new": ""}}})
	jsonEq(t, core.PatchAsEdit("*** Begin Patch\n*** Add File: n.txt\n+hello\n+world\n*** End Patch"),
		map[string]any{"op": "write", "path": "n.txt", "content": "hello\nworld\n"})
	ok(t, core.PatchAsEdit("*** Begin Patch\n*** Update File: a\n@@\n-1\n+2\n*** Update File: b\n@@\n-1\n+2\n*** End Patch") == nil, "two files")
	ok(t, core.PatchAsEdit("*** Begin Patch\n*** Delete File: a\n*** End Patch") == nil)
	ok(t, core.PatchAsEdit("*** Begin Patch\n*** Update File: a\n*** Move to: b\n@@\n-1\n+2\n*** End Patch") == nil)
	ok(t, core.PatchAsEdit("*** Begin Patch\n*** Update File: a\n@@\n+only added\n*** End Patch") == nil, "pure insertion has no anchor for Edit")
}

func contents(t *testing.T, file string, typ string) []map[string]any {
	var out []map[string]any
	for _, r := range readJSONL(t, file) {
		for _, c := range core.Arr(core.Obj(r["message"])["content"]) {
			if m := core.Obj(c); m["type"] == typ {
				out = append(out, m)
			}
		}
	}
	return out
}

func TestApplyPatchBecomesAStructuralEdit(t *testing.T) {
	s := newSandbox(t)
	writeCodexRollout(t, s.Codex, "", nil)
	a := s.app(enable("codex", "claude-code", "pi"))
	scan(t, a)
	cc := carry(t, a, engine.CarryOptions{Selector: "codex:" + CodexThread, To: "claude-code"})
	for _, l := range cc.Plan.Losses {
		ok(t, l.Field != "tool_call:apply_patch", "not textualized")
	}
	for _, u := range contents(t, cc.Plan.Target.Path, "tool_use") {
		if u["name"] == "Edit" {
			jsonEq(t, u["input"], map[string]any{"file_path": "src/a.ts", "old_string": "x", "new_string": "y"})
		}
	}
	pi := carry(t, a, engine.CarryOptions{Selector: "codex:" + CodexThread, To: "pi"})
	found := false
	for _, c := range contents(t, pi.Plan.Target.Path, "toolCall") {
		if c["name"] == "edit" {
			found = true
			jsonEq(t, c["arguments"], map[string]any{"path": "src/a.ts", "edits": []any{map[string]any{"oldText": "x", "newText": "y"}}})
		}
	}
	ok(t, found, "edit call")
}

func TestUnmodeledArgumentIsTextualized(t *testing.T) {
	s := newSandbox(t)
	session := "22222222-3333-4444-8555-666666666666"
	b := ccBase(session)
	call := func(id, name string, input map[string]any) map[string]any {
		return map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}
	}
	result := func(id, text string) map[string]any {
		return map[string]any{"type": "tool_result", "tool_use_id": id, "content": text}
	}
	user := func(uuid, parent string, n int, content any) core.O {
		return rec(core.O{{K: "parentUuid", V: parent}}, b, core.O{{K: "type", V: "user"}, {K: "message", V: map[string]any{"role": "user", "content": content}}, {K: "uuid", V: uuid}, {K: "timestamp", V: ts(n)}})
	}
	asst := func(uuid, parent string, n int, content []any, stop string) core.O {
		return rec(core.O{{K: "parentUuid", V: parent}}, b, core.O{{K: "type", V: "assistant"}, {K: "message", V: map[string]any{"model": "m", "id": "msg_" + uuid, "type": "message", "role": "assistant", "content": content, "stop_reason": stop}}, {K: "uuid", V: uuid}, {K: "timestamp", V: ts(n)}})
	}
	root := user("u1", "", 0, "start the dev server, read the README, list files")
	root[0].V = nil
	writeLines(t, filepath.Join(s.Claude, "projects", "-work-proj", session+".jsonl"), []any{
		root,
		asst("a1", "u1", 1, []any{
			call("toolu_bg", "Bash", map[string]any{"command": "npm run dev", "run_in_background": true}),
			call("toolu_read", "Read", map[string]any{"file_path": "/work/proj/README.md"}),
			call("toolu_ls", "Bash", map[string]any{"command": "ls", "description": "List files"}),
			call("toolu_grep", "Grep", map[string]any{"pattern": "todo", "-i": true}),
		}, "tool_use"),
		user("r1", "a1", 2, []any{result("toolu_bg", "started in background: shell 7")}),
		user("r2", "r1", 3, []any{result("toolu_read", "# Proj")}),
		user("r3", "r2", 4, []any{result("toolu_ls", "README.md")}),
		user("r4", "r3", 5, []any{result("toolu_grep", "README.md:1:TODO")}),
		asst("a2", "r4", 6, []any{map[string]any{"type": "text", "text": "Done."}}, "end_turn"),
	})
	a := s.app(enable("claude-code", "pi"))
	scan(t, a)
	r := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + session, To: "pi"})
	var fields []string
	for _, l := range r.Plan.Losses {
		if strings.HasPrefix(l.Field, "tool_call:") {
			fields = append(fields, l.Field)
		}
	}
	sort.Strings(fields)
	jsonEq(t, fields, []string{"tool_call:Bash", "tool_call:Grep"})
	var msgs []map[string]any
	for _, x := range readJSONL(t, r.Plan.Target.Path) {
		if x["type"] == "message" {
			msgs = append(msgs, core.Obj(x["message"]))
		}
	}
	var roles []string
	for _, m := range msgs {
		roles = append(roles, core.S(m["role"]))
	}
	jsonEq(t, roles, []string{"user", "assistant", "toolResult", "toolResult", "assistant"})
	var names, texts []string
	for _, c := range core.Arr(msgs[1]["content"]) {
		cm := core.Obj(c)
		if cm["type"] == "toolCall" {
			names = append(names, core.S(cm["name"]))
		}
		if cm["type"] == "text" {
			texts = append(texts, core.S(cm["text"]))
		}
	}
	jsonEq(t, names, []string{"read", "bash"}, "Read and the Bash whose only extra argument is a description stay structural")
	text := strings.Join(texts, "\n")
	ok(t, strings.Contains(text, "run_in_background"))
	ok(t, strings.Contains(text, `"-i":true`))
	var later []string
	for _, m := range msgs[4:] {
		var s []string
		for _, c := range core.Arr(m["content"]) {
			s = append(s, core.S(core.Obj(c)["text"]))
		}
		later = append(later, strings.Join(s, ""))
	}
	ok(t, regexp.MustCompile(`(?s)shell 7.*TODO`).MatchString(strings.Join(later, "\n")))
}
