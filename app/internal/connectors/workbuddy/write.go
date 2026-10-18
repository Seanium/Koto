package workbuddy

import (
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"koto/internal/connectors/claudecode"
	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/planner"
)

func (c *connector) Plan(in engine.PlanInput) (*engine.DeliveryPlan, error) {
	if in.Projection == nil || in.Target.Mode != "create" || !uuid.MatchString(in.Target.Session) {
		return nil, fmt.Errorf("WorkBuddy requires a new-session copy")
	}
	if in.Target.Profile != "" && in.Target.Profile != c.id {
		return nil, fmt.Errorf("WorkBuddy target profile does not match its storage directory")
	}
	db, err := c.open(false)
	if err != nil {
		return nil, err
	}
	owner, err := c.owner(context.Background(), db, in.Target.Account)
	db.Close()
	if err != nil {
		return nil, err
	}
	target := in.Target
	target.Profile, target.Account, target.Root = c.id, owner, c.root
	if target.Cwd == "" {
		target.Cwd = in.Projection.Hints.Cwd
	}
	if target.Cwd == "" {
		return nil, fmt.Errorf("WorkBuddy requires a project folder")
	}
	target.Cwd, err = filepath.Abs(target.Cwd)
	if err != nil {
		return nil, err
	}
	target.Path = c.transcript(target.Cwd, target.Session)
	// WorkBuddy has native user attachment badges, but non-user file
	// blocks are not part of its format. Keep their complete bytes beside the
	// new transcript and carry a normal text reference with an explicit loss.
	in, referencedAssets := shared.PrepareAttachments(in, strings.TrimSuffix(target.Path, ".jsonl")+".assets", func(_ core.Part, role string) bool { return role == "user" })
	plan := &engine.DeliveryPlan{Sink: c.id, Harness: c.harness, Mode: "create", Thread: in.Thread, Head: in.Head, Level: in.Level, Target: target, Losses: append([]core.Loss{}, in.Projection.Losses...), Notes: append([]string{}, in.Projection.Notes...), Extra: core.JsonObject{"owner": owner, "attachments": []any{}}}
	lines := []shared.NativeLine{}
	var clock shared.Clock
	parent := ""
	ordinal := 0
	compactionSeq := 0
	assets := referencedAssets
	seen := map[string]bool{}
	for _, asset := range assets {
		seen[core.S(core.Obj(asset)["path"])] = true
	}
	created := time.Now().UnixMilli()
	add := func(it planner.Item, r core.JsonObject) {
		ordinal++
		id := core.UUID5(c.harness + "\x00" + target.Session + "\x00" + fmt.Sprint(ordinal))
		r["id"], r["sessionId"], r["cwd"] = id, target.Session, target.Cwd
		r["timestamp"] = clock.Next(it.Event)
		if parent != "" {
			r["parentId"] = parent
		}
		if core.Obj(r["providerData"])["isCompacted"] == true {
			r["logicalParentId"] = parent
			delete(r, "parentId")
			core.Obj(r["providerData"])["compactionId"] = id
		}
		parent = id
		lines = append(lines, shared.NativeLine{NativeID: id, EventID: it.Event.ID, Line: core.Stringify(r)})
	}
	materialBytes := func(p core.Part) ([]byte, error) {
		sha := core.S(core.Obj(p["blob"])["sha256"])
		if sha == "" || in.Hub == nil {
			return nil, fmt.Errorf("WorkBuddy copy needs the original attachment bytes")
		}
		data, ok := in.Hub.Blob(sha)
		if !ok || core.SHA256Hex(data) != sha {
			return nil, fmt.Errorf("WorkBuddy attachment unavailable or changed")
		}
		return data, nil
	}
	media := func(p core.Part) (string, []byte, error) {
		data, err := materialBytes(p)
		if err != nil {
			return "", nil, err
		}
		sha := core.SHA256Hex(data)
		name := shared.AttachmentName(core.S(p["name"]))
		if name == "attachment" && p["type"] == "image" {
			name = "image"
		}
		path := filepath.Join(strings.TrimSuffix(target.Path, ".jsonl")+".assets", sha, name)
		if p["type"] == "image" {
			ext := filepath.Ext(name)
			if ext == "" {
				switch core.S(p["mediaType"]) {
				case "image/png":
					ext = ".png"
				case "image/jpeg":
					ext = ".jpg"
				case "image/webp":
					ext = ".webp"
				case "image/gif":
					ext = ".gif"
				default:
					return "", nil, fmt.Errorf("unsupported WorkBuddy image media type")
				}
			}
			path = filepath.Join(c.root, "blobs", sha[:2], sha+ext)
		}
		if !seen[path] {
			seen[path] = true
			assets = append(assets, core.JsonObject{"path": path, "data": data})
		}
		return path, data, nil
	}
	for _, it := range in.Projection.Items {
		if it.Event == nil || it.Event.Kind == "context" {
			return nil, fmt.Errorf("system context cannot be copied")
		}
		role := it.Role
		if role != "user" && role != "assistant" && role != "tool" {
			return nil, fmt.Errorf("unsupported WorkBuddy role %q", role)
		}
		content := []any{}
		filePaths := []string{}
		referenceRewrites := map[string]string{}
		flush := func() {
			if len(content) == 0 {
				return
			}
			if role == "user" && it.Event.Kind != "compaction" {
				textIndex := -1
				for i, x := range content {
					if core.Obj(x)["type"] == "input_text" {
						textIndex = i
						break
					}
				}
				if textIndex < 0 {
					content = append([]any{core.JsonObject{"type": "input_text", "text": ""}}, content...)
					textIndex = 0
				}
				p := core.Obj(content[textIndex])
				text := core.S(p["text"])
				for _, x := range content {
					part := core.Obj(x)
					if part["type"] != "input_text" {
						continue
					}
					value := core.S(part["text"])
					for old, replacement := range referenceRewrites {
						value = strings.ReplaceAll(value, `@"`+old+`"`, `@"`+replacement+`"`)
					}
					part["text"] = value
				}
				text = core.S(p["text"])
				prefix := ""
				if len(filePaths) > 0 {
					var b strings.Builder
					b.WriteString("<system-reminder data-role=\"additional-data\">\n<attached_files>\n")
					for _, path := range filePaths {
						b.WriteString("<file path=\"" + html.EscapeString(path) + "\" />\n")
					}
					b.WriteString("</attached_files>\n</system-reminder>\n")
					prefix = b.String()
				}
				p["text"] = prefix + "<user_query>" + text + "</user_query>"
			}
			record := core.JsonObject{"type": "message", "role": role, "status": "completed", "content": content}
			if it.Event.Kind == "compaction" {
				compactionSeq++
				native := core.Obj(it.Event.Attrs["workbuddyCompaction"])
				compactType := core.S(native["type"])
				if compactType == "" {
					compactType = "user-command"
				}
				kind, trigger := core.S(native["kind"]), core.S(native["trigger"])
				if kind == "" {
					kind = "manual"
				}
				if trigger == "" {
					trigger = "manual"
				}
				record["providerData"] = core.JsonObject{"skipRun": false, "isCompacted": true, "isCompactInternal": true, "isSummary": native["summary"] != false, "compactType": compactType, "compactionKind": kind, "compactionTrigger": trigger, "compactionSeq": compactionSeq}
			}
			add(it, record)
			content = nil
			filePaths = nil
		}
		for _, p := range it.Parts {
			switch core.S(p["type"]) {
			case "text":
				typ := "output_text"
				if role == "user" {
					typ = "input_text"
				}
				content = append(content, core.JsonObject{"type": typ, "text": core.S(p["text"])})
			case "image":
				if role != "user" {
					return nil, fmt.Errorf("WorkBuddy native image output is not yet validated for %s messages", role)
				}
				data, e := materialBytes(p)
				if e != nil {
					return nil, e
				}
				mime := core.S(p["mediaType"])
				switch mime {
				case "image/png", "image/jpeg", "image/webp", "image/gif":
				default:
					return nil, fmt.Errorf("unsupported WorkBuddy image media type %q", mime)
				}
				// The native history reader accepts both representations, but 5.8.x
				// manual compaction calls image.startsWith and fails on blob objects.
				// Inline bytes preserve the image and work in both native paths.
				content = append(content, core.JsonObject{"type": "input_image", "original_filename": core.S(p["name"]), "image": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)})
			case "file":
				if role != "user" {
					return nil, fmt.Errorf("WorkBuddy native attachment output is not yet validated for %s messages", role)
				}
				path, _, e := media(p)
				if e != nil {
					return nil, e
				}
				filePaths = append(filePaths, path)
				// The planner strips the source URI after verifying bytes. Use the
				// original event only to relocate WorkBuddy's explicit file badge.
				for _, original := range it.Event.Parts {
					if core.S(core.Obj(original["blob"])["sha256"]) != core.S(core.Obj(p["blob"])["sha256"]) {
						continue
					}
					if u, e := url.Parse(core.S(original["uri"])); e == nil {
						if old, ok := shared.FileURLPath(u); ok {
							referenceRewrites[old] = path
						}
					}
				}
			case "reasoning":
				flush()
				add(it, core.JsonObject{"type": "reasoning", "content": []any{}, "rawContent": []any{core.JsonObject{"type": "reasoning_text", "text": core.S(p["text"])}}})
			case "tool_call":
				flush()
				name, args := core.S(p["rawName"]), p["args"]
				if native, ok := (claudecode.Vocabulary{}).FromCanonical(core.Obj(p["tool"])); ok {
					name, args = native.Name, native.Args
				}
				if name == "" {
					return nil, fmt.Errorf("WorkBuddy tool name missing")
				}
				add(it, core.JsonObject{"type": "function_call", "callId": core.S(p["callId"]), "name": name, "arguments": core.Stringify(args)})
			case "tool_result":
				flush()
				var b strings.Builder
				blocks := []any{}
				hasImage := false
				for _, child := range core.Parts(p["parts"]) {
					switch child["type"] {
					case "text":
						text := core.S(child["text"])
						b.WriteString(text)
						blocks = append(blocks, core.JsonObject{"type": "text", "text": text})
					case "image":
						path, data, e := media(child)
						if e != nil {
							return nil, e
						}
						hasImage = true
						blocks = append(blocks, core.JsonObject{"type": "image_blob_ref", "blob_id": core.SHA256Hex(data), "mime": core.S(child["mediaType"]), "size": len(data), "blob_path": path, "original_filename": core.S(child["name"])})
					default:
						return nil, fmt.Errorf("unsupported WorkBuddy tool result part %q", child["type"])
					}
				}
				text := b.String()
				if hasImage {
					text = core.Stringify(blocks)
				}
				status := "completed"
				if core.S(p["outcome"]) != "success" {
					status = "failed"
				}
				add(it, core.JsonObject{"type": "function_call_result", "callId": core.S(p["callId"]), "status": status, "output": core.JsonObject{"type": "text", "text": text}})
			default:
				return nil, fmt.Errorf("unsupported WorkBuddy projected part %q", p["type"])
			}
		}
		if len(filePaths) > 0 && len(content) == 0 {
			content = append(content, core.JsonObject{"type": "input_text", "text": ""})
		}
		flush()
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("no WorkBuddy messages selected")
	}
	plan.Content, plan.Records = shared.LinesToContent(lines)
	plan.ID = shared.PlanID(c.id, target.Session, in.Head, "create", core.SHA256Hex(plan.Content))
	plan.Extra["attachments"] = assets
	plan.Extra["created"] = created
	return plan, nil
}
