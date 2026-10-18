// Package planner turns a version's path into what a sink renders, given the sink's capabilities
// and the level (design §7). Pure and deterministic: the same inputs give the same items and losses.
//
// Cross-provider replay follows pi-ai's transform-messages: readable reasoning becomes text,
// encrypted/empty reasoning is dropped, signatures survive only over the same wire API, tool-call
// ids are normalized for the target and results rewritten to match, errored/aborted assistant
// turns are skipped, and tool calls without results get a synthetic error result. Tools never re-run.
package planner

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"koto/internal/core"
)

// Item is one message the sink writes.
type Item struct {
	// Key mints deterministic native ids: the event id, or syn:<hash> for synthetic items.
	Key       string
	Event     *core.Event
	Role      string // user | assistant | tool
	Parts     []core.Part
	Verbatim  bool
	Synthetic string
	// Merged: earlier events folded into this item (consecutive assistant records of one reply).
	Merged []core.EventID
}

// Hints gathered from meta events along the path.
type Hints struct {
	Cwd       string
	GitBranch string
	Model     core.JsonObject
}

// Projection is what a sink plans from.
type Projection struct {
	Level    string
	Items    []Item
	Losses   []core.Loss
	Notes    []string
	Deferred []core.EventID
	// Calls: every tool call on the path by call id.
	Calls map[string]core.Part
	Hints Hints
}

// Summarizer builds the L3 checkpoint from the summarized items (deterministic for an input).
type Summarizer func(items []Item) string

// Options for Project.
type Options struct {
	Sink             string
	SinkHarness      string
	Capabilities     core.Capabilities
	Vocabulary       core.ToolVocabulary
	SourceVocabulary func(harness string) core.ToolVocabulary
	Level            string
	MaxTokens        int
	KeepTurns        int
	// DeferIncomplete: nil = when the source looks live (written within 10 minutes).
	DeferIncomplete   *bool
	FullHistory       bool
	IncludeSidechains bool
	Summarizer        Summarizer
	Now               func() time.Time
	ReadBlob          func(string) ([]byte, bool)
}

const defaultKeepTurns = 6

// DefaultLevel: L0 within a harness, L1 when the target takes tools, else L2.
func DefaultLevel(caps core.Capabilities, sameHarness bool) string {
	if sameHarness {
		return "L0"
	}
	if caps.Write == nil || caps.Write.Tools == "text" {
		return "L2"
	}
	return "L1"
}

func textOf(parts []core.Part) string {
	var b []string
	for _, p := range parts {
		if core.S(p["type"]) == "text" {
			b = append(b, core.S(p["text"]))
		}
	}
	return strings.Join(b, "\n")
}

func hasType(parts []core.Part, t string) bool {
	for _, p := range parts {
		if core.S(p["type"]) == t {
			return true
		}
	}
	return false
}

// Project builds the projection of path for a sink.
func Project(path []*core.Event, o Options) *Projection {
	caps := o.Capabilities
	level := o.Level
	if level == "" {
		level = DefaultLevel(caps, false)
	}
	pr := &Projection{Level: level, Calls: map[string]core.Part{}, Losses: []core.Loss{}, Notes: []string{}, Deferred: []core.EventID{}}
	loss := func(ev core.EventID, field, kind, note string) {
		pr.Losses = append(pr.Losses, core.Loss{Sink: o.Sink, Event: ev, Field: field, Kind: kind, Note: note})
	}
	sameHarness := func(e *core.Event) bool { return e.Provenance.Source.Harness == o.SinkHarness }

	// 1. compaction: a replaced range collapses into its summary (across harnesses only)
	events := path
	if !o.FullHistory {
		idx := map[string]int{}
		for i, e := range events {
			idx[e.ID] = i
		}
		cut := [3]int{-1, -1, -1}
		for i, e := range events {
			if e.Kind == "compaction" && e.Replaces != nil && !(level == "L0" && sameHarness(e)) {
				f, okf := idx[e.Replaces.From]
				t, okt := idx[e.Replaces.To]
				if okf && okt && f <= t && t < i {
					cut = [3]int{f, t, i}
				}
			}
		}
		if cut[2] >= 0 {
			loss(events[cut[2]].ID, "history", "approximated", fmt.Sprintf("%d events before compaction replaced by its summary", cut[1]-cut[0]+1))
			var n []*core.Event
			n = append(n, events[:cut[0]]...)
			n = append(n, events[cut[2]])
			n = append(n, events[cut[1]+1:cut[2]]...)
			n = append(n, events[cut[2]+1:]...)
			events = n
		}
	}

	// 2. events → items (the latest context edit of a target on the path wins)
	edits := map[core.EventID][]core.Part{}
	for _, e := range path {
		if e.Kind == "x-context-edit" && e.Replaces != nil {
			edits[e.Replaces.To] = e.Parts
		}
		for _, p := range e.Parts {
			if core.S(p["type"]) == "tool_call" {
				pr.Calls[core.S(p["callId"])] = p
			}
		}
	}
	var items []Item
	ctx := 0
	for _, e0 := range events {
		edit, edited := edits[e0.ID]
		if edited && len(edit) == 0 {
			continue
		}
		e := e0
		if edited {
			c := *e0
			c.Parts = edit
			e = &c
		}
		attrs := e.Attrs
		if e.Kind == "meta" {
			if s, ok := attrs["cwd"].(string); ok {
				pr.Hints.Cwd = s
			}
			if s, ok := attrs["gitBranch"].(string); ok {
				pr.Hints.GitBranch = s
			}
			continue
		}
		if s, ok := attrs["cwd"].(string); ok {
			pr.Hints.Cwd = s
		}
		if s, ok := attrs["gitBranch"].(string); ok {
			pr.Hints.GitBranch = s
		}
		if m, ok := attrs["model"].(map[string]any); ok {
			pr.Hints.Model = m
		}
		if e.Kind == "context" {
			ctx++
			continue // system prompt / injected context never migrates
		}
		if e.Kind == "lineage" || strings.HasPrefix(e.Kind, "x-") {
			continue
		}
		if b, _ := attrs["sidechain"].(bool); b && !o.IncludeSidechains {
			loss(e.ID, "sidechain", "dropped", "subagent transcript")
			continue
		}
		verbatim := level == "L0" && sameHarness(e) && e.Attrs["filteredContext"] != true && !edited
		parts, assetsChanged := normalizeAssets(e.Parts, e.Actor, verbatim, o, func(field, kind, note string) { loss(e.ID, field, kind, note) })
		verbatim = verbatim && !assetsChanged
		if e.Kind == "compaction" {
			text := textOf(e.Parts)
			if verbatim {
				items = append(items, Item{Key: e.ID, Event: e, Role: "user", Parts: e.Parts, Verbatim: true})
			} else if text != "" {
				items = append(items, Item{Key: e.ID, Event: e, Role: "user", Parts: []core.Part{{"type": "text", "text": "[Summary of the earlier conversation]\n" + text}}})
			}
			continue
		}
		role := map[string]string{"user": "user", "assistant": "assistant", "tool": "tool"}[e.Actor]
		if role == "" {
			continue
		}
		items = append(items, Item{Key: e.ID, Event: e, Role: role, Parts: parts, Verbatim: verbatim})
	}
	if ctx > 0 {
		pr.Notes = append(pr.Notes, fmt.Sprintf("%d harness context events (system prompt, environment, instructions) are not migrated", ctx))
	}
	items = applyReplayRules(items, o, level, loss, &pr.Deferred)
	if len(pr.Deferred) > 0 {
		pr.Notes = append(pr.Notes, fmt.Sprintf("%d trailing events deferred: the last turn is still waiting for tool results", len(pr.Deferred)))
	}

	// 3. L3 / budget: older turns degrade to a checkpoint summary
	keep := o.KeepTurns
	if keep == 0 {
		keep = defaultKeepTurns
	}
	if level == "L3" {
		items = summarizeHead(items, keepTailIndex(items, keep), o, loss)
	} else if o.MaxTokens > 0 && EstimateTokens(items) > o.MaxTokens {
		cutAt := 0
		for i := range items {
			if items[i].Role != "user" {
				continue
			}
			if float64(EstimateTokens(items[i:])) <= float64(o.MaxTokens)*0.8 {
				cutAt = i
				break
			}
		}
		if cutAt == 0 {
			cutAt = keepTailIndex(items, 1)
		}
		items = summarizeHead(items, cutAt, o, loss)
		pr.Notes = append(pr.Notes, fmt.Sprintf("token budget %d: older turns summarized", o.MaxTokens))
	}

	pr.Items = CoalesceAssistant(items)
	return pr
}

// CoalesceAssistant folds consecutive assistant items (one reply stored as several records) into
// one; the folded item keeps the last event's key, so re-reading the written record is a no-op.
func CoalesceAssistant(items []Item) []Item {
	var out []Item
	for _, it := range items {
		if n := len(out); n > 0 {
			prev := out[n-1]
			if prev.Role == "assistant" && it.Role == "assistant" && !prev.Verbatim && !it.Verbatim && prev.Synthetic == "" && it.Synthetic == "" {
				m := append(append([]core.EventID{}, prev.Merged...), prev.Event.ID)
				parts := append(append([]core.Part{}, prev.Parts...), it.Parts...)
				it.Parts, it.Merged = parts, m
				out[n-1] = it
				continue
			}
		}
		out = append(out, it)
	}
	return out
}

func apiOf(e *core.Event) string {
	m, _ := e.Attrs["model"].(map[string]any)
	return core.S(m["api"])
}

func copyPart(p core.Part) core.Part {
	c := core.Part{}
	for k, v := range p {
		c[k] = v
	}
	return c
}

func applyReplayRules(items []Item, o Options, level string, loss func(ev core.EventID, field, kind, note string), deferred *[]core.EventID) []Item {
	// One native record can contain several results, some structurally
	// representable and some textualized. Give each result its own replay item
	// so a writer cannot silently ignore text alongside a native result.
	expanded := make([]Item, 0, len(items))
	for _, it := range items {
		if it.Role != "tool" || it.Verbatim || len(it.Parts) < 2 {
			expanded = append(expanded, it)
			continue
		}
		for i, part := range it.Parts {
			x := it
			x.Key = fmt.Sprintf("%s:result:%d", it.Key, i)
			x.Parts = []core.Part{part}
			expanded = append(expanded, x)
		}
	}
	items = expanded
	w := o.Capabilities.Write
	textTools := w == nil || w.Tools == "text" || level == "L2" || level == "L3"
	var out []Item
	droppedCalls := map[string]bool{}
	structural := map[string]bool{}
	idMap := map[string]string{}
	var pat *regexp.Regexp
	if w != nil && w.ToolCallID != nil {
		pat = regexp.MustCompile(w.ToolCallID.Pattern)
	}
	normID := func(id string) string {
		if pat == nil || (pat.MatchString(id) && len([]rune(id)) <= w.ToolCallID.MaxLength) {
			return id
		}
		n, ok := idMap[id]
		if !ok {
			n = "call_" + core.SHA256Hex(id)[:24]
			idMap[id] = n
		}
		return n
	}
	openCalls := map[string]bool{}
	var held []Item
	flushHeld := func() {
		out = append(out, held...)
		held = nil
		openCalls = map[string]bool{}
	}
	for _, it := range items {
		if it.Verbatim {
			out = append(out, it)
			for _, p := range it.Parts {
				if core.S(p["type"]) == "tool_call" {
					structural[core.S(p["callId"])] = true
				}
			}
			continue
		}
		stop := core.S(it.Event.Attrs["stopReason"])
		if it.Role == "assistant" && (stop == "error" || stop == "aborted") {
			for _, p := range it.Parts {
				if core.S(p["type"]) == "tool_call" {
					droppedCalls[core.S(p["callId"])] = true
				}
			}
			loss(it.Event.ID, "message", "dropped", "assistant turn ended with "+stop)
			continue
		}
		var parts []core.Part
		for _, p := range it.Parts {
			switch core.S(p["type"]) {
			case "reasoning":
				if r := reasoningFor(p, it.Event, o, loss); r != nil {
					parts = append(parts, r)
				}
			case "image":
				if w != nil && !w.Images {
					parts = append(parts, core.Part{"type": "text", "text": "[image omitted: " + core.S(p["mediaType"]) + "]"})
					loss(it.Event.ID, "image", "dropped", "")
				} else {
					parts = append(parts, p)
				}
			case "tool_call":
				same := it.Event.Provenance.Source.Harness == o.SinkHarness
				rendered := false
				if !same && o.Vocabulary != nil {
					_, rendered = o.Vocabulary.FromCanonical(core.Obj(p["tool"]))
				}
				ok := false
				if !textTools {
					if same {
						ok = true
					} else if rendered {
						var sv core.ToolVocabulary
						if o.SourceVocabulary != nil {
							sv = o.SourceVocabulary(it.Event.Provenance.Source.Harness)
						}
						ok = core.CanonicalIsLossless(p, sv)
					} else {
						ok = w != nil && w.Tools == "native"
					}
				}
				structural[core.S(p["callId"])] = ok
				if ok {
					c := copyPart(p)
					c["callId"] = normID(core.S(p["callId"]))
					parts = append(parts, c)
				} else {
					parts = append(parts, core.Part{"type": "text", "text": core.TextualizeCall(p)})
					loss(it.Event.ID, "tool_call:"+core.S(p["rawName"]), "textualized", "")
				}
			case "tool_result":
				id := core.S(p["callId"])
				if droppedCalls[id] {
					loss(it.Event.ID, "tool_result", "dropped", "result of a skipped assistant turn")
					break
				}
				if structural[id] {
					c := copyPart(p)
					c["callId"] = normID(id)
					parts = append(parts, c)
				} else {
					parts = append(parts, core.Part{"type": "text", "text": TextualizeResult(p)})
					// A textual tool description must not discard its image/file payloads.
					for _, asset := range core.Parts(p["parts"]) {
						if asset["type"] == "image" || asset["type"] == "file" {
							parts = append(parts, asset)
						}
					}
					if _, known := structural[id]; known {
						loss(it.Event.ID, "tool_result", "textualized", "")
					} else {
						loss(it.Event.ID, "tool_result", "textualized", "orphan result (no matching call)")
					}
				}
			case "opaque":
				parts = append(parts, core.Part{"type": "text", "text": "[未复制的内容：" + core.S(p["semantic"]) + "]"})
				loss(it.Event.ID, "opaque:"+core.S(p["semantic"]), "dropped", core.S(p["reason"]))
			default:
				parts = append(parts, p)
			}
		}
		if len(parts) == 0 {
			continue
		}
		// A textualized tool result is part of the assistant's work, not a new
		// human prompt. Keep it after pending structural results, then apply the
		// target's material rules for its effective role.
		role := it.Role
		if it.Role == "tool" && !hasType(parts, "tool_result") {
			role = "assistant"
			parts, _ = normalizeAssets(parts, role, false, o, func(field, kind, note string) {
				loss(it.Event.ID, field, kind, note)
			})
		}
		if role == "assistant" && it.Role == "tool" && len(openCalls) > 0 {
			c := it
			c.Role, c.Parts = role, parts
			held = append(held, c)
			continue
		}
		if it.Role != "tool" {
			flushHeld()
		}
		c := it
		c.Role, c.Parts = role, parts
		out = append(out, c)
		for _, p := range parts {
			switch core.S(p["type"]) {
			case "tool_call":
				openCalls[core.S(p["callId"])] = true
			case "tool_result":
				delete(openCalls, core.S(p["callId"]))
			}
		}
		if len(openCalls) == 0 {
			flushHeld()
		}
	}
	flushHeld()

	// c) synthetic error results for structural calls that never got one; a trailing turn still
	// waiting for results is deferred instead while the source looks live.
	answered := map[string]bool{}
	for _, it := range out {
		for _, p := range it.Parts {
			if core.S(p["type"]) == "tool_result" {
				answered[core.S(p["callId"])] = true
			}
		}
	}
	lastTime := 0.0
	for _, it := range out {
		lastTime = math.Max(lastTime, it.Event.TimeMs())
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	live := lastTime > 0 && float64(now().UnixMilli())-lastTime < 10*60_000
	if o.DeferIncomplete != nil {
		live = *o.DeferIncomplete
	}
	var final []Item
	for i, it := range out {
		var pending []core.Part
		if it.Role == "assistant" && !it.Verbatim {
			for _, p := range it.Parts {
				if core.S(p["type"]) == "tool_call" && !answered[core.S(p["callId"])] {
					pending = append(pending, p)
				}
			}
		}
		moved := false
		for _, x := range out[i+1:] {
			if x.Role == "user" || x.Role == "assistant" {
				moved = true
				break
			}
		}
		if len(pending) > 0 && !moved && live {
			for _, x := range out[i:] {
				*deferred = append(*deferred, x.Event.ID)
			}
			break
		}
		final = append(final, it)
		for _, c := range pending {
			id := core.S(c["callId"])
			answered[id] = true
			loss(it.Event.ID, "tool_result:"+core.S(c["rawName"]), "approximated", "synthetic error result for a call without result")
			final = append(final, Item{
				Key: "syn:" + core.SHA256Hex(it.Event.ID + "\x00result\x00" + id)[:32], Event: it.Event, Role: "tool",
				Parts:     []core.Part{{"type": "tool_result", "callId": id, "outcome": "error", "parts": []any{map[string]any{"type": "text", "text": "No result provided"}}}},
				Synthetic: "missing tool result",
			})
		}
	}
	// d) a trailing question the Agent has not answered yet, while the source looks live: wait for the reply.
	if n := len(final); live && len(*deferred) == 0 && n > 0 {
		t := final[n-1]
		if t.Role == "user" && t.Synthetic == "" && hasType(t.Parts, "text") {
			*deferred = append(*deferred, t.Event.ID)
			final = final[:n-1]
		}
	}
	return final
}

func reasoningFor(p core.Part, e *core.Event, o Options, loss func(ev core.EventID, field, kind, note string)) core.Part {
	mode, same := "none", ""
	if w := o.Capabilities.Write; w != nil {
		mode, same = w.Reasoning, w.SameAPI
	}
	if mode == "passthrough" {
		return p
	}
	sig := core.S(p["signature"])
	sealed, hasSealed := p["sealed"].(map[string]any)
	if same != "" && (sig != "" || hasSealed) && apiOf(e) == same && (!hasSealed || core.S(sealed["by"]) == same) {
		return p
	}
	if t := core.S(p["text"]); strings.TrimSpace(t) != "" {
		if mode == "plain" && sig == "" && !hasSealed {
			return p
		}
		loss(e.ID, "reasoning", "textualized", "reasoning from another wire API replayed as text")
		return core.Part{"type": "text", "text": t}
	}
	if hasSealed {
		loss(e.ID, "reasoning", "dropped", "encrypted by "+core.S(sealed["by"]))
	} else {
		loss(e.ID, "reasoning", "dropped", "empty reasoning")
	}
	return nil
}

// TextualizeResult renders a tool result as text.
func TextualizeResult(p core.Part) string {
	var b []string
	for _, x := range core.Arr(p["parts"]) {
		xm := core.Obj(x)
		if core.S(xm["type"]) == "text" {
			b = append(b, core.S(xm["text"]))
		} else {
			b = append(b, "["+core.S(xm["type"])+"]")
		}
	}
	oc := ""
	if o := core.S(p["outcome"]); o != "success" {
		oc = " (" + o + ")"
	}
	return "[tool result " + core.S(p["callId"]) + oc + "]\n" + strings.Join(b, "\n")
}

// EstimateTokens is a rough count (characters / 4).
func EstimateTokens(items []Item) int {
	chars := 0
	var walk func(ps []any)
	walk = func(ps []any) {
		for _, x := range ps {
			p := core.Obj(x)
			switch core.S(p["type"]) {
			case "text":
				chars += len([]rune(core.S(p["text"])))
			case "reasoning":
				chars += len([]rune(core.S(p["text"])))
			case "tool_call":
				chars += len(core.StringifyJS(p["args"]))
			case "tool_result":
				walk(core.Arr(p["parts"]))
			default:
				chars += 1000
			}
		}
	}
	for _, it := range items {
		walk(core.PartsJSON(it.Parts))
	}
	return int(math.Ceil(float64(chars) / 4))
}

func keepTailIndex(items []Item, turns int) int {
	seen := 0
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Role == "user" && hasType(items[i].Parts, "text") && items[i].Synthetic == "" {
			seen++
			if seen >= turns {
				return i
			}
		}
	}
	return 0
}

func summarizeHead(items []Item, cutAt int, o Options, loss func(ev core.EventID, field, kind, note string)) []Item {
	if cutAt <= 0 {
		return items
	}
	head, tail := items[:cutAt], items[cutAt:]
	anchor := head[len(head)-1].Event
	sum := o.Summarizer
	if sum == nil {
		sum = HandoffSummary
	}
	summary := sum(head)
	for _, it := range head {
		loss(it.Event.ID, "message", "approximated", "summarized into handoff checkpoint")
	}
	return append([]Item{{Key: "syn:" + core.SHA256Hex(anchor.ID + "\x00summary\x00" + summary)[:32], Event: anchor, Role: "user", Parts: []core.Part{{"type": "text", "text": summary}}, Synthetic: "handoff summary"}}, tail...)
}
