package cursor

import "koto/internal/core"

type Vocabulary struct{}

func (Vocabulary) ToCanonical(name string, args any) core.JsonObject {
	a := core.Obj(args)
	switch name {
	case "Shell":
		return core.JsonObject{"op": "shell", "command": core.S(a["command"])}
	case "ReadFile", "Read":
		return core.JsonObject{"op": "read", "path": core.S(a["path"])}
	case "Glob":
		return core.JsonObject{"op": "list", "pattern": core.S(a["glob_pattern"]), "path": a["target_directory"]}
	case "Grep":
		return core.JsonObject{"op": "search", "query": core.S(a["pattern"]), "scope": a["path"]}
	default:
		return core.JsonObject{"op": "raw"}
	}
}

// These argument forms are verified in ordinary Cursor's native history.
// The replay writer still keeps the original call in its native MCP envelope;
// this inverse also lets the planner prove a source call is lossless before
// translating it to another Agent. Unrepresented arguments fail that proof.
func (Vocabulary) FromCanonical(t core.JsonObject) (core.NativeToolCall, bool) {
	switch core.S(t["op"]) {
	case "read":
		if p := core.S(t["path"]); p != "" && len(core.Arr(t["range"])) == 0 {
			return core.NativeToolCall{Name: "Read", Args: core.JsonObject{"path": p}}, true
		}
	case "list":
		if pattern := core.S(t["pattern"]); pattern != "" {
			args := core.JsonObject{"glob_pattern": pattern}
			if p := core.S(t["path"]); p != "" {
				args["target_directory"] = p
			}
			return core.NativeToolCall{Name: "Glob", Args: args}, true
		}
	}
	return core.NativeToolCall{}, false
}
