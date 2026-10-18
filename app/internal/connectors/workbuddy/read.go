package workbuddy

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"koto/internal/connectors/claudecode"
	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
)

type decoder struct{ root string }

func (decoder) FormatVersion() string         { return "5.8-local" }
func (decoder) InitialState() core.JsonObject { return core.JsonObject{} }
func (decoder) Decode(any, []byte, *engine.DecodeContext) engine.DecodeOut {
	// WorkBuddy can produce context and message events from one physical line.
	// The strict multi-event path must also propagate missing/corrupt material
	// errors; collapsing it into the legacy decoder would silently lose data.
	panic("WorkBuddy requires JSONLMultiDecoder.DecodeMany")
}

var fileRef = regexp.MustCompile(`<file path="([^"]+)"\s*/>`)

func (d decoder) DecodeMany(v any, raw []byte, ctx *engine.DecodeContext) ([]engine.DecodeOut, error) {
	r := core.Obj(v)
	id := core.S(r["id"])
	if id == "" {
		id = fmt.Sprintf("#record-%d", ctx.Index)
	}
	typ := core.S(r["type"])
	meta := core.JsonObject{}
	if cwd := core.S(r["cwd"]); cwd != "" {
		meta["cwd"] = cwd
	}
	parent := core.NativeParent(core.S(r["parentId"]))
	if parent.Native == "" {
		parent = core.NativeParent(core.S(r["logicalParentId"]))
	}
	if parent.Native == "" {
		parent = core.NullParent()
	}
	if fork := core.S(ctx.State["forkParent"]); fork != "" && typ == "message" && r["role"] == "user" {
		parent = core.NativeParent(fork)
		delete(ctx.State, "forkParent")
	}
	if typ == "resend-fork-notice" {
		ctx.State["forkParent"] = core.S(r["parentId"])
	}
	switch typ {
	case "custom-title", "ai-title", "session-meta", "file-history-snapshot", "summary", "topic", "goal-result", "goal-progress", "resend-fork-notice":
		if typ == "ai-title" {
			meta["title"] = core.S(r["aiTitle"])
		}
		if typ == "custom-title" {
			meta["title"] = core.S(r["customTitle"])
		}
		return []engine.DecodeOut{{NativeID: id, HasAlias: true, Alias: parent, Meta: meta}}, nil
	}
	pd := core.Obj(r["providerData"])
	compacted := pd["isCompacted"] == true && typ == "message" && r["role"] == "user"
	if !compacted && (pd["isCompactInternal"] == true || core.S(pd["compactType"]) != "") {
		return nil, fmt.Errorf("WorkBuddy compacted history requires native replay validation before copying")
	}
	ts, _ := core.Num(r["timestamp"])
	attrs := core.JsonObject{}
	if m := core.S(pd["model"]); m != "" {
		attrs["model"] = core.JsonObject{"id": m}
	}
	makeOut := func(nid, kind, actor string, parts []core.Part, p *core.ParentRef) engine.DecodeOut {
		return engine.DecodeOut{NativeID: nid, Meta: meta, Draft: &core.DraftEvent{NativeID: nid, Kind: kind, Actor: actor, Parts: parts, Parent: p, Time: &ts, Attrs: attrs, Identity: "native"}}
	}
	switch typ {
	case "message":
		role := core.S(r["role"])
		if role != "user" && role != "assistant" && role != "system" {
			return nil, fmt.Errorf("unsupported WorkBuddy message role %q", role)
		}
		parts := []core.Part{}
		contextParts := []core.Part{}
		blocks := core.Arr(r["content"])
		if text, ok := r["content"].(string); ok {
			blocks = []any{core.JsonObject{"type": "text", "text": text}}
		}
		for _, v := range blocks {
			b := core.Obj(v)
			switch core.S(b["type"]) {
			case "input_text", "output_text", "text":
				if ctx.IndexOnly && role != "user" {
					continue
				}
				text := core.S(b["text"])
				if role == "user" {
					if start := strings.Index(text, "<user_query>"); start >= 0 && strings.HasSuffix(text, "</user_query>") {
						prefix := text[:start]
						if prefix != "" {
							contextParts = append(contextParts, core.Part{"type": "text", "text": prefix})
						}
						text = text[start+len("<user_query>") : len(text)-len("</user_query>")]
						for _, m := range fileRef.FindAllStringSubmatch(prefix, -1) {
							path := html.UnescapeString(m[1])
							p, err := d.file(path, "", filepath.Base(path), ctx)
							if err != nil {
								return nil, err
							}
							parts = append(parts, p)
							// The native @"path" badge is represented by the file part;
							// retaining it would leak a source path after a cross-agent copy.
							text = strings.ReplaceAll(text, `@"`+path+`"`, "")
						}
					} else if strings.HasPrefix(text, "<image_local_path>") && strings.HasSuffix(text, "</image_local_path>") {
						continue
					}
				}
				if text != "" {
					if role == "assistant" {
						parts = append(parts, attachmentText(text, ctx)...)
					} else {
						parts = append(parts, core.Part{"type": "text", "text": text})
					}
				}
			case "input_image", "image_blob_ref":
				part, err := d.image(b, ctx)
				if err != nil {
					return nil, err
				}
				parts = append(parts, part)
			default:
				return nil, fmt.Errorf("unsupported WorkBuddy content block %q", b["type"])
			}
		}
		out := []engine.DecodeOut{}
		if len(contextParts) > 0 {
			out = append(out, makeOut(id+"#context", "context", "system", contextParts, parent))
			parent = core.NativeParent(id + "#context")
		}
		kind := "message"
		if compacted {
			// Native history retains the earlier records, while its inference
			// context starts at this boundary. Preserve both facts in the IR.
			attrs["workbuddyCompaction"] = core.JsonObject{"type": pd["compactType"], "summary": pd["isSummary"], "kind": pd["compactionKind"], "trigger": pd["compactionTrigger"]}
			item := makeOut(id, "compaction", "harness", parts, parent)
			if !parent.Null {
				item.Draft.Replaces = &core.DraftReplaces{To: parent}
			}
			return append(out, item), nil
		}
		if role == "system" || pd["isMeta"] == true || pd["skipRun"] == true {
			kind = "context"
		}
		out = append(out, makeOut(id, kind, role, parts, parent))
		return out, nil
	case "function_call":
		if ctx.IndexOnly {
			return []engine.DecodeOut{makeOut(id, "message", "assistant", nil, parent)}, nil
		}
		var args any
		if err := json.Unmarshal([]byte(core.S(r["arguments"])), &args); err != nil {
			return nil, fmt.Errorf("invalid WorkBuddy tool arguments: %w", err)
		}
		name := core.S(r["name"])
		return []engine.DecodeOut{makeOut(id, "message", "assistant", []core.Part{{"type": "tool_call", "callId": core.S(r["callId"]), "rawName": name, "args": args, "tool": (claudecode.Vocabulary{}).ToCanonical(name, args)}}, parent)}, nil
	case "function_call_result", "function_call_output":
		if ctx.IndexOnly {
			return []engine.DecodeOut{makeOut(id, "message", "tool", nil, parent)}, nil
		}
		output := core.Obj(r["output"])
		if output["type"] != "text" {
			return nil, fmt.Errorf("unsupported WorkBuddy tool result %q", output["type"])
		}
		outcome := "success"
		result := core.Obj(pd["toolResult"])
		if r["status"] != "completed" || core.S(result["error"]) != "" || core.Obj(result["rawResponse"])["is_error"] == true {
			outcome = "error"
		}
		parts, err := d.toolOutput(core.S(output["text"]), ctx)
		if err != nil {
			return nil, err
		}
		return []engine.DecodeOut{makeOut(id, "message", "tool", []core.Part{{"type": "tool_result", "callId": core.S(r["callId"]), "outcome": outcome, "parts": parts}}, parent)}, nil
	case "reasoning":
		if ctx.IndexOnly {
			return []engine.DecodeOut{makeOut(id, "message", "assistant", nil, parent)}, nil
		}
		parts := []core.Part{}
		for _, v := range core.Arr(r["rawContent"]) {
			b := core.Obj(v)
			if b["type"] != "reasoning_text" {
				return nil, fmt.Errorf("unsupported WorkBuddy reasoning content %q", b["type"])
			}
			if t := core.S(b["text"]); t != "" {
				parts = append(parts, core.Part{"type": "reasoning", "text": t})
			}
		}
		if len(parts) == 0 {
			return nil, fmt.Errorf("unsupported WorkBuddy reasoning record")
		}
		return []engine.DecodeOut{makeOut(id, "message", "assistant", parts, parent)}, nil
	default:
		return nil, fmt.Errorf("unsupported WorkBuddy record type %q", typ)
	}
}
func readMaterial(path string) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("WorkBuddy attachment unavailable: %w", err)
	}
	if !st.Mode().IsRegular() || st.Size() > 256*1024*1024 {
		return nil, fmt.Errorf("WorkBuddy attachment is not a bounded regular file")
	}
	return os.ReadFile(path)
}
func (d decoder) file(path, mime, name string, ctx *engine.DecodeContext) (core.Part, error) {
	if ctx.IndexOnly {
		return core.Part{"type": "file", "name": name}, nil
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("WorkBuddy attachment path is not absolute")
	}
	b, err := readMaterial(path)
	if err != nil {
		return nil, err
	}
	if mime == "" {
		mime = http.DetectContentType(b)
	}
	return core.Part{"type": "file", "name": name, "mediaType": mime, "blob": ctx.Blob(b), "uri": shared.FileURL(path).String()}, nil
}

// Tool Read persists image results as a JSON array inside output.text. Those
// references must become material parts before copying to another app/root.
func attachmentText(text string, ctx *engine.DecodeContext) []core.Part {
	dir := strings.TrimSuffix(ctx.Ref.Path, ".jsonl") + ".assets"
	if ctx.IndexOnly {
		return shared.DecodeAttachmentTextIndex(text, dir)
	}
	return shared.DecodeAttachmentText(text, dir, ctx.Blob)
}

func (d decoder) toolOutput(text string, ctx *engine.DecodeContext) ([]core.Part, error) {
	var blocks []core.JsonObject
	if json.Unmarshal([]byte(text), &blocks) == nil {
		hasImage := false
		for _, b := range blocks {
			if b["type"] == "image_blob_ref" {
				hasImage = true
			}
		}
		if hasImage {
			parts := []core.Part{}
			for _, b := range blocks {
				switch b["type"] {
				case "image_blob_ref", "input_image":
					p, err := d.image(b, ctx)
					if err != nil {
						return nil, err
					}
					parts = append(parts, p)
				case "text":
					parts = append(parts, attachmentText(core.S(b["text"]), ctx)...)
				default:
					return nil, fmt.Errorf("unsupported WorkBuddy material result block %q", b["type"])
				}
			}
			return parts, nil
		}
	}
	return attachmentText(text, ctx), nil
}
func (d decoder) image(b core.JsonObject, ctx *engine.DecodeContext) (core.Part, error) {
	if ctx.IndexOnly {
		return core.Part{"type": "image"}, nil
	}
	img := core.Obj(b["image"])
	if b["type"] == "image_blob_ref" {
		img = b
	}
	mime, name := core.S(img["mime"]), core.S(b["original_filename"])
	if mime == "" {
		mime = core.S(img["mediaType"])
	}
	if name == "" {
		name = core.S(img["original_filename"])
	}
	var data []byte
	var err error
	if img["type"] == "image_blob_ref" {
		path := core.S(img["blob_path"])
		real, e := filepath.EvalSymlinks(path)
		root, re := filepath.EvalSymlinks(filepath.Join(d.root, "blobs"))
		if e != nil || re != nil || !strings.HasPrefix(real, root+string(filepath.Separator)) {
			return nil, fmt.Errorf("WorkBuddy image blob is missing or outside its blob directory")
		}
		data, err = readMaterial(real)
		if err != nil {
			return nil, err
		}
		if core.SHA256Hex(data) != core.S(img["blob_id"]) {
			return nil, fmt.Errorf("WorkBuddy image blob checksum changed")
		}
	} else {
		encoded := core.S(img["data"])
		if raw, ok := b["image"].(string); ok {
			if !strings.HasPrefix(raw, "data:") {
				return nil, fmt.Errorf("WorkBuddy remote image must be materialized before copying")
			}
			parts := strings.SplitN(strings.TrimPrefix(raw, "data:"), ";base64,", 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid WorkBuddy inline image URI")
			}
			mime, encoded = parts[0], parts[1]
		}
		if encoded == "" || len(encoded) > 256*1024*1024*4/3+4 {
			return nil, fmt.Errorf("unsupported WorkBuddy image storage or size")
		}
		data, err = base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("invalid WorkBuddy inline image: %w", err)
		}
	}
	if mime == "" {
		mime = http.DetectContentType(data)
	}
	return core.Part{"type": "image", "blob": ctx.Blob(data), "mediaType": mime, "name": name}, nil
}
