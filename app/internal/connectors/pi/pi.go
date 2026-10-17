// Package pi is the pi (earendil-works coding-agent) connector. Storage root:
// $PI_CODING_AGENT_DIR (default ~/.pi/agent); sessions are append-only JSONL trees (format v3).
package pi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/planner"
)

const Version = "pi-connector@0.1.0"

// Vocabulary maps pi tools to the canonical vocabulary and back.
type Vocabulary struct{}

func (Vocabulary) ToCanonical(name string, args any) map[string]any {
	a := core.Obj(args)
	path := core.S(a["path"])
	switch name {
	case "read":
		var off, lim *float64
		if n, ok := core.Num(a["offset"]); ok {
			off = &n
		}
		if n, ok := core.Num(a["limit"]); ok {
			lim = &n
		}
		t := map[string]any{"op": "read", "path": path}
		if r := core.RangeFromOffsetLimit(off, lim); r != nil {
			t["range"] = r
		}
		return t
	case "write":
		t := map[string]any{"op": "write", "path": path}
		if c, ok := a["content"].(string); ok {
			t["content"] = c
		}
		return t
	case "edit":
		var edits []any
		if arr, ok := a["edits"].([]any); ok {
			edits = []any{}
			for _, e := range arr {
				em := core.Obj(e)
				edits = append(edits, map[string]any{"old": core.S(em["oldText"]), "new": core.S(em["newText"])})
			}
		} else {
			edits = []any{map[string]any{"old": core.S(a["oldText"]), "new": core.S(a["newText"])}}
		}
		return map[string]any{"op": "edit", "path": path, "edits": edits}
	case "bash":
		return map[string]any{"op": "shell", "command": core.S(a["command"])}
	case "grep":
		t := map[string]any{"op": "search", "query": core.S(a["pattern"])}
		if p := core.S(a["path"]); p != "" {
			t["scope"] = p
		}
		return t
	case "find":
		t := map[string]any{"op": "list"}
		if p := core.S(a["path"]); p != "" {
			t["path"] = p
		}
		if p := core.S(a["pattern"]); p != "" {
			t["pattern"] = p
		}
		return t
	case "ls":
		t := map[string]any{"op": "list"}
		if p := core.S(a["path"]); p != "" {
			t["path"] = p
		}
		return t
	}
	return map[string]any{"op": "raw"}
}

func (v Vocabulary) FromCanonical(t map[string]any) (core.NativeToolCall, bool) {
	switch core.S(t["op"]) {
	case "read":
		a := map[string]any{"path": core.S(t["path"])}
		if off, lim := core.OffsetLimitFromRange(core.Arr(t["range"])); off != nil {
			a["offset"] = *off
			if lim != nil {
				a["limit"] = *lim
			}
		}
		return core.NativeToolCall{Name: "read", Args: a}, true
	case "write":
		c, ok := t["content"].(string)
		if !ok {
			return core.NativeToolCall{}, false
		}
		return core.NativeToolCall{Name: "write", Args: map[string]any{"path": core.S(t["path"]), "content": c}}, true
	case "edit":
		edits := []any{}
		for _, e := range core.Arr(t["edits"]) {
			em := core.Obj(e)
			edits = append(edits, map[string]any{"oldText": core.S(em["old"]), "newText": core.S(em["new"])})
		}
		return core.NativeToolCall{Name: "edit", Args: map[string]any{"path": core.S(t["path"]), "edits": edits}}, true
	case "shell":
		return core.NativeToolCall{Name: "bash", Args: map[string]any{"command": core.S(t["command"])}}, true
	case "search":
		a := map[string]any{"pattern": core.S(t["query"])}
		if s := core.S(t["scope"]); s != "" {
			a["path"] = s
		}
		return core.NativeToolCall{Name: "grep", Args: a}, true
	case "list":
		if p := core.S(t["pattern"]); p != "" {
			a := map[string]any{"pattern": p}
			if s := core.S(t["path"]); s != "" {
				a["path"] = s
			}
			return core.NativeToolCall{Name: "find", Args: a}, true
		}
		a := map[string]any{}
		if s := core.S(t["path"]); s != "" {
			a["path"] = s
		}
		return core.NativeToolCall{Name: "ls", Args: a}, true
	case "patch":
		if e := core.PatchAsEdit(core.S(t["patch"])); e != nil {
			return v.FromCanonical(e)
		}
	}
	return core.NativeToolCall{}, false
}

var vocab = Vocabulary{}

// Capabilities: pi-ai re-applies cross-provider replay rules at request time, so signed or
// encrypted reasoning is stored as is and only replayed to the same model.
var Capabilities = core.Capabilities{
	Read:         core.ReadCaps{Images: true, Reasoning: "sealed", Compaction: true},
	Write:        &core.WriteCaps{Mode: "native-file", Images: true, Reasoning: "passthrough", Tools: "canonical", Tree: true},
	WriteProfile: "pi-write@1", FormatVersions: []string{"3"},
}

var leadSep = regexp.MustCompile(`^[/\\]`)
var seps = regexp.MustCompile(`[/\\:]`)

// CwdDir: /Users/a/b → --Users-a-b-- (pi drops the leading separator and maps / \ : to -).
func CwdDir(cwd string) string {
	return "--" + seps.ReplaceAllString(leadSep.ReplaceAllString(cwd, ""), "-") + "--"
}

// SessionIDFromPath: <timestamp>_<id>.jsonl → id.
func SessionIDFromPath(p string) string {
	b := strings.TrimSuffix(filepath.Base(p), ".jsonl")
	if i := strings.Index(b, "_"); i >= 0 {
		return b[i+1:]
	}
	return b
}

// ------------------------------------------------------------------ decode

func contentParts(content any, ctx *engine.DecodeContext) []core.Part {
	if s, ok := content.(string); ok {
		if s == "" {
			return []core.Part{}
		}
		return shared.DecodeAttachmentText(s, strings.TrimSuffix(ctx.Ref.Path, ".jsonl")+".assets", ctx.Blob)
	}
	arr, ok := content.([]any)
	if !ok {
		return []core.Part{}
	}
	out := []core.Part{}
	for _, x := range arr {
		c := core.Obj(x)
		if c["type"] == "text" {
			if t, ok := c["text"].(string); ok {
				out = append(out, shared.DecodeAttachmentText(t, strings.TrimSuffix(ctx.Ref.Path, ".jsonl")+".assets", ctx.Blob)...)
			}
		} else if c["type"] == "image" {
			if d, ok := c["data"].(string); ok {
				mt, ok := c["mimeType"].(string)
				if !ok {
					mt = "image/png"
				}
				out = append(out, core.Part{"type": "image", "blob": ctx.Blob(core.Base64(d)), "mediaType": mt})
			}
		}
	}
	return out
}

func strOrNil(v any) any {
	if s, ok := v.(string); ok {
		return s
	}
	return nil
}

// Decoder reads pi JSONL.
type Decoder struct{}

func (Decoder) FormatVersion() string         { return "3" }
func (Decoder) InitialState() core.JsonObject { return core.JsonObject{"version": float64(3)} }

func (Decoder) Decode(r0 any, raw []byte, ctx *engine.DecodeContext) engine.DecodeOut {
	r := core.Obj(r0)
	typ, _ := r["type"].(string)
	if typ == "session" {
		v, ok := core.Num(r["version"])
		if !ok {
			v = 1
		}
		ctx.State["version"] = v
		out := engine.DecodeOut{NativeID: "#header", Meta: core.JsonObject{"cwd": strOrNil(r["cwd"]), "piVersion": v}}
		if ps := core.S(r["parentSession"]); ps != "" {
			out.Lineage = &core.Lineage{Kind: "fork", Harness: "pi", Session: SessionIDFromPath(ps)}
		}
		return out
	}
	// v1 entries have no ids: position instead (v1 files are linear and rewritten on load).
	id, ok := r["id"].(string)
	if !ok {
		id = "#L" + strconv.Itoa(ctx.Index)
	}
	var parent *core.ParentRef
	hasParent := false
	if pv, ok := r["parentId"]; ok && pv == nil {
		parent, hasParent = core.NullParent(), true
	} else if s := core.S(r["parentId"]); s != "" {
		parent, hasParent = core.NativeParent(s), true
	}
	var t *float64
	if s, ok := r["timestamp"].(string); ok {
		t = core.ParseTime(s)
	}
	mk := func(kind, actor string, parts []core.Part, attrs core.JsonObject) *core.DraftEvent {
		d := &core.DraftEvent{NativeID: id, Kind: kind, Actor: actor, Parts: parts, Time: t, Attrs: attrs}
		if hasParent {
			d.Parent = parent
		}
		return d
	}
	alias := engine.DecodeOut{NativeID: id, Alias: parent, HasAlias: hasParent}
	switch typ {
	case "message":
		m := core.Obj(r["message"])
		switch role, _ := m["role"].(string); role {
		case "user":
			return engine.DecodeOut{NativeID: id, Draft: mk("message", "user", contentParts(m["content"], ctx), nil)}
		case "assistant":
			parts := []core.Part{}
			for _, x := range core.Arr(m["content"]) {
				c := core.Obj(x)
				switch c["type"] {
				case "text":
					if s, ok := c["text"].(string); ok {
						parts = append(parts, core.Part{"type": "text", "text": s})
					}
				case "thinking":
					sig := core.S(c["thinkingSignature"])
					if c["redacted"] == true && sig != "" {
						by, ok := m["api"].(string)
						if !ok {
							by = "unknown"
						}
						parts = append(parts, core.Part{"type": "reasoning", "sealed": map[string]any{"by": by, "blob": ctx.Blob([]byte(sig))}})
					} else {
						p := core.Part{"type": "reasoning"}
						if s := core.S(c["thinking"]); s != "" {
							p["text"] = s
						}
						if sig != "" {
							p["signature"] = sig
						}
						parts = append(parts, p)
					}
				case "toolCall":
					name := core.S(c["name"])
					args := c["arguments"]
					if args == nil {
						args = map[string]any{}
					}
					parts = append(parts, core.Part{"type": "tool_call", "callId": core.S(c["id"]), "rawName": name, "args": args, "tool": vocab.ToCanonical(name, args)})
				}
			}
			attrs := core.JsonObject{"model": map[string]any{"provider": strOrNil(m["provider"]), "api": strOrNil(m["api"]), "id": strOrNil(m["model"])}, "stopReason": strOrNil(m["stopReason"])}
			if u, ok := m["usage"]; ok && u != nil && u != false {
				attrs["usage"] = u
			}
			return engine.DecodeOut{NativeID: id, Draft: mk("message", "assistant", parts, attrs)}
		case "toolResult":
			outcome := "success"
			if m["isError"] == true {
				outcome = "error"
			}
			part := core.Part{"type": "tool_result", "callId": core.S(m["toolCallId"]), "outcome": outcome, "parts": core.PartsJSON(contentParts(m["content"], ctx))}
			return engine.DecodeOut{NativeID: id, Draft: mk("tool_result", "tool", []core.Part{part}, core.JsonObject{"toolName": strOrNil(m["toolName"])})}
		case "system":
			sec := m["sections"]
			return engine.DecodeOut{NativeID: id, Draft: mk("context", "system", contentParts(m["content"], ctx), core.JsonObject{"sections": sec})}
		case "bashExecution":
			text := "$ " + core.S(m["command"]) + "\n" + core.S(m["output"])
			kind := "message"
			if m["excludeFromContext"] == true {
				kind = "context"
			}
			return engine.DecodeOut{NativeID: id, Draft: mk(kind, "user", []core.Part{{"type": "text", "text": text}}, core.JsonObject{"piRole": "bashExecution"})}
		case "custom":
			return engine.DecodeOut{NativeID: id, Draft: mk("context", "harness", contentParts(m["content"], ctx), core.JsonObject{"customType": strOrNil(m["customType"])})}
		}
		return alias
	case "compaction":
		d := mk("compaction", "harness", []core.Part{{"type": "text", "text": core.S(r["summary"])}}, nil)
		if first := core.S(r["firstKeptEntryId"]); first != "" && first != id {
			d.Replaces = &core.DraftReplaces{To: core.NativeParent(first), ToExclusive: true}
		} else if hasParent && !parent.Null {
			d.Replaces = &core.DraftReplaces{To: parent}
		}
		return engine.DecodeOut{NativeID: id, Draft: d}
	case "branch_summary":
		return engine.DecodeOut{NativeID: id, Draft: mk("compaction", "harness", []core.Part{{"type": "text", "text": core.S(r["summary"])}}, core.JsonObject{"branchSummary": true})}
	case "context_edit":
		target := core.S(r["targetId"])
		if target == "" {
			return alias
		}
		parts := []core.Part{}
		if rep := r["replacement"]; rep != nil {
			parts = contentParts(rep, ctx)
		}
		d := mk("x-context-edit", "harness", parts, nil)
		d.Replaces = &core.DraftReplaces{From: core.NativeParent(target), To: core.NativeParent(target)}
		return engine.DecodeOut{NativeID: id, Draft: d}
	case "model_change":
		return engine.DecodeOut{NativeID: id, Draft: mk("meta", "harness", []core.Part{}, core.JsonObject{"model": map[string]any{"provider": strOrNil(r["provider"]), "api": nil, "id": strOrNil(r["modelId"])}})}
	case "thinking_level_change":
		return engine.DecodeOut{NativeID: id, Draft: mk("meta", "harness", []core.Part{}, core.JsonObject{"thinkingLevel": strOrNil(r["thinkingLevel"])})}
	case "custom_message":
		return engine.DecodeOut{NativeID: id, Draft: mk("context", "harness", contentParts(r["content"], ctx), core.JsonObject{"customType": strOrNil(r["customType"])})}
	case "session_info":
		alias.Meta = core.JsonObject{"title": strOrNil(r["name"])}
		return alias
	}
	// label, custom, usage, unknown future types: no event; children still resolve through the alias
	return alias
}

// ------------------------------------------------------------------ encode

func renderItem(it planner.Item, in engine.PlanInput, t float64) []core.O {
	h := in.Hub
	same := it.Event.Provenance.Source.Harness == "pi"
	media := func(parts []core.Part) []any {
		out := []any{}
		for _, p := range parts {
			switch core.S(p["type"]) {
			case "text":
				out = append(out, core.O{{K: "type", V: "text"}, {K: "text", V: core.S(p["text"])}})
			case "image":
				if data, ok := shared.BlobBase64(h, core.S(core.Obj(p["blob"])["sha256"])); ok {
					out = append(out, core.O{{K: "type", V: "image"}, {K: "data", V: data}, {K: "mimeType", V: core.S(p["mediaType"])}})
				} else {
					out = append(out, core.O{{K: "type", V: "text"}, {K: "text", V: "[image " + core.S(p["mediaType"]) + " unavailable]"}})
				}
			case "file":
				out = append(out, core.O{{K: "type", V: "text"}, {K: "text", V: shared.PartsText(h, []core.Part{p}, 0)}})
			}
		}
		return out
	}
	switch it.Role {
	case "user":
		return []core.O{{{K: "role", V: "user"}, {K: "content", V: media(it.Parts)}, {K: "timestamp", V: t}}}
	case "assistant":
		content := []any{}
		calls := 0
		for _, p := range it.Parts {
			switch core.S(p["type"]) {
			case "text":
				content = append(content, core.O{{K: "type", V: "text"}, {K: "text", V: core.S(p["text"])}})
			case "reasoning":
				if sealed, ok := p["sealed"].(map[string]any); ok {
					sig := ""
					if b, ok := h.Blob(core.S(core.Obj(sealed["blob"])["sha256"])); ok {
						sig = string(b)
					}
					content = append(content, core.O{{K: "type", V: "thinking"}, {K: "thinking", V: ""}, {K: "thinkingSignature", V: sig}, {K: "redacted", V: true}})
				} else {
					o := core.O{{K: "type", V: "thinking"}, {K: "thinking", V: core.S(p["text"])}}
					if s := core.S(p["signature"]); s != "" {
						o = append(o, core.KV{K: "thinkingSignature", V: s})
					}
					content = append(content, o)
				}
			case "tool_call":
				n := core.NativeToolCall{Name: core.S(p["rawName"]), Args: p["args"]}
				if !same {
					if x, ok := vocab.FromCanonical(core.Obj(p["tool"])); ok {
						n = x
					}
				}
				content = append(content, core.O{{K: "type", V: "toolCall"}, {K: "id", V: core.S(p["callId"])}, {K: "name", V: n.Name}, {K: "arguments", V: n.Args}})
				calls++
			}
		}
		m := core.Obj(it.Event.Attrs["model"])
		or := func(k string) string {
			if s, ok := m[k].(string); ok {
				return s
			}
			return "unknown"
		}
		stop := "stop"
		if calls > 0 {
			stop = "toolUse"
		}
		zero := core.O{{K: "input", V: 0}, {K: "output", V: 0}, {K: "cacheRead", V: 0}, {K: "cacheWrite", V: 0}}
		usage := append(append(core.O{}, zero...), core.KV{K: "totalTokens", V: 0}, core.KV{K: "cost", V: append(append(core.O{}, zero...), core.KV{K: "total", V: 0})})
		return []core.O{{{K: "role", V: "assistant"}, {K: "content", V: content}, {K: "api", V: or("api")}, {K: "provider", V: or("provider")}, {K: "model", V: or("id")},
			{K: "usage", V: usage}, {K: "stopReason", V: stop}, {K: "timestamp", V: t}}}
	}
	// tool: one toolResult message per result part
	var out []core.O
	for _, p := range it.Parts {
		if core.S(p["type"]) != "tool_result" {
			continue
		}
		id := core.S(p["callId"])
		name := "unknown"
		if call, ok := in.Projection.Calls[id]; ok {
			name = core.S(call["rawName"])
			if !same {
				if x, ok := vocab.FromCanonical(core.Obj(call["tool"])); ok {
					name = x.Name
				}
			}
		} else if s, ok := it.Event.Attrs["toolName"].(string); ok {
			name = s
		}
		out = append(out, core.O{{K: "role", V: "toolResult"}, {K: "toolCallId", V: id}, {K: "toolName", V: name}, {K: "content", V: media(core.Parts(p["parts"]))}, {K: "isError", V: core.S(p["outcome"]) != "success"}, {K: "timestamp", V: t}})
	}
	return out
}

// Options for the connector.
type Options struct {
	Root string // $PI_CODING_AGENT_DIR
	ID   string
}

type piConn struct {
	id, root, sessions string
	files              *shared.PathIndex
}

// New makes a pi connector.
func New(o Options) *engine.Connector {
	if o.ID == "" {
		o.ID = "pi"
	}
	c := &piConn{id: o.ID, root: o.Root, sessions: filepath.Join(o.Root, "sessions"), files: shared.NewPathIndex()}
	return &engine.Connector{ID: c.id, Root: c.root, Harness: "pi", Version: Version, Capabilities: Capabilities, Vocabulary: vocab, Source: c, Sink: c, Trash: c}
}

func (c *piConn) Discover(ctx context.Context) ([]core.NativeRef, error) {
	dirs, err := os.ReadDir(c.sessions)
	if err != nil {
		return nil, nil
	}
	var out []core.NativeRef
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join(c.sessions, d.Name())
		fs, _ := os.ReadDir(dir)
		for _, f := range fs {
			if !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			p := filepath.Join(dir, f.Name())
			st, err := os.Stat(p)
			if err != nil {
				continue
			}
			session := SessionIDFromPath(f.Name())
			c.files.Set(session, p)
			out = append(out, core.NativeRef{Harness: "pi", Connector: c.id, Session: session, Path: p, Size: st.Size(), MtimeMs: engine.MtimeMs(st)})
		}
	}
	return out, nil
}

func (c *piConn) Tail(ctx context.Context, ref core.NativeRef, from *core.SourceOffset, yield func(core.SourceBatch) error) error {
	return engine.TailJSONL(ctx, ref, from, Decoder{}, yield, 0, false)
}

func (c *piConn) WatchRoots() []string { return []string{c.sessions} }

func (c *piConn) ReadRecord(session string, index int, offset *int64) ([]byte, error) {
	p, ok := c.files.Get(session)
	if !ok {
		_, _ = c.Discover(context.Background())
		p, ok = c.files.Get(session)
	}
	if !ok || offset == nil {
		return nil, nil
	}
	return engine.ReadRecordAt(p, *offset)
}

func (c *piConn) Files(s engine.SessionRef) []string {
	file, ok := c.files.Get(s.Session)
	if !ok {
		file = s.Path
	}
	if file != "" {
		if _, err := os.Stat(file); err == nil {
			return []string{file}
		}
	}
	return nil
}

func (c *piConn) NewSessionID(key string) string { return core.UUID5("pi\x00" + key) }
func (c *piConn) ResumeCommand() string          { return "pi --session {id}" }

func (c *piConn) OpenBy(s engine.SessionRef) string {
	cwd, _ := s.Meta["cwd"].(string)
	return OpenBy(s.Session, s.Path, cwd)
}

// AppendBlocked: pi migrates older files (v1/v2) on load and rewrites them; continue those in a new session.
func (c *piConn) AppendBlocked(s engine.SessionRef) string {
	v, ok := s.Meta["piVersion"]
	if !ok || v == nil {
		return ""
	}
	if n, ok := core.Num(v); ok && n == 3 {
		return ""
	}
	return fmt.Sprintf("pi session format v%v (pi rewrites it on load)", v)
}

func (c *piConn) Plan(in engine.PlanInput) (*engine.DeliveryPlan, error) {
	t, proj := in.Target, in.Projection
	var clock shared.Clock
	used := map[string]bool{}
	var prev any
	file := ""
	if t.Mode != "create" {
		if p, ok := c.files.Get(t.Session); ok {
			file = p
		} else if b := in.Hub.Binding("pi", t.Session); b != nil {
			file = b.Path
		}
	}
	// Ids are per file: never mint one the session already has (its parentId links would fork).
	if file != "" {
		if f, err := os.Open(file); err == nil {
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 1<<20), 1<<30)
			for sc.Scan() {
				var r struct {
					ID any `json:"id"`
				}
				if json.Unmarshal(bytes.TrimSpace(sc.Bytes()), &r) == nil {
					if s, ok := r.ID.(string); ok {
						used[s] = true
					}
				}
			}
			f.Close()
		}
	}
	var out []shared.NativeLine
	firstTime := 0.0
	for _, it := range proj.Items {
		if it.Event.Time != nil && *it.Event.Time != 0 {
			firstTime = *it.Event.Time
			break
		}
	}
	headEvent := in.Head
	if len(proj.Items) > 0 {
		headEvent = proj.Items[0].Event.ID
	}
	if t.Mode == "create" {
		home, _ := os.UserHomeDir()
		cwd := t.Cwd
		if cwd == "" {
			cwd = proj.Hints.Cwd
		}
		if cwd == "" {
			cwd = home
		}
		file = filepath.Join(c.sessions, CwdDir(cwd), shared.ISONoColons(firstTime)+"_"+t.Session+".jsonl")
		out = append(out, shared.NativeLine{NativeID: "#header", EventID: headEvent, Line: core.Stringify(core.O{{K: "type", V: "session"}, {K: "version", V: 3}, {K: "id", V: t.Session}, {K: "timestamp", V: shared.ISO(firstTime)}, {K: "cwd", V: cwd}})})
	} else {
	}
	in, attachments := shared.PrepareAttachments(in, strings.TrimSuffix(file, ".jsonl")+".assets", func(core.Part, string) bool { return false })
	proj = in.Projection
	mint := func(key string) string {
		for salt := 0; ; salt++ {
			id := core.ShortID(t.Session+"\x00"+key, 8, salt)
			if !used[id] {
				used[id] = true
				return id
			}
		}
	}
	// The title as pi's /name writes it; first in the tree, so later appends never branch off it.
	if t.Mode == "create" && t.Title != "" {
		nid := mint("#title")
		out = append(out, shared.NativeLine{NativeID: "#title", EventID: headEvent, Line: core.Stringify(core.O{{K: "type", V: "session_info"}, {K: "id", V: nid}, {K: "parentId", V: nil}, {K: "timestamp", V: shared.ISO(firstTime)}, {K: "name", V: t.Title}})})
		prev = nid
	}
	for _, it := range proj.Items {
		ts := clock.Next(it.Event)
		if it.Verbatim && in.Residual != nil {
			if raw := in.Residual(it.Event); raw != nil {
				var rec map[string]any
				if json.Unmarshal(raw, &rec) == nil {
					nid := mint(it.Key)
					rec["id"], rec["parentId"] = nid, prev
					out = append(out, shared.NativeLine{NativeID: nid, EventID: it.Event.ID, Line: core.Stringify(rec)})
					prev = nid
					continue
				}
			}
		}
		msgs := renderItem(it, in, ts)
		for i, m := range msgs {
			key := it.Key
			if len(msgs) > 1 {
				key += "#" + strconv.Itoa(i)
			}
			nid := mint(key)
			out = append(out, shared.NativeLine{NativeID: nid, EventID: it.Event.ID, Line: core.Stringify(core.O{{K: "type", V: "message"}, {K: "id", V: nid}, {K: "parentId", V: prev}, {K: "timestamp", V: shared.ISO(ts)}, {K: "message", V: m}})})
			prev = nid
		}
	}
	mode := t.Mode
	if len(out) == 0 {
		mode = "noop"
	}
	content, all := shared.LinesToContent(out)
	var shas []string
	records := []engine.PlanRecord{}
	for _, r := range all {
		shas = append(shas, r.SHA256)
		if r.NativeID != "#header" && r.NativeID != "#title" {
			records = append(records, r)
		}
	}
	if file != "" {
		t.Path = file
	}
	return &engine.DeliveryPlan{ID: shared.PlanID(c.id, t.Session, in.Head, mode, strings.Join(shas, "")), Sink: c.id, Harness: "pi", Thread: in.Thread, Head: in.Head,
		Level: in.Level, Mode: mode, Target: t, Records: records, Content: content, Losses: proj.Losses, Notes: proj.Notes, Extra: core.JsonObject{"attachments": attachments}}, nil
}

func (c *piConn) Apply(ctx context.Context, plan *engine.DeliveryPlan) (core.JsonObject, error) {
	if err := shared.ApplyAttachments(plan); err != nil {
		return nil, err
	}
	r, err := shared.ApplyFilePlan(plan)
	if err == nil {
		c.files.Set(plan.Target.Session, plan.Target.Path)
	}
	return r, err
}

var piProc = regexp.MustCompile(`pi-coding-agent|(^|/)pi(\s|$)`)

// OpenBy: pi keeps no lock or registry and opens the file only per write, so a running pi whose
// command line names this session, or that runs in the session's cwd, counts as open.
func OpenBy(session, file, cwd string) string {
	procs, _ := shared.ProcessesWithCwd(piProc)
	for _, p := range procs {
		if strings.Contains(p.Command, session) || (file != "" && strings.Contains(p.Command, filepath.Base(file))) {
			if strings.Contains(p.Command, session) {
				return fmt.Sprintf("pi pid %d has it open", p.Pid)
			}
			return fmt.Sprintf("pi pid %d runs in %s", p.Pid, cwd)
		}
	}
	if cwd != "" {
		for _, p := range procs {
			if p.Cwd != "" && shared.SamePath(p.Cwd, cwd) {
				return fmt.Sprintf("pi pid %d runs in %s", p.Pid, cwd)
			}
		}
	}
	return ""
}
