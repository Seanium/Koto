// Package claudecode is the Claude Code connector. Storage root: $CLAUDE_CONFIG_DIR (default
// ~/.claude); sessions are projects/<cwd-encoded>/<sessionId>.jsonl, large tool outputs in
// <sessionId>/tool-results/. The CLI and Desktop's Code tab share this root; each Cowork session
// directory is a root of its own (another instance with another id).
//
// Reader: tolerant of every record type seen in 2.1.263–2.1.300; unknown types become aliases (no
// event), so the tree still resolves. Writer: profile cc-write@1, only fields every version has.
package claudecode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/planner"
)

const (
	Version      = "claude-code-connector@0.2.0"
	WriteVersion = "2.1.300"
)

// VerifiedVersions: CLI versions whose records were checked against the write profile.
var VerifiedVersions = []string{"2.1.263", "2.1.287", "2.1.289", "2.1.291", "2.1.295", "2.1.296", "2.1.300"}

// Vocabulary maps Claude Code tools to the canonical vocabulary and back.
type Vocabulary struct{}

func optStr(m map[string]any, k string) (string, bool) { s, ok := m[k].(string); return s, ok }

func (Vocabulary) ToCanonical(name string, args any) map[string]any {
	a := core.Obj(args)
	path := core.S(a["file_path"])
	switch name {
	case "Read":
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
	case "Write":
		t := map[string]any{"op": "write", "path": path}
		if c, ok := optStr(a, "content"); ok {
			t["content"] = c
		}
		return t
	case "Edit":
		return map[string]any{"op": "edit", "path": path, "edits": []any{map[string]any{"old": core.S(a["old_string"]), "new": core.S(a["new_string"])}}}
	case "MultiEdit":
		var edits []any
		for _, e := range core.Arr(a["edits"]) {
			em := core.Obj(e)
			edits = append(edits, map[string]any{"old": core.S(em["old_string"]), "new": core.S(em["new_string"])})
		}
		if edits == nil {
			edits = []any{}
		}
		return map[string]any{"op": "edit", "path": path, "edits": edits}
	case "Bash":
		return map[string]any{"op": "shell", "command": core.S(a["command"])}
	case "Grep":
		t := map[string]any{"op": "search", "query": core.S(a["pattern"])}
		if p := core.S(a["path"]); p != "" {
			t["scope"] = p
		}
		return t
	case "Glob":
		pat, ok := optStr(a, "pattern")
		if !ok {
			pat = "*"
		}
		t := map[string]any{"op": "list", "pattern": pat}
		if p := core.S(a["path"]); p != "" {
			t["path"] = p
		}
		return t
	case "LS":
		t := map[string]any{"op": "list"}
		if p := core.S(a["path"]); p != "" {
			t["path"] = p
		}
		return t
	case "WebFetch":
		return map[string]any{"op": "web", "url": core.S(a["url"])}
	case "WebSearch":
		return map[string]any{"op": "web", "query": core.S(a["query"])}
	}
	return map[string]any{"op": "raw"}
}

func (v Vocabulary) FromCanonical(t map[string]any) (core.NativeToolCall, bool) {
	switch core.S(t["op"]) {
	case "read":
		args := core.O{{K: "file_path", V: core.S(t["path"])}}
		if off, lim := core.OffsetLimitFromRange(core.Arr(t["range"])); off != nil {
			args = append(args, core.KV{K: "offset", V: *off})
			if lim != nil {
				args = append(args, core.KV{K: "limit", V: *lim})
			}
		}
		return core.NativeToolCall{Name: "Read", Args: oToMap(args)}, true
	case "write":
		c, ok := t["content"].(string)
		if !ok {
			return core.NativeToolCall{}, false
		}
		return core.NativeToolCall{Name: "Write", Args: map[string]any{"file_path": core.S(t["path"]), "content": c}}, true
	case "edit":
		edits := core.Arr(t["edits"])
		if len(edits) != 1 {
			return core.NativeToolCall{}, false
		}
		e := core.Obj(edits[0])
		return core.NativeToolCall{Name: "Edit", Args: map[string]any{"file_path": core.S(t["path"]), "old_string": core.S(e["old"]), "new_string": core.S(e["new"])}}, true
	case "shell":
		return core.NativeToolCall{Name: "Bash", Args: map[string]any{"command": core.S(t["command"])}}, true
	case "search":
		a := map[string]any{"pattern": core.S(t["query"])}
		if s := core.S(t["scope"]); s != "" {
			a["path"] = s
		}
		return core.NativeToolCall{Name: "Grep", Args: a}, true
	case "list":
		pat, ok := t["pattern"].(string)
		if !ok {
			pat = "*"
		}
		a := map[string]any{"pattern": pat}
		if p := core.S(t["path"]); p != "" {
			a["path"] = p
		}
		return core.NativeToolCall{Name: "Glob", Args: a}, true
	case "web":
		if u := core.S(t["url"]); u != "" {
			return core.NativeToolCall{Name: "WebFetch", Args: map[string]any{"url": u, "prompt": ""}}, true
		}
		return core.NativeToolCall{Name: "WebSearch", Args: map[string]any{"query": core.S(t["query"])}}, true
	case "patch":
		if e := core.PatchAsEdit(core.S(t["patch"])); e != nil {
			return v.FromCanonical(e)
		}
	}
	return core.NativeToolCall{}, false
}

func oToMap(o core.O) map[string]any {
	m := map[string]any{}
	for _, kv := range o {
		m[kv.K] = kv.V
	}
	return m
}

// Capabilities of the Claude Code connector.
var Capabilities = core.Capabilities{
	Read: core.ReadCaps{Images: true, Reasoning: "sealed", Subagents: true, Compaction: true},
	Write: &core.WriteCaps{Mode: "native-file", Images: true, SameAPI: "anthropic-messages", Tools: "canonical", Tree: true,
		ToolCallID: &core.IDRule{Pattern: "^[a-zA-Z0-9_-]+$", MaxLength: 64}},
	WriteProfile: "cc-write@1", FormatVersions: VerifiedVersions,
}

// MaxSideFile is the largest persisted tool output followed into the working copy.
const MaxSideFile = 64 * 1024 * 1024

// OwnSideFile: p (read from the session file, so untrusted) is a regular file inside dir once
// symlinks and .. are resolved, and not larger than MaxSideFile.
func OwnSideFile(dir, p string) bool {
	if !filepath.IsAbs(p) {
		return false
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return false
	}
	base, err := filepath.EvalSymlinks(dir)
	if err != nil || !strings.HasPrefix(real, base+string(filepath.Separator)) {
		return false
	}
	st, err := os.Stat(real)
	return err == nil && st.Mode().IsRegular() && st.Size() <= MaxSideFile
}

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]`)

// ProjectDir is Claude Code's project dir name: every non-alphanumeric character becomes '-'.
func ProjectDir(cwd string) string {
	if s := nonAlnum.ReplaceAllString(cwd, "-"); s != "" {
		return s
	}
	return "-"
}

// ------------------------------------------------------------------ decode

var contextPrefixes = []string{"<command-name>", "<command-message>", "<command-args>", "<local-command-stdout>", "<local-command-stderr>", "<local-command-caveat>", "<system-reminder>"}

var savedTo = regexp.MustCompile(`Full output saved to: (\S+/tool-results/\S+)`)

var vocab = Vocabulary{}

func blocks(content any, ctx *engine.DecodeContext, record map[string]any) []core.Part {
	if ctx.IndexOnly {
		return indexBlocks(content, filepath.Join(strings.TrimSuffix(ctx.Ref.Path, ".jsonl"), "attachments"))
	}
	if s, ok := content.(string); ok {
		if s == "" {
			return []core.Part{}
		}
		return shared.DecodeAttachmentText(s, filepath.Join(strings.TrimSuffix(ctx.Ref.Path, ".jsonl"), "attachments"), ctx.Blob)
	}
	out := []core.Part{}
	for _, x := range core.Arr(content) {
		b := core.Obj(x)
		switch b["type"] {
		case "text":
			if t, ok := b["text"].(string); ok {
				out = append(out, shared.DecodeAttachmentText(t, filepath.Join(strings.TrimSuffix(ctx.Ref.Path, ".jsonl"), "attachments"), ctx.Blob)...)
			}
		case "image":
			src := core.Obj(b["source"])
			if src["type"] == "base64" {
				if d, ok := src["data"].(string); ok {
					mt, ok := src["media_type"].(string)
					if !ok {
						mt = "image/png"
					}
					out = append(out, core.Part{"type": "image", "blob": ctx.Blob(core.Base64(d)), "mediaType": mt})
				}
			} else if u := core.S(src["url"]); u != "" {
				out = append(out, core.Part{"type": "image", "uri": u, "mediaType": core.S(src["media_type"])})
			}
		case "document":
			src := core.Obj(b["source"])
			p := core.Part{"type": "file", "document": true, "name": core.S(b["title"]), "mediaType": core.S(src["media_type"])}
			switch core.S(src["type"]) {
			case "base64":
				p["blob"] = ctx.Blob(core.Base64(core.S(src["data"])))
			case "text":
				p["blob"] = ctx.Blob([]byte(core.S(src["data"])))
				p["mediaType"] = "text/plain"
			case "url":
				p["uri"] = core.S(src["url"])
			default:
				out = append(out, core.Part{"type": "opaque", "blob": ctx.Blob([]byte(core.Stringify(b))), "semantic": "cc:document", "reason": "unsupported document source"})
				continue
			}
			out = append(out, p)
		case "thinking":
			p := core.Part{"type": "reasoning"}
			if s := core.S(b["thinking"]); s != "" {
				p["text"] = s
			}
			if s := core.S(b["signature"]); s != "" {
				p["signature"] = s
			}
			out = append(out, p)
		case "redacted_thinking":
			if d, ok := b["data"].(string); ok {
				out = append(out, core.Part{"type": "reasoning", "sealed": map[string]any{"by": "anthropic-messages", "blob": ctx.Blob([]byte(d))}})
			}
		case "tool_use":
			name := core.S(b["name"])
			args := b["input"]
			if args == nil {
				args = map[string]any{}
			}
			out = append(out, core.Part{"type": "tool_call", "callId": core.S(b["id"]), "rawName": name, "args": args, "tool": vocab.ToCanonical(name, args)})
		case "tool_result":
			inner := blocks(b["content"], ctx, record)
			// Large outputs are persisted next to the session; follow the reference.
			tur := core.Obj(record["toolUseResult"])
			persisted, _ := tur["persistedOutputPath"].(string)
			if persisted == "" {
				for _, p := range inner {
					if p["type"] == "text" {
						if m := savedTo.FindStringSubmatch(core.S(p["text"])); m != nil {
							persisted = m[1]
						}
						break
					}
				}
			}
			// Only into this session's own tool-results/: never arbitrary paths that appear in text.
			alreadyEmbedded := false
			for _, p := range inner {
				if p["type"] == "file" && core.S(p["name"]) == filepath.Base(persisted) {
					alreadyEmbedded = true
				}
			}
			if persisted != "" && ctx.Ref.Path != "" && !alreadyEmbedded {
				var data []byte
				ok := false
				if OwnSideFile(filepath.Join(filepath.Dir(ctx.Ref.Path), ctx.Ref.Session, "tool-results"), persisted) {
					data, ok = ctx.Side(persisted)
				}
				if ok {
					inner = append(inner, core.Part{"type": "file", "blob": ctx.Blob(data), "mediaType": "text/plain", "name": filepath.Base(persisted)})
				} else {
					inner = append(inner, core.Part{"type": "file", "mediaType": "text/plain", "name": filepath.Base(persisted)})
				}
			}
			outcome := "success"
			if b["is_error"] == true {
				outcome = "error"
			}
			out = append(out, core.Part{"type": "tool_result", "callId": core.S(b["tool_use_id"]), "outcome": outcome, "parts": core.PartsJSON(inner)})
		default:
			// server tools, documents, future block types: opaque, so nothing is silently lost
			out = append(out, core.Part{"type": "opaque", "blob": ctx.Blob([]byte(core.Stringify(b))), "semantic": "cc:" + jsString(b["type"]), "reason": "unmapped content block"})
		}
	}
	return out
}

func jsString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return "undefined"
	}
	return fmt.Sprint(v)
}

// Decoder reads Claude Code JSONL. State: cwd; turn = native ids since the last genuine prompt;
// tip = last event-producing record. Claude Code parents a parallel tool result on its tool_use
// record (a sibling of the next tool_use); inside a turn it is re-parented onto the tip.
type Decoder struct{}

func (Decoder) FormatVersion() string { return "cc-jsonl" }
func (Decoder) InitialState() core.JsonObject {
	return core.JsonObject{"cwd": nil, "turn": []any{}, "tip": nil}
}

func (Decoder) Decode(r0 any, raw []byte, ctx *engine.DecodeContext) engine.DecodeOut {
	r := core.Obj(r0)
	cwd, hasCwd := r["cwd"].(string)
	prevCwd, _ := ctx.State["cwd"].(string)
	changed := hasCwd && (ctx.State["cwd"] == nil || cwd != prevCwd)
	out := linearize(decodeCC(r, ctx), r, ctx.State)
	if changed {
		if out.Meta == nil {
			out.Meta = core.JsonObject{}
		}
		out.Meta["cwd"] = cwd
	}
	// Written from Claude Desktop's Code tab (also when Desktop's own index of it is gone).
	if r["entrypoint"] == "claude-desktop" && ctx.State["desktop"] == nil {
		ctx.State["desktop"] = true
		if out.Meta == nil {
			out.Meta = core.JsonObject{}
		}
		out.Meta["frontend"] = "claude-desktop"
	}
	if model := core.S(core.Obj(r["message"])["model"]); model != "" && model != "<synthetic>" {
		var identity core.SessionIdentity
		if saved := core.Obj(ctx.State["identity"]); len(saved) > 0 {
			identity = core.ReadIdentity(core.JsonObject{"identity": saved})
		} else {
			identity = core.ReadIdentity(ctx.Ref.Meta)
		}
		found := false
		for _, p := range identity.Providers {
			if p.Model == model && p.Scope == "message" {
				found = true
				break
			}
		}
		if !found {
			identity.Providers = append(identity.Providers, core.ProviderIdentity{Model: model, Scope: "message", Evidence: "Claude transcript message.model"})
		}
		ctx.State["identity"] = core.IdentityMeta(identity)
		if out.Meta == nil {
			out.Meta = core.JsonObject{}
		}
		out.Meta["identity"] = ctx.State["identity"]
	}
	// /branch and --fork-session copy the chain with the same uuids and point each copy at its original.
	ff := core.Obj(r["forkedFrom"])
	if s := core.S(ff["sessionId"]); s != "" && s != core.S(r["sessionId"]) {
		out.Lineage = &core.Lineage{Kind: "fork", Harness: "claude-code", Session: s, Copied: true}
	}
	return out
}

func strList(v any) []string {
	var out []string
	for _, x := range core.Arr(v) {
		out = append(out, core.S(x))
	}
	return out
}

func linearize(out engine.DecodeOut, r map[string]any, st core.JsonObject) engine.DecodeOut {
	if r["isSidechain"] == true || core.S(r["uuid"]) == "" {
		return out
	}
	turn := strList(st["turn"])
	tip, _ := st["tip"].(string)
	if d := out.Draft; d != nil {
		prompt := d.Kind == "message" && d.Actor == "user"
		p := d.Parent
		if prompt {
			turn = nil
		} else if p != nil && !p.Null && p.Native != "" && p.Event == "" && tip != "" && p.Native != tip && containsS(turn, p.Native) {
			d.Parent = core.NativeParent(tip)
		}
		turn = append(turn, d.NativeID)
		st["tip"] = d.NativeID
	} else if out.HasAlias {
		turn = append(turn, out.NativeID)
	}
	// Parallel results point at recent tool_use records: a short window suffices.
	if len(turn) > 64 {
		turn = turn[len(turn)-64:]
	}
	a := make([]any, len(turn))
	for i, x := range turn {
		a[i] = x
	}
	st["turn"] = a
	return out
}

func containsS(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func decodeCC(r map[string]any, ctx *engine.DecodeContext) engine.DecodeOut {
	typ := core.S(r["type"])
	uuid := core.S(r["uuid"])
	nativeID := uuid
	if _, ok := r["uuid"].(string); !ok {
		nativeID = "#" + typ + strconv.Itoa(ctx.Index)
	}
	var parent *core.ParentRef
	hasParent := false
	if pu, ok := r["parentUuid"]; ok && pu == nil {
		parent, hasParent = core.NullParent(), true
	} else if s := core.S(r["parentUuid"]); s != "" {
		parent, hasParent = core.NativeParent(s), true
	}
	var t *float64
	if s, ok := r["timestamp"].(string); ok {
		t = core.ParseTime(s)
	}
	attrs := core.JsonObject{}
	if s, ok := r["cwd"].(string); ok {
		attrs["cwd"] = s
		ctx.State["cwd"] = s
	}
	if s, ok := r["gitBranch"].(string); ok {
		attrs["gitBranch"] = s
	}
	if s, ok := r["version"].(string); ok {
		attrs["ccVersion"] = s
	}
	if s, ok := r["entrypoint"].(string); ok {
		attrs["entrypoint"] = s
	}
	if r["isSidechain"] == true {
		attrs["sidechain"] = true
	}
	draft := func(kind, actor string, parts []core.Part, extra core.JsonObject, replaces *core.DraftReplaces) engine.DecodeOut {
		a := core.JsonObject{}
		for k, v := range attrs {
			a[k] = v
		}
		for k, v := range extra {
			a[k] = v
		}
		d := &core.DraftEvent{NativeID: nativeID, Kind: kind, Actor: actor, Parts: parts, Time: t, Attrs: a, Replaces: replaces}
		if hasParent {
			d.Parent = parent
		}
		return engine.DecodeOut{NativeID: nativeID, Draft: d}
	}
	alias := func(p *core.ParentRef, has bool) engine.DecodeOut {
		return engine.DecodeOut{NativeID: nativeID, Alias: p, HasAlias: has}
	}
	switch typ {
	case "user":
		m := core.Obj(r["message"])
		parts := blocks(m["content"], ctx, r)
		for _, p := range parts {
			if p["type"] == "tool_result" {
				return draft("tool_result", "tool", parts, nil, nil)
			}
		}
		if r["isCompactSummary"] == true {
			var rep *core.DraftReplaces
			if hasParent && !parent.Null {
				rep = &core.DraftReplaces{To: parent}
			}
			return draft("compaction", "harness", parts, nil, rep)
		}
		// Async task results are delivered to the parent as native user records. They are
		// conversation content, not environment context or a new human prompt. Keep only
		// the delivered payload; output-file links do not authorize reading the child log.
		if core.S(core.Obj(r["origin"])["kind"]) == "task-notification" || r["turnOrigin"] == "task_notification" {
			return draft("notification", "user", parts, nil, nil)
		}
		if r["isMeta"] == true {
			return draft("context", "user", parts, nil, nil)
		}
		clean := []core.Part{}
		stripped := false
		for _, part := range parts {
			if core.S(part["type"]) != "text" {
				clean = append(clean, part)
				continue
			}
			text := core.S(part["text"])
			for {
				matched := false
				for _, prefix := range contextPrefixes {
					trimmed := strings.TrimSpace(text)
					if !strings.HasPrefix(trimmed, prefix) {
						continue
					}
					endTag := "</" + strings.TrimSuffix(strings.TrimPrefix(prefix, "<"), ">") + ">"
					end := strings.Index(trimmed, endTag)
					if end < 0 {
						continue
					} // An incomplete or quoted block is user text; never silently discard it.
					text = strings.TrimSpace(trimmed[end+len(endTag):])
					stripped = true
					matched = true
					break
				}
				if !matched {
					break
				}
			}
			if text != "" {
				clean = append(clean, core.Part{"type": "text", "text": text})
			}
		}
		if stripped {
			if len(clean) == 0 {
				return draft("context", "user", parts, nil, nil)
			}
			return draft("message", "user", clean, core.JsonObject{"filteredContext": true}, nil)
		}
		return draft("message", "user", parts, nil, nil)
	case "assistant":
		m := core.Obj(r["message"])
		model, hasModel := m["model"].(string)
		var stop any = nil
		if r["isApiErrorMessage"] == true || model == "<synthetic>" {
			stop = "error"
		} else if s, ok := m["stop_reason"].(string); ok {
			stop = s
		}
		var modelID any = nil
		if hasModel {
			modelID = model
		}
		var msgID any = nil
		if s, ok := m["id"].(string); ok {
			msgID = s
		}
		extra := core.JsonObject{"model": map[string]any{"provider": nil, "api": "anthropic-messages", "id": modelID}, "messageId": msgID, "stopReason": stop}
		if u, ok := m["usage"]; ok && u != nil && u != false && u != "" {
			extra["usage"] = u
		}
		if ctx.IndexOnly {
			return draft("message", "assistant", nil, extra, nil)
		}
		return draft("message", "assistant", blocks(m["content"], ctx, r), extra, nil)
	case "attachment":
		if ctx.IndexOnly {
			return draft("context", "harness", nil, nil, nil)
		}
		var rendered []string
		hasRendered := false
		if arr, ok := r["rendered"].([]any); ok {
			hasRendered = true
			for _, x := range arr {
				rendered = append(rendered, core.S(core.Obj(x)["content"]))
			}
		}
		text := strings.Join(rendered, "\n")
		parts := []core.Part{}
		if hasRendered && text != "" {
			parts = []core.Part{{"type": "text", "text": text}}
		}
		var at any = nil
		if s, ok := core.Obj(r["attachment"])["type"].(string); ok {
			at = s
		}
		return draft("context", "harness", parts, core.JsonObject{"attachment": at}, nil)
	case "system":
		// compact_boundary starts a new root in CC's tree; alias it to the logical parent.
		if r["subtype"] == "compact_boundary" && core.S(r["logicalParentUuid"]) != "" {
			return alias(core.NativeParent(core.S(r["logicalParentUuid"])), true)
		}
		return alias(parent, hasParent)
	case "relocated":
		return engine.DecodeOut{NativeID: nativeID, Meta: core.JsonObject{"cwd": strOrNil(r["relocatedCwd"])}}
	case "custom-title":
		return engine.DecodeOut{NativeID: nativeID, Meta: core.JsonObject{"title": strOrNil(r["customTitle"])}}
	case "ai-title":
		return engine.DecodeOut{NativeID: nativeID, Meta: core.JsonObject{"aiTitle": strOrNil(r["aiTitle"])}}
	case "summary":
		return engine.DecodeOut{NativeID: nativeID, Meta: core.JsonObject{"summary": strOrNil(r["summary"])}}
	case "bridge-session":
		// cse_… shares the Projects session namespace: lets a thread be looked up.
		return engine.DecodeOut{NativeID: nativeID, Meta: core.JsonObject{"bridgeSessionId": strOrNil(r["bridgeSessionId"])}}
	}
	if uuid != "" && hasParent {
		return alias(parent, true)
	}
	return engine.DecodeOut{NativeID: nativeID}
}

func strOrNil(v any) any {
	if s, ok := v.(string); ok {
		return s
	}
	return nil
}

// ------------------------------------------------------------------ encode (cc-write@1)

func ccContent(in engine.PlanInput, parts []core.Part, forResult bool) any {
	var out []core.O
	for _, p := range parts {
		switch core.S(p["type"]) {
		case "text":
			out = append(out, core.O{{K: "type", V: "text"}, {K: "text", V: core.S(p["text"])}})
		case "image":
			if uri := core.S(p["uri"]); uri != "" {
				out = append(out, core.O{{K: "type", V: "image"}, {K: "source", V: core.O{{K: "type", V: "url"}, {K: "url", V: uri}}}})
				continue
			}
			blob := core.Obj(p["blob"])
			if data, ok := shared.BlobBase64(in.Hub, core.S(blob["sha256"])); ok {
				out = append(out, core.O{{K: "type", V: "image"}, {K: "source", V: core.O{{K: "type", V: "base64"}, {K: "media_type", V: core.S(p["mediaType"])}, {K: "data", V: data}}}})
			} else {
				out = append(out, core.O{{K: "type", V: "text"}, {K: "text", V: "[image " + core.S(p["mediaType"]) + " unavailable]"}})
			}
		case "file":
			if uri := core.S(p["uri"]); uri != "" {
				out = append(out, core.O{{K: "type", V: "document"}, {K: "title", V: p["name"]}, {K: "source", V: core.O{{K: "type", V: "url"}, {K: "url", V: uri}}}})
				continue
			}
			if core.S(p["mediaType"]) == "application/pdf" || strings.HasPrefix(core.S(p["mediaType"]), "text/") || core.S(p["uri"]) != "" {
				if data, ok := in.Hub.Blob(core.S(core.Obj(p["blob"])["sha256"])); ok {
					source := core.O{{K: "type", V: "base64"}, {K: "media_type", V: core.S(p["mediaType"])}, {K: "data", V: base64.StdEncoding.EncodeToString(data)}}
					if strings.HasPrefix(core.S(p["mediaType"]), "text/") {
						source = core.O{{K: "type", V: "text"}, {K: "media_type", V: "text/plain"}, {K: "data", V: string(data)}}
					}
					out = append(out, core.O{{K: "type", V: "document"}, {K: "title", V: core.S(p["name"])}, {K: "source", V: source}})
				}
			} else {
				out = append(out, core.O{{K: "type", V: "text"}, {K: "text", V: shared.PartsText(in.Hub, []core.Part{p}, 0)}})
			}
		}
	}
	allText := true
	for _, b := range out {
		if t, _ := b.Get("type"); t != "text" {
			allText = false
		}
	}
	if forResult && allText {
		var ts []string
		for _, b := range out {
			t, _ := b.Get("text")
			ts = append(ts, t.(string))
		}
		return strings.Join(ts, "\n")
	}
	arr := make([]any, len(out))
	for i, b := range out {
		arr[i] = b
	}
	return arr
}

type ccCtx struct{ session, cwd, gitBranch string }

func renderCC(it planner.Item, in engine.PlanInput, c ccCtx) []core.O {
	same := it.Event.Provenance.Source.Harness == "claude-code"
	prompt := core.UUID5("cc-prompt\x00" + c.session + "\x00" + it.Key)
	switch it.Role {
	case "user":
		content := ccContent(in, it.Parts, false)
		if arr, ok := content.([]any); ok {
			all := true
			var ts []string
			for _, b := range arr {
				o := b.(core.O)
				if t, _ := o.Get("type"); t != "text" {
					all = false
					break
				}
				t, _ := o.Get("text")
				ts = append(ts, t.(string))
			}
			if all {
				content = strings.Join(ts, "\n")
			}
		}
		if it.Event.Kind == "notification" && it.Synthetic == "" {
			// Explicit range selection re-encodes even same-Agent records. Retain the native
			// distinction between a delivered task result and a fresh human prompt.
			return []core.O{{{K: "type", V: "user"}, {K: "message", V: core.O{{K: "role", V: "user"}, {K: "content", V: content}}}, {K: "promptSource", V: "system"}, {K: "turnOrigin", V: "task_notification"}, {K: "origin", V: core.O{{K: "kind", V: "task-notification"}, {K: "producer", V: "session-task"}}}}}
		}
		return []core.O{{{K: "type", V: "user"}, {K: "message", V: core.O{{K: "role", V: "user"}, {K: "content", V: content}}}, {K: "promptId", V: prompt}, {K: "promptSource", V: "typed"}, {K: "permissionMode", V: "default"}}}
	case "assistant":
		content := []any{}
		hasCalls := false
		for _, p := range it.Parts {
			switch core.S(p["type"]) {
			case "text":
				content = append(content, core.O{{K: "type", V: "text"}, {K: "text", V: core.S(p["text"])}})
			case "reasoning":
				if sealed, ok := p["sealed"].(map[string]any); ok {
					if b, ok := in.Hub.Blob(core.S(core.Obj(sealed["blob"])["sha256"])); ok {
						content = append(content, core.O{{K: "type", V: "redacted_thinking"}, {K: "data", V: string(b)}})
					}
				} else {
					content = append(content, core.O{{K: "type", V: "thinking"}, {K: "thinking", V: core.S(p["text"])}, {K: "signature", V: core.S(p["signature"])}})
				}
			case "tool_call":
				hasCalls = true
				n := core.NativeToolCall{Name: core.S(p["rawName"]), Args: p["args"]}
				if !same {
					if x, ok := vocab.FromCanonical(core.Obj(p["tool"])); ok {
						n = x
					}
				}
				content = append(content, core.O{{K: "type", V: "tool_use"}, {K: "id", V: core.S(p["callId"])}, {K: "name", V: n.Name}, {K: "input", V: n.Args}})
			}
		}
		m := core.Obj(it.Event.Attrs["model"])
		model, ok := m["id"].(string)
		if !ok {
			model = "unknown"
		}
		stop := "end_turn"
		if hasCalls {
			stop = "tool_use"
		}
		id := "msg_koto" + strings.ReplaceAll(core.UUID5("cc-msg\x00"+c.session+"\x00"+it.Key), "-", "")[:24]
		return []core.O{{{K: "type", V: "assistant"}, {K: "message", V: core.O{
			{K: "id", V: id}, {K: "type", V: "message"}, {K: "role", V: "assistant"},
			// Record whichever model produced the turn; never claim '<synthetic>' (Claude Code's own placeholder).
			{K: "model", V: model}, {K: "content", V: content}, {K: "stop_reason", V: stop}, {K: "stop_sequence", V: nil},
			{K: "usage", V: core.O{{K: "input_tokens", V: 0}, {K: "output_tokens", V: 0}, {K: "cache_creation_input_tokens", V: 0}, {K: "cache_read_input_tokens", V: 0}}},
		}}}}
	}
	// tool results: one user record per result, as Claude Code writes them
	var out []core.O
	for _, p := range it.Parts {
		if core.S(p["type"]) != "tool_result" {
			continue
		}
		res := core.O{{K: "type", V: "tool_result"}, {K: "tool_use_id", V: core.S(p["callId"])}, {K: "content", V: ccContent(in, core.Parts(p["parts"]), true)}}
		if core.S(p["outcome"]) != "success" {
			res = append(res, core.KV{K: "is_error", V: true})
		}
		out = append(out, core.O{{K: "type", V: "user"}, {K: "message", V: core.O{{K: "role", V: "user"}, {K: "content", V: []any{res}}}}, {K: "promptId", V: prompt}, {K: "promptSource", V: "typed"}, {K: "permissionMode", V: "default"}})
	}
	return out
}

func envelope(rec core.O, c ccCtx, uuid string, parent any, t float64) core.O {
	typ, _ := rec.Get("type")
	msg, _ := rec.Get("message")
	out := core.O{{K: "parentUuid", V: parent}, {K: "isSidechain", V: false}, {K: "type", V: typ}, {K: "message", V: msg}}
	for _, kv := range rec {
		if kv.K != "type" && kv.K != "message" {
			out = append(out, kv)
		}
	}
	return append(out, core.KV{K: "uuid", V: uuid}, core.KV{K: "timestamp", V: shared.ISO(t)}, core.KV{K: "userType", V: "external"}, core.KV{K: "entrypoint", V: "cli"},
		core.KV{K: "cwd", V: c.cwd}, core.KV{K: "sessionId", V: c.session}, core.KV{K: "version", V: WriteVersion}, core.KV{K: "gitBranch", V: c.gitBranch})
}

// Encode renders the projection as Claude Code records (also used by the Codex importer's shadow file).
func Encode(in engine.PlanInput, session, cwd string) []shared.NativeLine {
	return encodeItems(in, in.Projection, ccCtx{session: session, cwd: cwd, gitBranch: in.Projection.Hints.GitBranch})
}

func encodeItems(in engine.PlanInput, proj *planner.Projection, c ccCtx) []shared.NativeLine {
	var clock shared.Clock
	var parent any
	var lines []shared.NativeLine
	for _, it := range proj.Items {
		t := clock.Next(it.Event)
		// This encoder also renders Codex's import shadow. Only Claude Code records are valid
		// verbatim input here; a same-Codex copy still needs conversion to Claude Code JSONL.
		if it.Verbatim && in.Residual != nil && it.Event.Provenance.Source.Harness == "claude-code" {
			if raw := in.Residual(it.Event); raw != nil {
				var rec map[string]any
				if json.Unmarshal(raw, &rec) == nil {
					uuid := core.UUID5("cc\x00" + c.session + "\x00" + it.Key)
					rec["uuid"], rec["parentUuid"], rec["sessionId"] = uuid, parent, c.session
					rec["cwd"] = c.cwd
					lines = append(lines, shared.NativeLine{NativeID: uuid, EventID: it.Event.ID, Line: core.Stringify(rec)})
					parent = uuid
					continue
				}
			}
		}
		recs := renderCC(it, in, c)
		for i, rec := range recs {
			key := it.Key
			if len(recs) > 1 {
				key += "#" + strconv.Itoa(i)
			}
			uuid := core.UUID5("cc\x00" + c.session + "\x00" + key)
			lines = append(lines, shared.NativeLine{NativeID: uuid, EventID: it.Event.ID, Line: core.Stringify(envelope(rec, c, uuid, parent, t))})
			parent = uuid
		}
	}
	return lines
}

// Options for the connector.
type Options struct {
	Root string // $CLAUDE_CONFIG_DIR
	ID   string
	// DesktopScope is one account/org metadata directory; it filters the shared transcript root.
	DesktopScope string
	Identity     core.SessionIdentity
}

type cc struct {
	id, root, projects string
	desktopScope       string
	identity           core.SessionIdentity
	files              *shared.PathIndex
}

var sessionFile = regexp.MustCompile(`(?i)^[0-9a-f-]{36}\.jsonl$`)

// New makes a Claude Code connector.
func New(o Options) *engine.Connector {
	if o.ID == "" {
		o.ID = "claude-code"
	}
	c := &cc{id: o.ID, root: o.Root, projects: filepath.Join(o.Root, "projects"), desktopScope: o.DesktopScope, identity: o.Identity, files: shared.NewPathIndex()}
	out := &engine.Connector{ID: c.id, Root: c.root, Harness: "claude-code", Version: Version, Capabilities: Capabilities, Vocabulary: vocab,
		Source: c, Sink: c}
	return out
}

func (c *cc) Discover(ctx context.Context) ([]core.NativeRef, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if c.desktopScope != "" {
		return c.discoverDesktop(ctx)
	}
	dirs, err := os.ReadDir(c.projects)
	if err != nil {
		return nil, nil
	}
	var out []core.NativeRef
	for _, d := range dirs {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join(c.projects, d.Name())
		fs, _ := os.ReadDir(dir)
		for _, f := range fs {
			if e := ctx.Err(); e != nil {
				return nil, e
			}
			if !sessionFile.MatchString(f.Name()) {
				continue
			}
			p := filepath.Join(dir, f.Name())
			st, err := os.Stat(p)
			if err != nil {
				continue
			}
			session := strings.TrimSuffix(f.Name(), ".jsonl")
			c.files.Set(session, p)
			meta := core.JsonObject{"projectDir": d.Name(), "identity": core.IdentityMeta(c.identity)}
			out = append(out, core.NativeRef{Harness: "claude-code", Connector: c.id, Session: session, Path: p, Size: st.Size(), MtimeMs: engine.MtimeMs(st), Meta: meta})
		}
	}
	return out, ctx.Err()
}

func (c *cc) Tail(ctx context.Context, ref core.NativeRef, from *core.SourceOffset, yield func(core.SourceBatch) error) error {
	return engine.TailJSONL(ctx, ref, from, Decoder{}, yield, 0, false)
}

func (c *cc) WatchRoots() []string {
	roots := []string{c.projects}
	if c.desktopScope != "" {
		roots = append(roots, c.desktopScope)
	}
	return roots
}

func (c *cc) ReadRecord(session string, index int, offset *int64) ([]byte, error) {
	return c.ReadRecordContext(context.Background(), session, index, offset)
}
func (c *cc) ReadRecordContext(ctx context.Context, session string, index int, offset *int64) ([]byte, error) {
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

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func (c *cc) NewSessionID(key string) string { return core.UUID5("claude-code\x00" + key) }
func (c *cc) ResumeCommand() string {
	if c.desktopScope != "" {
		return ""
	}
	return "claude --resume {id}"
}

func (c *cc) Plan(in engine.PlanInput) (*engine.DeliveryPlan, error) {
	t := in.Target
	if err := c.checkTargetProfile(t); err != nil {
		return nil, err
	}
	existing := ""
	if t.Mode != "create" {
		if p, ok := c.files.Get(t.Session); ok {
			existing = p
		} else if b := in.Hub.Binding("claude-code", t.Session, c.id); b != nil {
			existing = b.Path
		}
	}
	home, _ := os.UserHomeDir()
	cwd := firstNonEmpty(t.Cwd, in.Projection.Hints.Cwd, home)
	if existing != "" {
		if b := in.Hub.Binding("claude-code", t.Session, c.id); b != nil {
			if bc, ok := b.Meta["cwd"].(string); ok {
				cwd = bc
			}
		}
	}
	file := existing
	if file == "" {
		file = filepath.Join(c.projects, ProjectDir(cwd), t.Session+".jsonl")
	}
	in, attachments := shared.PrepareAttachments(in, filepath.Join(strings.TrimSuffix(file, ".jsonl"), "attachments"), func(p core.Part, role string) bool {
		m := core.S(p["mediaType"])
		data, ok := in.Hub.Blob(core.S(core.Obj(p["blob"])["sha256"]))
		return role != "assistant" && (core.S(p["uri"]) != "" || m == "application/pdf" || strings.HasPrefix(m, "text/") && ok && utf8.Valid(data))
	})
	lines := encodeItems(in, in.Projection, ccCtx{session: t.Session, cwd: cwd, gitBranch: in.Projection.Hints.GitBranch})
	mode := t.Mode
	if len(lines) == 0 {
		mode = "noop"
	}
	// The title as Claude Code's own /rename writes it (not part of the conversation tree).
	if t.Mode == "create" && t.Title != "" && len(lines) > 0 {
		lines = append(lines, shared.NativeLine{NativeID: "#title", EventID: in.Head, Line: core.Stringify(core.O{{K: "type", V: "custom-title"}, {K: "customTitle", V: t.Title}, {K: "sessionId", V: t.Session}})})
	}
	content, all := shared.LinesToContent(lines)
	var records []engine.PlanRecord
	var shas []string
	for _, r := range all {
		if r.NativeID != "#title" {
			records = append(records, r)
			shas = append(shas, r.SHA256)
		}
	}
	if records == nil {
		records = []engine.PlanRecord{}
	}
	t.Path = file
	plan := &engine.DeliveryPlan{ID: shared.PlanID(c.id, t.Session, in.Head, mode, strings.Join(shas, "")), Sink: c.id, Harness: "claude-code",
		Thread: in.Thread, Head: in.Head, Level: in.Level, Mode: mode, Target: t, Records: records, Content: content,
		Losses: in.Projection.Losses, Notes: in.Projection.Notes, Extra: core.JsonObject{"attachments": attachments}}
	if c.desktopScope != "" {
		if t.Mode != "create" {
			return nil, fmt.Errorf("Claude Desktop only supports new sessions")
		}
		now := time.Now().UnixMilli()
		plan.Extra["desktopMetadata"] = core.JsonObject{"sessionId": "local_" + t.Session, "cliSessionId": t.Session, "title": t.Title, "titleSource": "user", "cwd": cwd, "originCwd": cwd, "createdAt": now, "lastActivityAt": now, "envScopeId": "builtin_local", "isArchived": false, "isStarred": false, "completedTurns": 0}
	}
	return plan, nil
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

func (c *cc) Apply(ctx context.Context, plan *engine.DeliveryPlan) (core.JsonObject, error) {
	if plan == nil {
		return nil, fmt.Errorf("missing Claude delivery plan")
	}
	if err := c.checkTargetProfile(plan.Target); err != nil {
		return nil, err
	}
	if c.desktopScope != "" {
		return c.applyDesktop(ctx, plan)
	}
	if err := shared.ApplyAttachments(plan); err != nil {
		return nil, err
	}
	r, err := shared.ApplyFilePlan(plan)
	if err == nil {
		c.files.Set(plan.Target.Session, plan.Target.Path)
	}
	return r, err
}

func (c *cc) checkTargetProfile(target engine.DeliveryTarget) error {
	if target.Profile != "" && target.Profile != c.id || target.Root != "" && filepath.Clean(target.Root) != filepath.Clean(c.root) {
		return fmt.Errorf("Claude target storage profile changed")
	}
	return nil
}
