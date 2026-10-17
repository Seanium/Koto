package core

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
)

// NativeToolCall is a harness's own name and arguments for a tool call.
type NativeToolCall struct {
	Name string
	Args any
}

// ToolVocabulary maps a harness's native tool names/args to the canonical vocabulary and back.
// Same-harness delivery always uses rawName/args verbatim; the vocabulary is only used across harnesses.
type ToolVocabulary interface {
	ToCanonical(rawName string, args any) map[string]any
	// FromCanonical renders a canonical tool for this harness; ok=false = no faithful equivalent (textualize).
	FromCanonical(tool map[string]any) (NativeToolCall, bool)
}

// MaxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER (an open line range ends there).
const MaxSafeInteger = 9007199254740991

// RangeFromOffsetLimit: canonical range is inclusive [start, end], 1-based.
func RangeFromOffsetLimit(offset, limit *float64) []any {
	if offset == nil && limit == nil {
		return nil
	}
	start := 1.0
	if offset != nil {
		start = *offset
	}
	end := float64(MaxSafeInteger)
	if limit != nil {
		end = start + *limit - 1
	}
	return []any{start, end}
}

// OffsetLimitFromRange is the inverse (limit omitted for an open range).
func OffsetLimitFromRange(r []any) (offset, limit *float64) {
	if len(r) != 2 {
		return nil, nil
	}
	s, _ := Num(r[0])
	e, _ := Num(r[1])
	offset = &s
	if e < MaxSafeInteger {
		l := e - s + 1
		limit = &l
	}
	return
}

var cosmeticArgs = map[string]bool{"description": true, "timeout": true}

// CanonicalIsLossless: the canonical form kept every argument of the original call — the source
// harness renders it back to the same tool with each original key at the same value.
func CanonicalIsLossless(p Part, source ToolVocabulary) bool {
	tool := Obj(p["tool"])
	if S(tool["op"]) == "raw" {
		return false
	}
	orig := jsonObject(p["args"])
	if orig == nil {
		return true
	}
	if source == nil {
		return false
	}
	back, ok := source.FromCanonical(tool)
	if !ok || back.Name != S(p["rawName"]) {
		return false
	}
	got := jsonObject(back.Args)
	if got == nil {
		got = map[string]any{}
	}
	for k, v := range orig {
		if cosmeticArgs[k] {
			continue
		}
		g, has := got[k]
		if v == nil && !has {
			continue
		}
		if !SameJSON(v, g) {
			return false
		}
	}
	return true
}

func jsonObject(v any) map[string]any {
	if s, ok := v.(string); ok {
		var x any
		if json.Unmarshal([]byte(s), &x) != nil {
			return nil
		}
		v = x
	}
	m, _ := v.(map[string]any)
	return m
}

// TextualizeCall is the plain-text rendering of a tool call, for L2 or a target without an equivalent.
func TextualizeCall(p Part) string {
	t := Obj(p["tool"])
	raw := S(p["rawName"])
	var summary string
	switch S(t["op"]) {
	case "shell":
		summary = "$ " + S(t["command"])
	case "read":
		summary = "read " + S(t["path"])
		if r := Arr(t["range"]); len(r) == 2 {
			a, _ := Num(r[0])
			b, _ := Num(r[1])
			end := ""
			if b < MaxSafeInteger {
				end = fmtNum(b)
			}
			summary += ":" + fmtNum(a) + "-" + end
		}
	case "write":
		summary = "write " + S(t["path"])
	case "edit":
		n := len(Arr(t["edits"]))
		s := "s"
		if n == 1 {
			s = ""
		}
		summary = fmt.Sprintf("edit %s (%d change%s)", S(t["path"]), n, s)
	case "search":
		summary = "search " + JSString(S(t["query"]))
		if sc := S(t["scope"]); sc != "" {
			summary += " in " + sc
		}
	case "list":
		path := S(t["path"])
		if _, ok := t["path"]; !ok {
			path = "."
		}
		summary = "list " + path
		if pat := S(t["pattern"]); pat != "" {
			summary += " " + pat
		}
	case "web":
		if u := S(t["url"]); u != "" {
			summary = "fetch " + u
		} else {
			summary = "web search " + JSString(S(t["query"]))
		}
	case "patch":
		summary = "apply_patch"
	default:
		summary = raw
	}
	args, ok := p["args"].(string)
	if !ok {
		args = StringifyJS(p["args"])
	}
	return fmt.Sprintf("[tool call %s (%s)] %s\n%s", raw, S(p["callId"]), summary, args)
}

func fmtNum(f float64) string {
	s, err := ESNumber(f)
	if err != nil || math.IsNaN(f) {
		return "NaN"
	}
	return s
}

var (
	reAddFile    = regexp.MustCompile(`^\*\*\* Add File: (.+)$`)
	reUpdateFile = regexp.MustCompile(`^\*\*\* Update File: (.+)$`)
)

// PatchAsEdit turns a Codex patch into a plain edit/write when it is one: a single Update File
// (each @@ hunk one old→new edit) or a single Add File. Anything else has no single-call equivalent.
func PatchAsEdit(patch string) map[string]any {
	lines := strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n")
	begin, end := -1, -1
	for i, l := range lines {
		if begin < 0 && strings.TrimSpace(l) == "*** Begin Patch" {
			begin = i
		}
		if end < 0 && strings.TrimSpace(l) == "*** End Patch" {
			end = i
		}
	}
	if begin < 0 || end < begin {
		return nil
	}
	var body []string
	for _, l := range lines[begin+1 : end] {
		if l != "*** End of File" {
			body = append(body, l)
		}
	}
	heads := 0
	for i, l := range body {
		if strings.HasPrefix(l, "*** ") {
			heads++
			if i != 0 {
				return nil
			}
		}
	}
	if heads != 1 {
		return nil
	}
	head, rest := body[0], body[1:]
	if m := reAddFile.FindStringSubmatch(head); m != nil {
		var out []string
		for _, l := range rest {
			if !strings.HasPrefix(l, "+") {
				return nil
			}
			out = append(out, l[1:])
		}
		content := strings.Join(out, "\n")
		if len(rest) > 0 {
			content += "\n"
		}
		return map[string]any{"op": "write", "path": strings.TrimSpace(m[1]), "content": content}
	}
	m := reUpdateFile.FindStringSubmatch(head)
	if m == nil {
		return nil
	}
	var edits []any
	var cur *[2][]string
	flush := func() {
		if cur != nil && (len(cur[0]) > 0 || len(cur[1]) > 0) {
			edits = append(edits, map[string]any{"old": strings.Join(cur[0], "\n"), "new": strings.Join(cur[1], "\n")})
		}
	}
	for _, l := range rest {
		if strings.HasPrefix(l, "@@") {
			flush()
			cur = &[2][]string{}
			continue
		}
		if cur == nil {
			cur = &[2][]string{}
		}
		c := byte(' ')
		t := ""
		if len(l) > 0 {
			c = l[0]
			t = l[1:]
		}
		switch c {
		case ' ':
			cur[0] = append(cur[0], t)
			cur[1] = append(cur[1], t)
		case '-':
			cur[0] = append(cur[0], t)
		case '+':
			cur[1] = append(cur[1], t)
		default:
			return nil
		}
	}
	flush()
	if len(edits) == 0 {
		return nil
	}
	for _, e := range edits {
		em := e.(map[string]any)
		if em["old"] == "" || em["old"] == em["new"] {
			return nil
		}
	}
	return map[string]any{"op": "edit", "path": strings.TrimSpace(m[1]), "edits": edits}
}

// StringifyJS is JSON.stringify for a value built from JSON (sorted keys: Go maps keep no order).
func StringifyJS(v any) string {
	if v == nil {
		return "null"
	}
	s, err := Canonicalize(v)
	if err != nil {
		return "null"
	}
	return s
}
