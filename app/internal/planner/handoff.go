package planner

import (
	"regexp"
	"strings"

	"koto/internal/core"
)

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + " …"
	}
	return s
}

var (
	wsRe        = regexp.MustCompile(`\s+`)
	patchFileRe = regexp.MustCompile(`(?m)^\*\*\* (?:Add|Update|Delete) File: (.+)$`)
)

type orderedSet struct {
	seen  map[string]bool
	items []string
}

func (s *orderedSet) add(x string) {
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	if !s.seen[x] {
		s.seen[x] = true
		s.items = append(s.items, x)
	}
}

func lastN(xs []string, n int) []string {
	if len(xs) > n {
		return xs[len(xs)-n:]
	}
	return xs
}

// HandoffSummary is the deterministic L3 checkpoint: goal, later requests, files, commands, last state, open calls.
func HandoffSummary(items []Item) string {
	var users, assistants []string
	for _, it := range items {
		t := strings.TrimSpace(textOf(it.Parts))
		if t == "" {
			continue
		}
		if it.Role == "user" && it.Synthetic == "" {
			users = append(users, t)
		} else if it.Role == "assistant" {
			assistants = append(assistants, t)
		}
	}
	var read, changed orderedSet
	var commands []string
	var callOrder []string
	calls := map[string]string{}
	results := map[string]bool{}
	for _, it := range items {
		for _, p := range it.Parts {
			switch core.S(p["type"]) {
			case "tool_call":
				id := core.S(p["callId"])
				if _, ok := calls[id]; !ok {
					callOrder = append(callOrder, id)
				}
				calls[id] = core.S(p["rawName"])
				t := core.Obj(p["tool"])
				switch core.S(t["op"]) {
				case "read":
					read.add(core.S(t["path"]))
				case "write", "edit":
					changed.add(core.S(t["path"]))
				case "shell":
					commands = append(commands, core.S(t["command"]))
				case "patch":
					for _, m := range patchFileRe.FindAllStringSubmatch(core.S(t["patch"]), -1) {
						changed.add(m[1])
					}
				}
			case "tool_result":
				results[core.S(p["callId"])] = true
			}
		}
	}
	lines := []string{"# Session handoff checkpoint", "", "This summarizes the earlier part of a conversation carried over from another agent harness.", ""}
	if len(users) > 0 {
		lines = append(lines, "## Goal", clip(users[0], 2000), "")
	}
	if len(users) > 1 {
		lines = append(lines, "## Later requests")
		for _, u := range lastN(users[1:], 8) {
			lines = append(lines, "- "+clip(wsRe.ReplaceAllString(u, " "), 300))
		}
		lines = append(lines, "")
	}
	if len(changed.items) > 0 {
		lines = append(lines, "## Files changed")
		for _, f := range lastN(changed.items, 40) {
			lines = append(lines, "- "+f)
		}
		lines = append(lines, "")
	}
	if len(read.items) > 0 {
		lines = append(lines, "## Files read")
		for _, f := range lastN(read.items, 40) {
			lines = append(lines, "- "+f)
		}
		lines = append(lines, "")
	}
	if len(commands) > 0 {
		lines = append(lines, "## Recent commands")
		for _, c := range lastN(commands, 10) {
			lines = append(lines, "- `"+clip(strings.ReplaceAll(c, "\n", " "), 200)+"`")
		}
		lines = append(lines, "")
	}
	if len(assistants) > 0 {
		lines = append(lines, "## Last assistant state", clip(assistants[len(assistants)-1], 3000), "")
	}
	var open []string
	for _, id := range callOrder {
		if !results[id] {
			open = append(open, "- "+calls[id]+" ("+id+")")
		}
	}
	if len(open) > 0 {
		lines = append(lines, "## Open tool calls (never re-run automatically)")
		lines = append(lines, open...)
		lines = append(lines, "")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
