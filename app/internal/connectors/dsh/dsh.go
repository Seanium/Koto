// Package dsh is the DeepSeek Harness connector, session format V4 (verified against dsh
// 0.2.0-rc.2: real sessions ingest; written sessions pass dsh's own log verifier and resume).
//
// Storage root: $DSH_HOME (default ~/.dsh); sessions at
// sessions/<project-key>/<encoded-session-id>/session.v4.jsonl[.zstd]. The log is append-only with
// contiguous seq; surfaceOp {op:'replace', startSeq, endSeq} replaces a surface range (compaction)
// without deleting records — mapped to canonical replaces.
//
// Sink: create, and append to an existing session while holding its session.lock flock (dsh's
// single-writer lock). A session open in dsh holds the lock, so an append then fails as "in use"
// instead of tearing the log.
package dsh

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/planner"
)

const (
	Version    = "dsh-connector@0.2.0"
	systemHead = "You are an AI agent powered by DeepSeek Harness. This session was imported by koto from another coding agent; continue the conversation below."
)

func safeParse(s string) any {
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return s
	}
	return v
}

// Vocabulary: dsh tool names vary by bundle; the common coding tools are mapped, the rest stays raw.
type Vocabulary struct{}

func (Vocabulary) ToCanonical(name string, args any) map[string]any {
	if s, ok := args.(string); ok {
		args = safeParse(s)
	}
	a := core.Obj(args)
	pick := func(k1, k2 string) string {
		if s, ok := a[k1].(string); ok {
			return s
		}
		return core.S(a[k2])
	}
	switch name {
	case "read", "read_file":
		return map[string]any{"op": "read", "path": pick("path", "file_path")}
	case "write", "write_file":
		t := map[string]any{"op": "write", "path": pick("path", "file_path")}
		if c, ok := a["content"].(string); ok {
			t["content"] = c
		}
		return t
	case "bash", "shell", "exec":
		return map[string]any{"op": "shell", "command": pick("command", "cmd")}
	}
	return map[string]any{"op": "raw"}
}

func (Vocabulary) FromCanonical(t map[string]any) (core.NativeToolCall, bool) {
	switch core.S(t["op"]) {
	case "read":
		return core.NativeToolCall{Name: "read", Args: map[string]any{"path": core.S(t["path"])}}, true
	case "write":
		c, ok := t["content"].(string)
		if !ok {
			return core.NativeToolCall{}, false
		}
		return core.NativeToolCall{Name: "write", Args: map[string]any{"path": core.S(t["path"]), "content": c}}, true
	case "shell":
		return core.NativeToolCall{Name: "bash", Args: map[string]any{"command": core.S(t["command"])}}, true
	}
	return core.NativeToolCall{}, false
}

var vocab = Vocabulary{}

var Capabilities = core.Capabilities{
	Read:         core.ReadCaps{Images: true, Reasoning: "plain", Subagents: true, Compaction: true},
	Write:        &core.WriteCaps{Mode: "native-file", Images: true, Reasoning: "plain", Tools: "canonical"},
	WriteProfile: "dsh-write@2", FormatVersions: []string{"4"},
}

var safeCh = regexp.MustCompile(`^[A-Za-z0-9._-]$`)

func enc16(u uint16) string { return fmt.Sprintf("~%04X", u) }

// ProjectKey is dsh's projectKey(): separators → '-', unsafe UTF-16 units → ~XXXX, wrapped in -- --.
func ProjectKey(cwd string) string {
	var b strings.Builder
	sep := false
	for _, u := range utf16.Encode([]rune(cwd)) {
		ch := string(rune(u))
		switch {
		case ch == "/" || ch == "\\" || ch == ":":
			if !sep {
				b.WriteByte('-')
			}
			sep = true
		case u < 0x80 && ch != "~" && safeCh.MatchString(ch):
			b.WriteString(ch)
			sep = false
		default:
			b.WriteString(enc16(u))
			sep = false
		}
	}
	out := strings.TrimLeft(b.String(), "-")
	if out == "" {
		out = "root"
	}
	if len(out) > 251 {
		out = out[:251]
	}
	return "--" + out + "--"
}

// EncodeSegment encodes a session id as a directory name.
func EncodeSegment(raw string) string {
	if raw == "." {
		return "~002E"
	}
	if raw == ".." {
		return "~002E~002E"
	}
	var b strings.Builder
	for _, u := range utf16.Encode([]rune(raw)) {
		ch := string(rune(u))
		if u < 0x80 && ch != "~" && safeCh.MatchString(ch) {
			b.WriteString(ch)
		} else {
			b.WriteString(enc16(u))
		}
	}
	return b.String()
}

var escRe = regexp.MustCompile(`~([0-9A-F]{4})`)

// DecodeSegment is the inverse of EncodeSegment.
func DecodeSegment(seg string) string {
	var units []uint16
	last := 0
	for _, m := range escRe.FindAllStringSubmatchIndex(seg, -1) {
		units = append(units, utf16.Encode([]rune(seg[last:m[0]]))...)
		n, _ := strconv.ParseUint(seg[m[2]:m[3]], 16, 16)
		units = append(units, uint16(n))
		last = m[1]
	}
	units = append(units, utf16.Encode([]rune(seg[last:]))...)
	return string(utf16.Decode(units))
}

// ------------------------------------------------------------------ decode

func blocks(content any, ctx *engine.DecodeContext) []core.Part {
	out := []core.Part{}
	for _, x := range core.Arr(content) {
		b := core.Obj(x)
		switch b["type"] {
		case "text":
			if s, ok := b["text"].(string); ok {
				out = append(out, core.Part{"type": "text", "text": s})
			}
		case "reasoning":
			p := core.Part{"type": "reasoning"}
			if s := core.S(b["text"]); s != "" {
				p["text"] = s
			}
			out = append(out, p)
		case "tool-call":
			name := core.S(b["name"])
			argStr, ok := b["arguments"].(string)
			if !ok {
				argStr = "{}"
			}
			args := safeParse(argStr)
			out = append(out, core.Part{"type": "tool_call", "callId": core.S(b["id"]), "rawName": name, "args": args, "tool": vocab.ToCanonical(name, args)})
		case "image", "file":
			out = append(out, attachmentPart(b, ctx))
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

// Decoder reads dsh V4 logs.
type Decoder struct{}

func (Decoder) FormatVersion() string         { return "4" }
func (Decoder) InitialState() core.JsonObject { return core.JsonObject{"version": float64(4)} }

func numStr(f float64) string { s, _ := core.ESNumber(f); return s }

func (Decoder) Decode(r0 any, raw []byte, ctx *engine.DecodeContext) engine.DecodeOut {
	r := core.Obj(r0)
	typ := core.S(r["type"])
	if typ == "session" {
		v, ok := r["version"].(float64)
		if !ok {
			v = 0
		}
		ctx.State["version"] = v
		out := engine.DecodeOut{NativeID: "#header", Meta: core.JsonObject{"cwd": strOrNil(r["cwd"]), "dshVersion": v, "origin": strOrNil(r["origin"])}}
		if ps := core.S(r["parentSession"]); ps != "" {
			kind := "fork"
			if r["origin"] == "subagent" {
				kind = "spawn"
			}
			out.Lineage = &core.Lineage{Kind: kind, Harness: "dsh", Session: ps, Copied: r["isSeeded"] == true}
		}
		return out
	}
	seq, ok := r["seq"].(float64)
	if !ok {
		seq = float64(ctx.Index)
	}
	nativeID := "s" + numStr(seq)
	var t *float64
	if f, ok := r["time"].(float64); ok && f > 0 {
		t = &f
	}
	d := core.Obj(r["data"])
	op := core.Obj(r["surfaceOp"])
	var replaces *core.DraftReplaces
	if op["op"] == "replace" {
		s, ok1 := op["startSeq"].(float64)
		e, ok2 := op["endSeq"].(float64)
		if ok1 && ok2 {
			replaces = &core.DraftReplaces{From: core.NativeParent("s" + numStr(s)), To: core.NativeParent("s" + numStr(e))}
		}
	}
	ev := func(kind, actor string, parts []core.Part, attrs core.JsonObject) engine.DecodeOut {
		return engine.DecodeOut{NativeID: nativeID, Draft: &core.DraftEvent{NativeID: nativeID, Time: t, Kind: kind, Actor: actor, Parts: parts, Attrs: attrs, Replaces: replaces}}
	}
	switch typ {
	case "user/message":
		// dsh injects runtime-context snapshots (sandbox / approval policy) as user-role messages
		if k := core.S(core.Obj(d["source"])["kind"]); replaces == nil && k != "" && k != "user" {
			return ev("context", "harness", blocks(d["content"], ctx), nil)
		}
		if replaces != nil {
			return ev("compaction", "harness", blocks(d["content"], ctx), nil)
		}
		return ev("message", "user", blocks(d["content"], ctx), nil)
	case "assistant/message":
		m := core.Obj(d["message"])
		src := core.Obj(m["source"])
		attrs := core.JsonObject{"model": map[string]any{"provider": strOrNil(src["provider"]), "api": nil, "id": strOrNil(src["model"])}}
		if d["interrupted"] == true {
			attrs["stopReason"] = "aborted"
		}
		return ev("message", "assistant", blocks(m["content"], ctx), attrs)
	case "tool/result":
		m := core.Obj(d["message"])
		outcome := "success"
		if e := d["error"]; m["isError"] == true || (e != nil && e != false && e != "" && e != float64(0)) {
			outcome = "error"
		}
		return ev("tool_result", "tool", []core.Part{{"type": "tool_result", "callId": core.S(m["toolCallId"]), "outcome": outcome, "parts": core.PartsJSON(blocks(m["content"], ctx))}}, nil)
	case "system/message", "developer/message":
		return ev("context", "system", blocks(core.Obj(d["message"])["content"], ctx), nil)
	}
	// turn/step markers, tool/call (duplicated in the assistant message), request/*, plugin:* …
	return engine.DecodeOut{NativeID: "#" + typ + numStr(seq)}
}

// ------------------------------------------------------------------ encode (V4)

func renderDsh(it planner.Item, in engine.PlanInput, turn, step int, seq func() int, t float64, callSeqs *orderedCalls) []core.O {
	text := func(ps []core.Part) []any {
		out := []any{}
		for _, p := range ps {
			switch core.S(p["type"]) {
			case "text":
				out = append(out, core.O{{K: "type", V: "text"}, {K: "text", V: core.S(p["text"])}})
			case "image", "file":
				out = append(out, core.O{{K: "type", V: p["type"]}, {K: "attachment", V: p["attachment"]}})
			}
		}
		return out
	}
	switch it.Role {
	case "user":
		return []core.O{{{K: "type", V: "user/message"}, {K: "seq", V: seq()}, {K: "time", V: t}, {K: "data", V: core.O{{K: "id", V: it.Key}, {K: "role", V: "user"}, {K: "source", V: core.O{{K: "kind", V: "user"}}}, {K: "content", V: text(it.Parts)}}}, {K: "surfaceOp", V: "append"}}}
	case "assistant":
		content := []any{}
		type call struct{ id, name, args string }
		var calls []call
		for _, p := range it.Parts {
			switch core.S(p["type"]) {
			case "text":
				content = append(content, core.O{{K: "type", V: "text"}, {K: "text", V: core.S(p["text"])}})
			case "image", "file":
				content = append(content, core.O{{K: "type", V: p["type"]}, {K: "attachment", V: p["attachment"]}})
			case "reasoning":
				if s := core.S(p["text"]); s != "" {
					content = append(content, core.O{{K: "type", V: "reasoning"}, {K: "text", V: s}})
				}
			case "tool_call":
				n := core.NativeToolCall{Name: core.S(p["rawName"]), Args: p["args"]}
				if it.Event.Provenance.Source.Harness != "dsh" {
					if x, ok := vocab.FromCanonical(core.Obj(p["tool"])); ok {
						n = x
					}
				}
				args, ok := n.Args.(string)
				if !ok {
					args = core.Stringify(n.Args)
				}
				content = append(content, core.O{{K: "type", V: "tool-call"}, {K: "id", V: core.S(p["callId"])}, {K: "name", V: n.Name}, {K: "arguments", V: args}})
				calls = append(calls, call{core.S(p["callId"]), n.Name, args})
			}
		}
		m := core.Obj(it.Event.Attrs["model"])
		or := func(k string) string {
			if s, ok := m[k].(string); ok {
				return s
			}
			return "unknown"
		}
		msg := core.O{{K: "type", V: "assistant/message"}, {K: "seq", V: seq()}, {K: "time", V: t}, {K: "data", V: core.O{{K: "turn", V: turn}, {K: "step", V: step},
			{K: "message", V: core.O{{K: "id", V: it.Key}, {K: "role", V: "assistant"}, {K: "source", V: core.O{{K: "kind", V: "model"}, {K: "provider", V: or("provider")}, {K: "model", V: or("id")}}}, {K: "content", V: content}}},
			{K: "stream", V: []any{}}}}, {K: "surfaceOp", V: "append"}}
		out := []core.O{msg}
		// dsh's lifecycle: every advertised tool-call is started by a matching tool/call before its result
		for _, c := range calls {
			s := seq()
			out = append(out, core.O{{K: "type", V: "tool/call"}, {K: "seq", V: s}, {K: "time", V: t}, {K: "data", V: core.O{{K: "turn", V: turn}, {K: "step", V: step}, {K: "callId", V: c.id}, {K: "name", V: c.name}, {K: "arguments", V: c.args}}}})
			callSeqs.set(c.id, s)
		}
		return out
	}
	var out []core.O
	for _, p := range it.Parts {
		if core.S(p["type"]) != "tool_result" {
			continue
		}
		id := core.S(p["callId"])
		from, has := callSeqs.get(id)
		callSeqs.del(id)
		rec := core.O{{K: "type", V: "tool/result"}, {K: "seq", V: seq()}, {K: "time", V: t}, {K: "data", V: core.O{{K: "turn", V: turn}, {K: "step", V: step},
			{K: "message", V: core.O{{K: "id", V: it.Key + ":" + id}, {K: "role", V: "tool"}, {K: "source", V: core.O{{K: "kind", V: "tool"}, {K: "callId", V: id}}}, {K: "toolCallId", V: id}, {K: "content", V: text(core.Parts(p["parts"]))}, {K: "isError", V: core.S(p["outcome"]) != "success"}}}}}}
		if has {
			rec = append(rec, core.KV{K: "sourceEventSeqs", V: []any{from}})
		}
		out = append(out, append(rec, core.KV{K: "surfaceOp", V: "append"}))
	}
	return out
}

type orderedCalls struct {
	keys []string
	m    map[string]int
}

func (o *orderedCalls) set(k string, v int) {
	if o.m == nil {
		o.m = map[string]int{}
	}
	if _, ok := o.m[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.m[k] = v
}
func (o *orderedCalls) get(k string) (int, bool) { v, ok := o.m[k]; return v, ok }
func (o *orderedCalls) del(k string) {
	if _, ok := o.m[k]; !ok {
		return
	}
	delete(o.m, k)
	for i, x := range o.keys {
		if x == k {
			o.keys = append(o.keys[:i:i], o.keys[i+1:]...)
			break
		}
	}
}

// State is the tail of an existing dsh log: next seq, last turn, whether a turn is still open.
type State struct {
	Seq, Turn       int
	HasSystem, Open bool
}

// ReadState reads a log's tail state; upTo = only the prefix scanned (appends are whole frames),
// so every process plans the same bytes for the same head.
func ReadState(file string, upTo *int64) (State, error) {
	all, err := os.ReadFile(file)
	if err != nil {
		return State{}, err
	}
	raw := all
	if upTo != nil && *upTo <= int64(len(all)) {
		raw = all[:*upTo]
	}
	text := raw
	if strings.HasSuffix(file, ".zstd") {
		if text, err = core.ZstdDecompressAll(raw); err != nil {
			return State{}, err
		}
	}
	if len(text) > 0 && text[len(text)-1] != '\n' {
		return State{Open: true}, nil // partial record: being written
	}
	seq := -1
	st := State{}
	for _, line := range strings.Split(string(text), "\n") {
		if line == "" {
			continue
		}
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return State{}, err
		}
		if s, ok := r["seq"].(float64); ok {
			seq = int(math.Max(float64(seq), s))
		}
		d := core.Obj(r["data"])
		switch r["type"] {
		case "turn/start":
			st.Open = true
			if t, ok := d["turn"].(float64); ok && int(t) > st.Turn {
				st.Turn = int(t)
			}
		case "turn/end":
			st.Open = false
		case "system/message":
			st.HasSystem = true
		}
	}
	st.Seq = seq + 1
	return st, nil
}

// Options for the connector.
type Options struct {
	Home string // $DSH_HOME
	ID   string
}

type dshConn struct {
	id, home, root string
	files          *shared.PathIndex
}

// New makes a dsh connector.
func New(o Options) *engine.Connector {
	if o.ID == "" {
		o.ID = "dsh"
	}
	c := &dshConn{id: o.ID, home: o.Home, root: filepath.Join(o.Home, "sessions"), files: shared.NewPathIndex()}
	return &engine.Connector{ID: c.id, Root: c.home, Harness: "dsh", Version: Version, Capabilities: Capabilities, Vocabulary: vocab, Source: c, Sink: c, Trash: c}
}

var genRe = regexp.MustCompile(`^session(?:\.v(\d+))?\.jsonl(\.zstd)?$`)

func (c *dshConn) Discover(ctx context.Context) ([]core.NativeRef, error) {
	projs, err := os.ReadDir(c.root)
	if err != nil {
		return nil, nil
	}
	var out []core.NativeRef
	for _, p := range projs {
		if !p.IsDir() {
			continue
		}
		ss, _ := os.ReadDir(filepath.Join(c.root, p.Name()))
		for _, s := range ss {
			if !s.IsDir() {
				continue
			}
			dir := filepath.Join(c.root, p.Name(), s.Name())
			// the highest generation wins (session.vN.jsonl[.zstd]); older ones are predecessors
			type gen struct {
				f string
				n int
			}
			var gens []gen
			fs, _ := os.ReadDir(dir)
			for _, f := range fs {
				if m := genRe.FindStringSubmatch(f.Name()); m != nil {
					n, _ := strconv.Atoi(m[1])
					gens = append(gens, gen{f.Name(), n})
				}
			}
			if len(gens) == 0 {
				continue
			}
			sort.SliceStable(gens, func(i, j int) bool { return gens[i].n > gens[j].n })
			path := filepath.Join(dir, gens[0].f)
			st, err := os.Stat(path)
			if err != nil {
				continue
			}
			session := DecodeSegment(s.Name())
			c.files.Set(session, path)
			out = append(out, core.NativeRef{Harness: "dsh", Connector: c.id, Session: session, Path: path, Size: st.Size(), MtimeMs: engine.MtimeMs(st), Meta: core.JsonObject{"generation": float64(gens[0].n)}})
		}
	}
	return out, nil
}

func (c *dshConn) Tail(ctx context.Context, ref core.NativeRef, from *core.SourceOffset, yield func(core.SourceBatch) error) error {
	meta := core.JsonObject{}
	for k, v := range ref.Meta {
		meta[k] = v
	}
	meta["attachmentHome"] = c.home
	ref.Meta = meta
	return engine.TailJSONL(ctx, ref, from, Decoder{}, yield, 0, false)
}

func (c *dshConn) WatchRoots() []string { return []string{c.root} }

func (c *dshConn) ReadRecord(session string, index int, offset *int64) ([]byte, error) {
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

// Files: a dsh session is its own directory (every generation, the lock file).
func (c *dshConn) Files(s engine.SessionRef) []string {
	file, ok := c.files.Get(s.Session)
	if !ok {
		file = s.Path
	}
	if file == "" {
		return nil
	}
	if _, err := os.Stat(file); err != nil {
		return nil
	}
	dir := filepath.Dir(file)
	if filepath.Dir(filepath.Dir(dir)) == c.root && DecodeSegment(filepath.Base(dir)) == s.Session {
		return []string{dir}
	}
	return []string{file}
}

func (c *dshConn) NewSessionID(key string) string { return "session-" + core.UUID5("dsh\x00"+key) }
func (c *dshConn) ResumeCommand() string          { return "dsh --resume {id}" }

func (c *dshConn) OpenBy(s engine.SessionRef) string {
	file, ok := c.files.Get(s.Session)
	if !ok {
		file = s.Path
	}
	return OpenBy(file)
}

func (c *dshConn) Plan(in engine.PlanInput) (*engine.DeliveryPlan, error) {
	in, attachments, err := prepareAttachments(in, c.home)
	if err != nil {
		return nil, err
	}
	t, proj := in.Target, in.Projection
	reject := func(why string) *engine.DeliveryPlan {
		notes := append(append([]string{}, proj.Notes...), why)
		return &engine.DeliveryPlan{ID: shared.PlanID(c.id, t.Session, in.Head, "reject", why), Sink: c.id, Harness: "dsh", Thread: in.Thread, Head: in.Head, Level: in.Level,
			Mode: "reject", Target: t, Records: []engine.PlanRecord{}, Losses: proj.Losses, Notes: notes}
	}
	noop := func() *engine.DeliveryPlan {
		p := reject("")
		p.ID, p.Mode, p.Notes = shared.PlanID(c.id, t.Session, in.Head, "noop", ""), "noop", proj.Notes
		return p
	}
	if t.Mode == "noop" {
		return noop(), nil
	}
	var file, header string
	state := State{}
	if t.Mode == "create" {
		home, _ := os.UserHomeDir()
		cwd := t.Cwd
		if cwd == "" {
			cwd = proj.Hints.Cwd
		}
		if cwd == "" {
			cwd = home
		}
		file = filepath.Join(c.root, ProjectKey(cwd), EncodeSegment(t.Session), "session.v4.jsonl.zstd")
		created := 0.0
		if len(proj.Items) > 0 && proj.Items[0].Event.Time != nil {
			created = *proj.Items[0].Event.Time
		}
		header = core.Stringify(core.O{{K: "type", V: "session"}, {K: "version", V: 4}, {K: "id", V: t.Session}, {K: "createdAt", V: created}, {K: "cwd", V: cwd}, {K: "isSeeded", V: false}, {K: "delegationDepth", V: 0}})
	}
	var clock shared.Clock
	n := state.Seq
	seq := func() int { n++; return n - 1 }
	type line struct{ native, event, text string }
	var out []line
	push := func(rec core.O, eventID string) {
		s, _ := rec.Get("seq")
		out = append(out, line{"s" + strconv.Itoa(s.(int)), eventID, core.Stringify(rec)})
	}
	turn, step := state.Turn, 0
	open, hasSystem, afterTools := false, state.HasSystem, false
	calls := &orderedCalls{}
	closeTurn := func(t float64) {
		if !open {
			return
		}
		// a call the source never answered gets an error result, so the lifecycle stays closed
		for _, id := range calls.keys {
			from := calls.m[id]
			push(core.O{{K: "type", V: "tool/result"}, {K: "seq", V: seq()}, {K: "time", V: t}, {K: "data", V: core.O{{K: "turn", V: turn}, {K: "step", V: step},
				{K: "message", V: core.O{{K: "id", V: "koto-unanswered-" + id}, {K: "role", V: "tool"}, {K: "source", V: core.O{{K: "kind", V: "tool"}, {K: "callId", V: id}}}, {K: "toolCallId", V: id},
					{K: "content", V: []any{core.O{{K: "type", V: "text"}, {K: "text", V: "(no result was recorded for this call)"}}}}, {K: "isError", V: true}}}}},
				{K: "sourceEventSeqs", V: []any{from}}, {K: "surfaceOp", V: "append"}}, in.Head)
		}
		calls = &orderedCalls{}
		push(core.O{{K: "type", V: "step/end"}, {K: "seq", V: seq()}, {K: "time", V: t}, {K: "data", V: core.O{{K: "turn", V: turn}, {K: "step", V: step}}}}, in.Head)
		push(core.O{{K: "type", V: "turn/end"}, {K: "seq", V: seq()}, {K: "time", V: t}, {K: "data", V: core.O{{K: "turn", V: turn}, {K: "reason", V: core.O{{K: "kind", V: "completed"}}}}}}, in.Head)
		open = false
	}
	begin := func(t float64, eventID string) {
		turn++
		step, open, afterTools = 1, true, false
		push(core.O{{K: "type", V: "turn/start"}, {K: "seq", V: seq()}, {K: "time", V: t}, {K: "data", V: core.O{{K: "turn", V: turn}}}}, eventID)
		push(core.O{{K: "type", V: "step/start"}, {K: "seq", V: seq()}, {K: "time", V: t}, {K: "data", V: core.O{{K: "turn", V: turn}, {K: "step", V: step}}}}, eventID)
		// dsh requires the surface to start with a protected system/message head.
		if !hasSystem {
			push(core.O{{K: "type", V: "system/message"}, {K: "seq", V: seq()}, {K: "time", V: t}, {K: "data", V: core.O{{K: "turn", V: turn}, {K: "step", V: step},
				{K: "message", V: core.O{{K: "role", V: "system"}, {K: "content", V: []any{core.O{{K: "type", V: "text"}, {K: "text", V: systemHead}}}}, {K: "source", V: core.O{{K: "kind", V: "system-prompt"}}}, {K: "id", V: core.UUID5("dsh-system\x00" + t2s(in.Target.Session))}}}}},
				{K: "surfaceOp", V: "append"}}, eventID)
			hasSystem = true
		}
	}
	for _, it := range proj.Items {
		ts := clock.Next(it.Event)
		switch {
		case it.Role == "user":
			closeTurn(ts)
			begin(ts, it.Event.ID)
		case !open:
			begin(ts, it.Event.ID)
		case it.Role == "assistant" && afterTools:
			// the model's next response after tool results is a new step (dsh's own layout)
			push(core.O{{K: "type", V: "step/end"}, {K: "seq", V: seq()}, {K: "time", V: ts}, {K: "data", V: core.O{{K: "turn", V: turn}, {K: "step", V: step}}}}, it.Event.ID)
			step++
			push(core.O{{K: "type", V: "step/start"}, {K: "seq", V: seq()}, {K: "time", V: ts}, {K: "data", V: core.O{{K: "turn", V: turn}, {K: "step", V: step}}}}, it.Event.ID)
			afterTools = false
		}
		for _, r := range renderDsh(it, in, turn, step, seq, ts, calls) {
			push(r, it.Event.ID)
		}
		if it.Role != "user" && it.Role != "assistant" {
			afterTools = true
		}
	}
	if open {
		closeTurn(clock.Next(in.Hub.GetEvent(in.Head)))
	}
	if t.Mode == "append" && len(out) == 0 {
		return noop(), nil
	}
	var body strings.Builder
	records := []engine.PlanRecord{}
	for _, l := range out {
		body.WriteString(l.text)
		body.WriteByte('\n')
		records = append(records, engine.PlanRecord{NativeID: l.native, EventID: l.event, SHA256: core.SHA256Hex(l.text)})
	}
	// dsh's own layout: one zstd frame for the header, then one frame per appended batch
	var frames []byte
	if header != "" {
		frames = append(frames, core.ZstdCompress([]byte(header+"\n"))...)
	}
	frames = append(frames, core.ZstdCompress([]byte(body.String()))...)
	t.Path = file
	return &engine.DeliveryPlan{ID: shared.PlanID(c.id, t.Session, in.Head, t.Mode, core.SHA256Hex(body.String())), Sink: c.id, Harness: "dsh", Thread: in.Thread, Head: in.Head,
		Level: in.Level, Mode: t.Mode, Target: t, Records: records, Content: base64.StdEncoding.EncodeToString(frames), Losses: proj.Losses, Notes: proj.Notes,
		Extra: core.JsonObject{"encoding": "base64", "attachments": attachments}}, nil
}

func t2s(s string) string { return s }

func (c *dshConn) Apply(ctx context.Context, plan *engine.DeliveryPlan) (core.JsonObject, error) {
	if err := shared.ApplyAttachments(plan); err != nil {
		return nil, err
	}
	r, err := shared.ApplyFilePlan(plan)
	if err == nil {
		c.files.Set(plan.Target.Session, plan.Target.Path)
	}
	return r, err
}

// OpenBy: dsh holds its session.lock (and the session file) open while the session is loaded.
func OpenBy(file string) string {
	if file == "" {
		return ""
	}
	for _, f := range []string{filepath.Join(filepath.Dir(file), "session.lock"), file} {
		if _, err := os.Stat(f); err != nil {
			continue
		}
		if h, _ := shared.FileHolders(f); len(h) > 0 {
			var s []string
			for _, p := range h {
				s = append(s, strconv.Itoa(p))
			}
			return fmt.Sprintf("dsh pid %s has %s open", strings.Join(s, ", "), filepath.Base(f))
		}
	}
	return ""
}
