package core

import (
	"regexp"
	"strings"
)

// EditedPaths are the files a turn's tool calls wrote: edit and write tools, and patches. Shell
// commands are not looked into, so the answer can be incomplete; callers say so.
func EditedPaths(parts []Part) []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range parts {
		if S(p["type"]) != "tool_call" {
			continue
		}
		t := Obj(p["tool"])
		switch S(t["op"]) {
		case "write", "edit":
			add(S(t["path"]))
		case "patch":
			for _, f := range PatchPaths(S(t["patch"])) {
				add(f)
			}
		}
	}
	return out
}

var (
	rePatchFile = regexp.MustCompile(`^\*\*\* (?:Update|Add|Delete) File: (.+)$`)
	rePatchMove = regexp.MustCompile(`^\*\*\* Move to: (.+)$`)
	reDiffPlus  = regexp.MustCompile(`^\+\+\+ (?:b/)?(.+?)(?:\t.*)?$`)
)

// PatchPaths are the files a Codex patch (`*** Update File: x`) or a unified diff (`+++ b/x`) names.
func PatchPaths(patch string) []string {
	var out []string
	for _, line := range strings.Split(patch, "\n") {
		if m := rePatchFile.FindStringSubmatch(line); m != nil {
			out = append(out, strings.TrimSpace(m[1]))
			continue
		}
		if m := rePatchMove.FindStringSubmatch(line); m != nil {
			out = append(out, strings.TrimSpace(m[1]))
			continue
		}
		if m := reDiffPlus.FindStringSubmatch(line); m != nil && m[1] != "/dev/null" {
			out = append(out, strings.TrimSpace(m[1]))
		}
	}
	return out
}
