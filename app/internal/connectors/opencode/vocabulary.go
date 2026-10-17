package opencode

import "koto/internal/core"

type Vocabulary struct{}

func (Vocabulary) ToCanonical(name string, args any) map[string]any {
	a := core.Obj(args)
	switch name {
	case "read":
		t := map[string]any{"op": "read", "path": core.S(a["filePath"])}
		var off, lim *float64
		if n, ok := core.Num(a["offset"]); ok {
			off = &n
		}
		if n, ok := core.Num(a["limit"]); ok {
			lim = &n
		}
		if r := core.RangeFromOffsetLimit(off, lim); r != nil {
			t["range"] = r
		}
		return t
	case "write":
		return map[string]any{"op": "write", "path": core.S(a["filePath"]), "content": a["content"]}
	case "edit":
		return map[string]any{"op": "edit", "path": core.S(a["filePath"]), "edits": []any{map[string]any{"old": a["oldString"], "new": a["newString"]}}, "all": a["replaceAll"]}
	case "bash":
		return map[string]any{"op": "shell", "command": core.S(a["command"])}
	case "grep":
		return map[string]any{"op": "search", "query": core.S(a["pattern"]), "scope": a["path"]}
	case "glob":
		return map[string]any{"op": "list", "pattern": core.S(a["pattern"]), "path": a["path"]}
	}
	return map[string]any{"op": "raw"}
}
func (Vocabulary) FromCanonical(t map[string]any) (core.NativeToolCall, bool) {
	switch core.S(t["op"]) {
	case "read":
		a := map[string]any{"filePath": core.S(t["path"])}
		if off, lim := core.OffsetLimitFromRange(core.Arr(t["range"])); off != nil {
			a["offset"] = *off
			if lim != nil {
				a["limit"] = *lim
			}
		}
		return core.NativeToolCall{Name: "read", Args: a}, true
	case "write":
		if s, ok := t["content"].(string); ok {
			return core.NativeToolCall{Name: "write", Args: map[string]any{"filePath": core.S(t["path"]), "content": s}}, true
		}
	case "edit":
		es := core.Arr(t["edits"])
		if len(es) == 1 {
			e := core.Obj(es[0])
			return core.NativeToolCall{Name: "edit", Args: map[string]any{"filePath": core.S(t["path"]), "oldString": core.S(e["old"]), "newString": core.S(e["new"])}}, true
		}
	case "shell":
		return core.NativeToolCall{Name: "bash", Args: map[string]any{"command": core.S(t["command"]), "description": "Copied tool execution"}}, true
	case "search":
		return core.NativeToolCall{Name: "grep", Args: map[string]any{"pattern": core.S(t["query"]), "path": t["scope"]}}, true
	case "list":
		p := core.S(t["pattern"])
		if p == "" {
			p = "*"
		}
		return core.NativeToolCall{Name: "glob", Args: map[string]any{"pattern": p, "path": t["path"]}}, true
	}
	return core.NativeToolCall{}, false
}
