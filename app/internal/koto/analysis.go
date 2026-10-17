package koto

// Analysis is an ephemeral, read-only comparison of user-selected native sessions from one
// harness. It does not merge their index identities, modify files, or create delivery records.
import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"koto/internal/core"
	"koto/internal/hub"
)

type AnalysisSession struct {
	Selector string   `json:"selector"`
	Title    string   `json:"title"`
	Turns    int      `json:"turns"`
	Nodes    []string `json:"nodes"`
}

// AnalysisLabel identifies a generated display line, never a line from native content.
// Text remains the stable comparison representation; clients localize only these labels.
type AnalysisLabel struct {
	Line  int    `json:"line"`
	Kind  string `json:"kind"`
	Value string `json:"value,omitempty"`
}
type AnalysisNode struct {
	ID        string          `json:"id"`
	Parent    string          `json:"parent"`
	Turn      int             `json:"turn"`
	Preview   string          `json:"preview"`
	Sessions  []int           `json:"sessions"`
	Text      string          `json:"text"`
	Labels    []AnalysisLabel `json:"labels,omitempty"`
	Hash      string          `json:"hash"`
	Truncated bool            `json:"truncated"`
}
type AnalysisResult struct {
	Harness  string            `json:"harness"`
	Sessions []AnalysisSession `json:"sessions"`
	Nodes    []AnalysisNode    `json:"nodes"`
}

func (a *API) Analyze(ctx context.Context, selectors []string) (*AnalysisResult, error) {
	if len(selectors) < 2 || len(selectors) > 32 {
		return nil, apiErr(400, "choose 2–32 sessions from one Agent")
	}
	out := &AnalysisResult{Sessions: []AnalysisSession{}, Nodes: []AnalysisNode{}}
	seen := map[string]bool{}
	nodes := map[string]int{}
	for _, sel := range selectors {
		h, _, ok := strings.Cut(sel, ":")
		if !ok || h == "th" || seen[sel] {
			return nil, apiErr(400, "select distinct native sessions")
		}
		if out.Harness != "" && out.Harness != h {
			return nil, apiErr(400, "analysis requires sessions from the same Agent")
		}
		seen[sel], out.Harness = true, h
		events, title, err := a.analysisEvents(ctx, sel)
		if err != nil {
			return nil, err
		}
		row := AnalysisSession{Selector: sel, Title: title, Nodes: []string{}}
		var turns [][]*core.Event
		var contextEvents []*core.Event
		for _, e := range events {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if e.Kind == "context" {
				contextEvents = append(contextEvents, e)
				continue
			}
			if e.Kind != "message" && e.Kind != "compaction" && e.Kind != "tool_result" {
				continue
			}
			if e.Actor == "user" && hasUserText(e) || len(turns) == 0 {
				turns = append(turns, nil)
			}
			turns[len(turns)-1] = append(turns[len(turns)-1], e)
		}
		// Context is separately selectable and does not cause unrelated environment metadata to
		// split an otherwise identical conversation. It is never inserted into the copy projection.
		all := append([][]*core.Event{contextEvents}, turns...)
		parent := ""
		for n, events := range all {
			if len(events) == 0 {
				continue
			}
			canonical, display, preview := analysisContentDisplay(events)
			id := core.SHA256Hex(parent + "\x00" + canonical)
			if n == 0 {
				id = core.SHA256Hex("context\x00" + canonical)
				preview = "系统与环境上下文"
			}
			idx, exists := nodes[id]
			if !exists {
				idx = len(out.Nodes)
				nodes[id] = idx
				display, truncated := limitAnalysisDisplay(display, 24000)
				out.Nodes = append(out.Nodes, AnalysisNode{ID: id, Parent: parent, Hash: core.SHA256Hex(canonical), Turn: n, Preview: preview, Text: display.text, Labels: display.labels, Truncated: truncated, Sessions: []int{}})
			}
			out.Nodes[idx].Sessions = append(out.Nodes[idx].Sessions, len(out.Sessions))
			row.Nodes = append(row.Nodes, id)
			if n > 0 {
				parent = id
				row.Turns++
			}
		}
		out.Sessions = append(out.Sessions, row)
	}
	return out, nil
}

// Decode actual native contents, following only same-Agent ancestry needed to reconstruct a fork.
func (a *API) analysisEvents(ctx context.Context, selector string) ([]*core.Event, string, error) {
	harness, session, _ := strings.Cut(selector, ":")
	h := hub.New()
	defer h.Close()
	seen := map[string]bool{}
	var read func(string) error
	read = func(id string) error {
		if seen[id] {
			return fmt.Errorf("cyclic native ancestry")
		}
		seen[id] = true
		r, ok := a.App.Store.Session(harness, id)
		if !ok {
			return apiErr(404, "session not in index: "+harness+":"+id)
		}
		if r.Parent != nil && r.Parent.Harness == harness {
			if err := read(r.Parent.Session); err != nil {
				return err
			}
		}
		c := a.App.Registry.Get(r.Connector)
		if c == nil || c.Source == nil {
			return apiErr(400, "source unavailable")
		}
		return c.Source.Tail(ctx, core.NativeRef{Harness: harness, Connector: c.ID, Session: id, Path: r.Path, Size: r.Size, MtimeMs: r.Mtime, Meta: r.Meta}, nil, func(batch core.SourceBatch) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			h.Ingest(batch, hub.ConnectorInfo{ID: c.ID, Harness: harness, Version: c.Version})
			return nil
		})
	}
	if err := read(session); err != nil {
		return nil, "", err
	}
	b := h.Binding(harness, session)
	if b == nil {
		return nil, "", apiErr(404, "session has no readable content")
	}
	r, _ := a.App.Store.Session(harness, session)
	title := r.Title
	if title == "" {
		title = r.FirstPrompt
	}
	if title == "" {
		title = selector
	}
	return h.Path(b.Leaf), title, nil
}
func hasUserText(e *core.Event) bool {
	for _, p := range e.Parts {
		if core.S(p["type"]) == "text" || core.S(p["type"]) == "image" || core.S(p["type"]) == "file" || core.S(p["type"]) == "opaque" {
			return true
		}
	}
	return false
}
func analysisContent(events []*core.Event) (canonical, text, preview string) {
	canonical, display, preview := analysisContentDisplay(events)
	return canonical, display.text, preview
}

type analysisDisplay struct {
	text   string
	labels []AnalysisLabel
}

func analysisJoin(parts []analysisDisplay, separator string) analysisDisplay {
	var out analysisDisplay
	var text strings.Builder
	line := 0
	for i, part := range parts {
		if i > 0 {
			text.WriteString(separator)
			line += strings.Count(separator, "\n")
		}
		for _, label := range part.labels {
			label.Line += line
			out.labels = append(out.labels, label)
		}
		text.WriteString(part.text)
		line += strings.Count(part.text, "\n")
	}
	out.text = text.String()
	return out
}

func analysisLabel(kind, text, value string) analysisDisplay {
	// A native value may itself contain newlines. Keep it literal rather than
	// allowing a localized label to consume or inject comparison rows.
	if strings.ContainsAny(value, "\r\n") {
		return analysisDisplay{text: text}
	}
	return analysisDisplay{text: text, labels: []AnalysisLabel{{Line: 0, Kind: kind, Value: value}}}
}

func limitAnalysisDisplay(display analysisDisplay, limit int) (analysisDisplay, bool) {
	runes := []rune(display.text)
	if len(runes) <= limit {
		return display, false
	}
	prefix := string(runes[:limit])
	original, shown := strings.Split(display.text, "\n"), strings.Split(prefix, "\n")
	out := analysisDisplay{text: prefix + "\n…（内容较长，请在会话中查看全文）"}
	for _, label := range display.labels {
		if label.Line < len(shown) && shown[label.Line] == original[label.Line] {
			out.labels = append(out.labels, label)
		}
	}
	out.labels = append(out.labels, AnalysisLabel{Line: len(shown), Kind: "truncated"})
	return out, true
}

func analysisContentDisplay(events []*core.Event) (canonical string, display analysisDisplay, preview string) {
	var content []any
	var lines []analysisDisplay
	calls := map[string]string{}
	for _, e := range events {
		var parts []core.Part
		for _, p := range e.Parts {
			cp := core.Part{}
			for k, v := range p {
				if k != "signature" && k != "sealed" {
					cp[k] = v
				}
			}
			if id := core.S(cp["callId"]); id != "" {
				if calls[id] == "" {
					calls[id] = fmt.Sprintf("call%d", len(calls)+1)
				}
				cp["callId"] = calls[id]
			}
			if t, ok := cp["text"].(string); ok {
				cp["text"] = strings.ReplaceAll(t, "\r\n", "\n")
			}
			parts = append(parts, cp)
			if core.S(cp["type"]) == "text" && preview == "" {
				preview = core.S(cp["text"])
				r := []rune(preview)
				if len(r) > 100 {
					preview = string(r[:100])
				}
			}
			if t := core.S(cp["text"]); t != "" {
				lines = append(lines, analysisJoin([]analysisDisplay{analysisRole(e.Actor), {text: t}}, "\n"))
			} else {
				lines = append(lines, analysisJoin([]analysisDisplay{analysisRole(e.Actor), analysisPart(cp)}, "\n"))
			}
		}
		content = append(content, map[string]any{"role": e.Actor, "kind": e.Kind, "parts": parts})
	}
	return core.ToJSON(content), analysisJoin(lines, "\n\n"), preview
}

func analysisRole(role string) analysisDisplay {
	switch role {
	case "user":
		return analysisLabel("user", "提问", "")
	case "assistant":
		return analysisLabel("assistant", "回复", "")
	case "tool":
		return analysisLabel("tool", "工具", "")
	case "system", "harness":
		return analysisLabel("context", "上下文", "")
	}
	return analysisDisplay{text: role}
}
func analysisPart(p core.Part) analysisDisplay {
	pretty := func(v any) string {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return core.ToJSON(v)
		}
		return string(b)
	}
	switch core.S(p["type"]) {
	case "text":
		return analysisDisplay{text: core.S(p["text"])}
	case "reasoning":
		return analysisJoin([]analysisDisplay{analysisLabel("reasoning", "思考", ""), {text: core.S(p["text"])}}, "\n")
	case "tool_call":
		return analysisJoin([]analysisDisplay{analysisLabel("call", "调用 "+core.S(p["rawName"]), core.S(p["rawName"])), {text: pretty(p["args"])}}, "\n")
	case "tool_result":
		var text []analysisDisplay
		for _, part := range core.Parts(p["parts"]) {
			text = append(text, analysisPart(part))
		}
		return analysisJoin([]analysisDisplay{analysisLabel("result", "结果（"+core.S(p["outcome"])+"）", core.S(p["outcome"])), analysisJoin(text, "\n")}, "\n")
	case "image", "file":
		value := core.S(p["name"]) + " " + core.S(p["mediaType"])
		return analysisJoin([]analysisDisplay{analysisLabel("attachment", "附件 "+value, value), {text: core.S(core.Obj(p["blob"])["sha256"])}}, "\n")
	}
	return analysisDisplay{text: pretty(p)}
}
