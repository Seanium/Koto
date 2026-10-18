// Package codex is the Codex connector. Storage root: $CODEX_HOME (default ~/.codex), shared by
// the Codex CLI, app, IDE plugin and `codex exec`.
//
// Source: rollout JSONL (authoritative; state_5.sqlite is a projection). Rollouts get zstd-compressed
// after 7 days and may move, so a session is its rollout key `<thread>[_<rollout>]`, never a path.
//
// Sink: new native rollouts registered with the official app-server, preserving multimodal history.
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
)

const Version = "codex-connector@0.2.0"

var shellBin = regexp.MustCompile(`(^|/)(ba|z)?sh$`)

// ToCanonical maps Codex tools to the canonical vocabulary.
func ToCanonical(name string, args any) map[string]any {
	a := core.Obj(args)
	switch name {
	case "exec_command":
		cmd, ok := a["cmd"].(string)
		if !ok {
			if arr, ok := a["cmd"].([]any); ok {
				var ss []string
				for _, x := range arr {
					ss = append(ss, jsStr(x))
				}
				cmd = strings.Join(ss, " ")
			}
		}
		t := map[string]any{"op": "shell", "command": cmd}
		if w := core.S(a["workdir"]); w != "" {
			t["cwd"] = w
		}
		return t
	case "shell", "local_shell":
		var cmd []string
		if arr, ok := a["command"].([]any); ok {
			for _, x := range arr {
				cmd = append(cmd, jsStr(x))
			}
		}
		command := strings.Join(cmd, " ")
		if len(cmd) >= 3 && shellBin.MatchString(cmd[0]) && cmd[1] == "-lc" {
			command = cmd[2]
		}
		t := map[string]any{"op": "shell", "command": command}
		if w := core.S(a["workdir"]); w != "" {
			t["cwd"] = w
		}
		return t
	case "apply_patch":
		if s, ok := args.(string); ok {
			return map[string]any{"op": "patch", "patch": s}
		}
		return map[string]any{"op": "patch", "patch": core.S(a["input"])}
	case "view_image":
		return map[string]any{"op": "read", "path": core.S(a["path"])}
	}
	return map[string]any{"op": "raw"}
}

func jsStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	case float64:
		s, _ := core.ESNumber(x)
		return s
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	return fmt.Sprint(v)
}

// Native historical tool calls retain their original names and arguments; they never re-run.
type Vocabulary struct{}

func (Vocabulary) ToCanonical(name string, args any) map[string]any { return ToCanonical(name, args) }
func (Vocabulary) FromCanonical(t map[string]any) (core.NativeToolCall, bool) {
	return core.NativeToolCall{}, false
}

var vocab = Vocabulary{}

var Capabilities = core.Capabilities{
	Read: core.ReadCaps{Images: true, Reasoning: "sealed", Subagents: true, Compaction: true},
	Write: &core.WriteCaps{Mode: "native-file", Images: true, SameAPI: "openai-responses", Tools: "native",
		ToolCallID: &core.IDRule{Pattern: "^[a-zA-Z0-9_-]+$", MaxLength: 64}},
	WriteProfile: "codex-rollout@1", FormatVersions: []string{"rollout-jsonl"},
}

// ThreadOf: <thread>[_<rollout>] → <thread>, what `codex resume` and the app-server take.
func ThreadOf(session string) string { return strings.SplitN(session, "_", 2)[0] }

var rolloutRe = regexp.MustCompile(`^rollout-\d{4}-\d\d-\d\dT\d\d-\d\d-\d\d-(.+?)\.jsonl(\.zst)?$`)

// RolloutKey: rollout-2026-10-08T10-15-43-<thread>[_<rollout>].jsonl[.zst] → <thread>[_<rollout>].
func RolloutKey(file string) string {
	if m := rolloutRe.FindStringSubmatch(filepath.Base(file)); m != nil {
		return m[1]
	}
	return ""
}

// ------------------------------------------------------------------ decode

var contextTags = []string{"<environment_context>", "<user_instructions>", "<permissions instructions>", "# AGENTS.md instructions", "<INSTRUCTIONS>", "<turn_aborted>", "<user_shell_command>", "<skill", "<heartbeat>"}

var dataURL = regexp.MustCompile(`(?s)^data:([^;]+);base64,(.*)$`)

func contentItems(content any, ctx *engine.DecodeContext) []core.Part {
	if ctx.IndexOnly {
		return indexContentItems(content, assetDirectory(ctx.Ref.Path))
	}
	if s, ok := content.(string); ok {
		if s == "" {
			return []core.Part{}
		}
		return shared.DecodeAttachmentText(s, assetDirectory(ctx.Ref.Path), ctx.Blob)
	}
	out := []core.Part{}
	for _, x := range core.Arr(content) {
		c := core.Obj(x)
		t := core.S(c["type"])
		if t == "input_text" || t == "output_text" || t == "text" {
			if s, ok := c["text"].(string); ok {
				out = append(out, shared.DecodeAttachmentText(s, assetDirectory(ctx.Ref.Path), ctx.Blob)...)
			}
		} else if t == "input_image" || t == "input_audio" {
			field, kind := "image_url", "image"
			if t == "input_audio" {
				field, kind = "audio_url", "file"
			}
			if u, ok := c[field].(string); ok {
				if m := dataURL.FindStringSubmatch(u); m != nil {
					out = append(out, core.Part{"type": kind, "blob": ctx.Blob(core.Base64(m[2])), "mediaType": m[1]})
				} else {
					out = append(out, core.Part{"type": kind, "uri": u, "mediaType": map[string]string{"image": "image/unknown", "file": "audio/unknown"}[kind]})
				}
			} else {
				out = append(out, core.Part{"type": kind, "name": "unavailable " + t})
			}
		}
	}
	return out
}

func parseArgs(v any) any {
	s, ok := v.(string)
	if !ok {
		return v // undefined/null → null
	}
	var x any
	if json.Unmarshal([]byte(s), &x) != nil {
		return s
	}
	return x
}

func strOrNil(v any) any {
	if s, ok := v.(string); ok {
		return s
	}
	return nil
}

// Decoder reads rollout JSONL. State: model, cwd, effort, provider, lastNative, metaKey.
type Decoder struct{}

func (Decoder) FormatVersion() string { return "rollout-jsonl" }
func (Decoder) InitialState() core.JsonObject {
	return core.JsonObject{"model": nil, "cwd": nil, "effort": nil, "provider": nil, "lastNative": nil, "metaKey": nil}
}

func setIf(st core.JsonObject, k string, v any) {
	if s, ok := v.(string); ok {
		st[k] = s
	}
}

func (Decoder) Decode(r0 any, raw []byte, ctx *engine.DecodeContext) engine.DecodeOut {
	r := core.Obj(r0)
	typ, hasType := r["type"].(string)
	p := core.Obj(r["payload"])
	nativeID := fmt.Sprintf("L%d", ctx.Index)
	if o, ok := r["ordinal"].(float64); ok {
		s, _ := core.ESNumber(o)
		nativeID = "o" + s
	}
	var t *float64
	if s, ok := r["timestamp"].(string); ok {
		t = core.ParseTime(s)
	}
	st := ctx.State
	ev := func(kind, actor string, parts []core.Part, attrs core.JsonObject, replaces *core.DraftReplaces) engine.DecodeOut {
		st["lastNative"] = nativeID
		a := core.JsonObject{}
		if c := core.S(st["cwd"]); c != "" {
			a["cwd"] = c
		}
		for k, v := range attrs {
			a[k] = v
		}
		return engine.DecodeOut{NativeID: nativeID, Draft: &core.DraftEvent{NativeID: nativeID, Time: t, Kind: kind, Actor: actor, Parts: parts, Attrs: a, Replaces: replaces}}
	}
	// Codex speaks the Responses wire API; the provider is whatever the user configured.
	model := func() map[string]any {
		return map[string]any{"provider": st["provider"], "api": "openai-responses", "id": st["model"]}
	}
	switch typ {
	case "session_meta":
		setIf(st, "cwd", p["cwd"])
		setIf(st, "provider", p["model_provider"])
		out := engine.DecodeOut{NativeID: fmt.Sprintf("#meta%d", ctx.Index), Meta: core.JsonObject{"cwd": st["cwd"], "threadId": strOrNil(p["id"]), "frontend": strOrNil(p["source"]), "originator": strOrNil(p["originator"]), "cliVersion": strOrNil(p["cli_version"])}}
		identity := core.SessionIdentity{}
		if account := core.S(p["creator_account_id"]); account != "" {
			identity.Claims = append(identity.Claims, core.IdentityClaim{Kind: "account", ID: account, Scope: "creator", Evidence: "Codex session_meta.creator_account_id"})
		}
		if user := core.S(p["creator_user_id"]); user != "" {
			out.Meta["creatorUserId"] = user
		}
		if provider := core.S(p["model_provider"]); provider != "" {
			identity.Providers = append(identity.Providers, core.ProviderIdentity{ID: provider, Scope: "session", Evidence: "Codex session_meta.model_provider"})
		}
		out.Meta["identity"] = core.IdentityMeta(identity)
		st["identity"] = out.Meta["identity"]
		fork := core.S(p["forked_from_id"])
		base := core.Obj(p["history_base"])
		id := core.S(p["id"])
		after := func(v any) string {
			if n, ok := v.(float64); ok {
				s, _ := core.ESNumber(n - 1)
				return "o" + s
			}
			return ""
		}
		switch {
		case fork != "":
			out.Lineage = &core.Lineage{Kind: "fork", Harness: "codex", Session: fork, AfterNative: after(p["forked_from_ordinal_exclusive"])}
		// A rollback (thread/revert) leaves the rollout as it was and starts <thread>_<rollout>.jsonl:
		// its history is the base rollout up to end_ordinal_exclusive, then what this file adds.
		case core.S(base["thread_id"]) != "" && id != "" && strings.HasPrefix(ctx.Ref.Session, id+"_"):
			b := core.S(base["thread_id"])
			s := id
			if b != id {
				s = id + "_" + b
			}
			out.Lineage = &core.Lineage{Kind: "continue", Harness: "codex", Session: s, AfterNative: after(base["end_ordinal_exclusive"])}
		case core.S(base["thread_id"]) != "" && core.S(base["thread_id"]) != id:
			out.Lineage = &core.Lineage{Kind: "continue", Harness: "codex", Session: core.S(base["thread_id"]), AfterNative: after(base["end_ordinal_exclusive"])}
		case core.S(p["parent_thread_id"]) != "":
			out.Lineage = &core.Lineage{Kind: "spawn", Harness: "codex", Session: core.S(p["parent_thread_id"])}
		}
		return out
	case "turn_context":
		setIf(st, "model", p["model"])
		setIf(st, "cwd", p["cwd"])
		setIf(st, "provider", p["model_provider"])
		setIf(st, "effort", p["effort"])
		identity := core.ReadIdentity(core.JsonObject{"identity": st["identity"]})
		provider, modelID := core.S(st["provider"]), core.S(st["model"])
		found := false
		for _, v := range identity.Providers {
			if v.ID == provider && v.Model == modelID && v.Scope == "message" {
				found = true
				break
			}
		}
		if !found && (provider != "" || modelID != "") {
			identity.Providers = append(identity.Providers, core.ProviderIdentity{ID: provider, Model: modelID, Scope: "message", Evidence: "Codex turn_context.model / model_provider"})
		}
		st["identity"] = core.IdentityMeta(identity)
		key := metaKey(st)
		if st["metaKey"] == key {
			return engine.DecodeOut{NativeID: fmt.Sprintf("#tc%d", ctx.Index)}
		}
		st["metaKey"] = key
		out := ev("meta", "harness", []core.Part{}, core.JsonObject{"model": model(), "effort": st["effort"]}, nil)
		out.Meta = core.JsonObject{"identity": st["identity"]}
		return out
	case "compacted":
		if ctx.IndexOnly {
			var rep *core.DraftReplaces
			if prev := core.S(st["lastNative"]); prev != "" {
				rep = &core.DraftReplaces{To: core.NativeParent(prev)}
			}
			return ev("compaction", "harness", nil, nil, rep)
		}
		var texts []string
		if m := core.S(p["message"]); m != "" {
			texts = append(texts, m)
		}
		for _, h := range core.Arr(p["replacement_history"]) {
			hm := core.Obj(h)
			if hm["type"] != "message" {
				continue
			}
			for _, x := range contentItems(hm["content"], ctx) {
				if x["type"] == "text" && core.S(x["text"]) != "" {
					texts = append(texts, core.S(x["text"]))
				}
			}
		}
		prev := core.S(st["lastNative"])
		var rep *core.DraftReplaces
		if prev != "" {
			rep = &core.DraftReplaces{To: core.NativeParent(prev)}
		}
		return ev("compaction", "harness", []core.Part{{"type": "text", "text": strings.Join(texts, "\n\n")}}, nil, rep)
	case "response_item":
		return decodeResponseItem(p, ev, model, ctx)
	}
	// event_msg (UI lifecycle, duplicates items), world_state, token usage, inter-agent metadata…
	if !hasType {
		typ = "x"
	}
	return engine.DecodeOut{NativeID: fmt.Sprintf("#%s%d", typ, ctx.Index)}
}

// metaKey includes provider: switching endpoints with the same model is still a context change.
func metaKey(st core.JsonObject) string {
	f := func(v any) string {
		if v == nil {
			return "null"
		}
		return jsStr(v)
	}
	return f(st["model"]) + "\x00" + f(st["cwd"]) + "\x00" + f(st["effort"]) + "\x00" + f(st["provider"])
}

type evFn func(kind, actor string, parts []core.Part, attrs core.JsonObject, replaces *core.DraftReplaces) engine.DecodeOut

func decodeResponseItem(p map[string]any, ev evFn, model func() map[string]any, ctx *engine.DecodeContext) engine.DecodeOut {
	t, _ := p["type"].(string)
	if ctx.IndexOnly {
		if out, ok := indexResponseItem(p, t, ev, model); ok {
			return out
		}
	}
	switch t {
	case "message":
		role, hasRole := p["role"].(string)
		parts := contentItems(p["content"], ctx)
		if role == "assistant" {
			a := core.JsonObject{"model": model()}
			if ph := core.S(p["phase"]); ph != "" {
				a["phase"] = ph
			}
			return ev("message", "assistant", parts, a, nil)
		}
		if role == "user" {
			kinds, isArr := core.Obj(p["internal_chat_message_metadata_passthrough"])["content_item_kinds"].([]any)
			byKind := false
			if isArr && len(kinds) > 0 {
				byKind = true
				for _, k := range kinds {
					if s, ok := k.(string); ok && strings.HasPrefix(s, "user.") {
						byKind = false
					}
				}
			}
			byTag := false
			if !isArr {
				for _, x := range parts {
					if x["type"] == "text" {
						txt := strings.TrimLeftFunc(core.S(x["text"]), func(r rune) bool { return unicode.IsSpace(r) || r == 0xfeff })
						for _, tag := range contextTags {
							if strings.HasPrefix(txt, tag) {
								byTag = true
							}
						}
						break
					}
				}
			}
			kind := "message"
			if byKind || byTag {
				kind = "context"
			}
			return ev(kind, "user", parts, nil, nil)
		}
		var rv any = nil
		if hasRole {
			rv = role
		}
		return ev("context", "system", parts, core.JsonObject{"role": rv}, nil)
	case "reasoning":
		var texts []string
		for _, s := range core.Arr(p["summary"]) {
			if x := core.S(core.Obj(s)["text"]); x != "" {
				texts = append(texts, x)
			}
		}
		for _, s := range core.Arr(p["content"]) {
			if x := core.S(core.Obj(s)["text"]); x != "" {
				texts = append(texts, x)
			}
		}
		part := core.Part{"type": "reasoning"}
		if text := strings.Join(texts, "\n\n"); text != "" {
			part["text"] = text
		}
		if enc := core.S(p["encrypted_content"]); enc != "" {
			part["sealed"] = map[string]any{"by": "openai-responses", "blob": ctx.Blob([]byte(enc))}
		}
		return ev("reasoning", "assistant", []core.Part{part}, core.JsonObject{"model": model()}, nil)
	case "function_call", "custom_tool_call":
		name := core.S(p["name"])
		var args any
		if t == "function_call" {
			args = parseArgs(p["arguments"])
		} else {
			args = p["input"]
			if args == nil {
				args = ""
			}
		}
		return ev("tool_call", "assistant", []core.Part{{"type": "tool_call", "callId": core.S(p["call_id"]), "rawName": name, "args": args, "tool": ToCanonical(name, args)}}, core.JsonObject{"model": model(), "codexType": t}, nil)
	case "local_shell_call":
		action := core.Obj(p["action"])
		cmd := action["command"]
		if cmd == nil {
			cmd = []any{}
		}
		args := map[string]any{"command": cmd, "workdir": action["working_directory"]}
		callID, ok := p["call_id"].(string)
		if !ok {
			callID = core.S(p["id"])
		}
		return ev("tool_call", "assistant", []core.Part{{"type": "tool_call", "callId": callID, "rawName": "local_shell", "args": args, "tool": ToCanonical("local_shell", args)}}, core.JsonObject{"model": model(), "codexType": t}, nil)
	case "function_call_output", "custom_tool_call_output", "local_shell_call_output":
		out := p["output"]
		var parts []core.Part
		if s, ok := out.(string); ok {
			parts = []core.Part{{"type": "text", "text": s}}
		} else {
			parts = contentItems(out, ctx)
		}
		outcome := "success"
		if core.Obj(out)["success"] == false {
			outcome = "error"
		}
		return ev("tool_result", "tool", []core.Part{{"type": "tool_result", "callId": core.S(p["call_id"]), "outcome": outcome, "parts": core.PartsJSON(parts)}}, nil, nil)
	case "web_search_call":
		a := core.Obj(p["action"])
		q, ok := a["query"].(string)
		if !ok {
			if arr, ok := a["queries"].([]any); ok {
				var ss []string
				for _, x := range arr {
					ss = append(ss, jsStr(x))
				}
				q = strings.Join(ss, " | ")
			}
		}
		return ev("message", "assistant", []core.Part{{"type": "text", "text": "[web search: " + q + "]"}}, core.JsonObject{"model": model(), "codexType": t}, nil)
	case "tool_search_call", "tool_search_output":
		return ev("context", "harness", []core.Part{}, core.JsonObject{"codexType": t}, nil)
	case "agent_message":
		return ev("x-codex-agent-message", "harness", contentItems(p["content"], ctx), core.JsonObject{"codexType": t}, nil)
	}
	tt := t
	if _, ok := p["type"].(string); !ok {
		tt = jsStr(p["type"])
		if p["type"] == nil {
			tt = "undefined"
		}
	}
	return ev("message", "harness", []core.Part{{"type": "opaque", "blob": ctx.Blob([]byte(core.Stringify(p))), "semantic": "codex:" + tt, "reason": "unmapped response item"}}, nil, nil)
}

// ------------------------------------------------------------------ connector

// Options for the connector.
type Options struct {
	Home string // $CODEX_HOME
	// Shadow is the isolated HOME used by the app-server; CODEX_HOME is the target root.
	Shadow string
	Bin    string
	ID     string
}

type codexConn struct {
	id, home, shadow, bin string
	files                 *shared.PathIndex
	mu                    sync.Mutex
	server                *AppServer
	projects              projectCache
}

// New makes a Codex connector.
func New(o Options) *engine.Connector {
	if o.ID == "" {
		o.ID = "codex"
	}
	if o.Bin == "" {
		o.Bin = "codex"
	}
	c := &codexConn{id: o.ID, home: o.Home, shadow: o.Shadow, bin: o.Bin, files: shared.NewPathIndex()}
	return &engine.Connector{ID: c.id, Root: c.home, Harness: "codex", Version: Version, Capabilities: Capabilities, Vocabulary: vocab, Source: c, Sink: c,
		Dispose: func() {
			c.mu.Lock()
			s := c.server
			c.mu.Unlock()
			if s != nil {
				s.Stop()
			}
		}}
}

var rolloutFile = regexp.MustCompile(`^rollout-.*\.jsonl(\.zst)?$`)

func walk(dir string, fn func(string)) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			walk(p, fn)
		} else if rolloutFile.MatchString(e.Name()) {
			fn(p)
		}
	}
}

func (c *codexConn) Discover(ctx context.Context) ([]core.NativeRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	seen := map[string]string{}
	var order []string
	for _, root := range []string{filepath.Join(c.home, "sessions"), filepath.Join(c.home, "archived_sessions")} {
		walk(root, func(f string) {
			key := RolloutKey(f)
			if key == "" {
				return
			}
			// During compression both containers can exist briefly: prefer the plain file.
			if prev, ok := seen[key]; ok && strings.HasSuffix(prev, ".jsonl") && strings.HasSuffix(f, ".zst") {
				return
			}
			if _, ok := seen[key]; !ok {
				order = append(order, key)
			}
			seen[key] = f
		})
	}
	names := c.threadNames()
	locations := c.currentLocations(ctx)
	// After a rollback the thread goes on in its newest rollout; the older ones keep the branches it left.
	type nk struct{ key, at string }
	newest := map[string]nk{}
	for _, s := range order {
		t, at := ThreadOf(s), filepath.Base(seen[s])
		if prev, ok := newest[t]; !ok || at > prev.at {
			newest[t] = nk{s, at}
		}
	}
	var out []core.NativeRef
	for _, s := range order {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f := seen[s]
		c.files.Set(s, f)
		st, err := os.Stat(f)
		if err != nil {
			continue
		}
		meta := core.JsonObject{}
		// Null clears a previous assignment when the user detaches a project.
		meta["currentCwd"], meta["currentProject"] = nil, nil
		if location, ok := locations[ThreadOf(s)]; ok {
			meta["currentCwd"] = location.cwd
			if location.project != nil {
				meta["currentProject"] = location.project
			}
		}
		if title, ok := names[ThreadOf(s)]; ok {
			meta["title"] = title
		}
		if cur := newest[ThreadOf(s)].key; cur == s {
			meta["supersededBy"] = nil
		} else {
			meta["supersededBy"] = cur
		}
		out = append(out, core.NativeRef{Harness: "codex", Connector: c.id, Session: s, Path: f, Size: st.Size(), MtimeMs: engine.MtimeMs(st), Meta: meta})
	}
	return out, nil
}

// threadNames are the names the user gave in Codex ($CODEX_HOME/session_index.jsonl, newest entry per thread).
func (c *codexConn) threadNames() map[string]string {
	b, err := os.ReadFile(filepath.Join(c.home, "session_index.jsonl"))
	if err != nil {
		return nil
	}
	type na struct{ name, at string }
	best := map[string]na{}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		var r map[string]any
		if json.Unmarshal([]byte(l), &r) != nil {
			continue // a torn last line while Codex writes
		}
		id, ok1 := r["id"].(string)
		name, ok2 := r["thread_name"].(string)
		if !ok1 || !ok2 || strings.TrimSpace(name) == "" {
			continue
		}
		at, _ := r["updated_at"].(string)
		if prev, ok := best[id]; !ok || at >= prev.at {
			best[id] = na{strings.TrimSpace(name), at}
		}
	}
	out := map[string]string{}
	for k, v := range best {
		out[k] = v.name
	}
	return out
}

func (c *codexConn) Tail(ctx context.Context, ref core.NativeRef, from *core.SourceOffset, yield func(core.SourceBatch) error) error {
	return engine.TailJSONL(ctx, ref, from, Decoder{}, yield, 0, false)
}

func (c *codexConn) WatchRoots() []string {
	return []string{filepath.Join(c.home, "sessions"), filepath.Join(c.home, "session_index.jsonl"), filepath.Join(c.home, ".codex-global-state.json"), filepath.Join(c.home, "state_5.sqlite"), filepath.Join(c.home, "state_5.sqlite-wal")}
}

func (c *codexConn) ReadRecord(session string, index int, offset *int64) ([]byte, error) {
	return c.ReadRecordContext(context.Background(), session, index, offset)
}
func (c *codexConn) ReadRecordContext(ctx context.Context, session string, index int, offset *int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, ok := c.files.Get(session)
	if !ok {
		if _, err := c.Discover(ctx); err != nil {
			return nil, err
		}
		p, ok = c.files.Get(session)
	}
	if !ok || offset == nil {
		return nil, nil
	}
	return engine.ReadRecordAtContext(ctx, p, *offset)
}

func (c *codexConn) findRollout(key string) string {
	found := ""
	for _, root := range []string{filepath.Join(c.home, "sessions"), filepath.Join(c.home, "archived_sessions")} {
		walk(root, func(f string) {
			if found == "" && RolloutKey(f) == key {
				found = f
			}
		})
		if found != "" {
			return found
		}
	}
	return ""
}

func (c *codexConn) ensureServer() *AppServer {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.server == nil {
		c.server = &AppServer{Bin: c.bin, IdleMs: 1000, Env: map[string]string{"HOME": c.shadow, "CODEX_HOME": c.home}}
	}
	return c.server
}

var toolNote = regexp.MustCompile(`\[external_agent_tool_(call|result)[^\]]*\][\s\S]*?\[/external_agent_tool_(call|result)\]`)
var userQuery = regexp.MustCompile(`(?s)^<user_query>(.*)</user_query>$`)
