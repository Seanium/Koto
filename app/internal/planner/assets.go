package planner

import (
	"fmt"
	"koto/internal/core"
	"strings"
)

// Resolve only bytes already read from native storage, never arbitrary message paths or URLs.
// Preserve the original bytes even when a target needs an ordinary file instead of an inline part.
func normalizeAssets(parts []core.Part, role string, raw bool, o Options, loss func(string, string, string)) ([]core.Part, bool) {
	out := make([]core.Part, 0, len(parts))
	changed := false
	for _, p := range parts {
		typ := core.S(p["type"])
		if typ == "tool_result" {
			children, ch := normalizeAssets(core.Parts(p["parts"]), "tool", raw, o, loss)
			if ch {
				p = copyPart(p)
				p["parts"] = core.PartsJSON(children)
				changed = true
			}
			out = append(out, p)
			continue
		}
		if typ != "image" && typ != "file" {
			out = append(out, p)
			continue
		}
		media, name := core.S(p["mediaType"]), core.S(p["name"])
		if name == "" {
			name = media
		}
		if name == "" {
			name = "附件"
		}
		hasBytes := false
		if o.ReadBlob != nil {
			if sha := core.S(core.Obj(p["blob"])["sha256"]); sha != "" {
				_, hasBytes = o.ReadBlob(sha)
			}
		}
		// Native readers may retain the source URI beside verified attachment
		// bytes. Re-encode those bytes for the destination: keeping the URI here
		// makes several writers reuse a path owned by the original conversation.
		if hasBytes && core.S(p["uri"]) != "" {
			p = copyPart(p)
			delete(p, "uri")
			changed = true
		}
		if uri := core.S(p["uri"]); uri != "" {
			if (o.SinkHarness == "opencode" && role == "user" || o.SinkHarness == "claude-code" || o.SinkHarness == "antigravity" || o.SinkHarness == "codex" && (typ == "image" || strings.HasPrefix(media, "audio/"))) && role != "assistant" {
				out = append(out, p)
			} else {
				out = append(out, core.Part{"type": "text", "text": fmt.Sprintf("[附件：%s](%s)", name, uri)})
				loss(typ, "referenced", "保留原始附件 URL；未下载远程内容")
			}
			changed = true
			continue
		}
		if o.ReadBlob != nil {
			if !hasBytes {
				out = append(out, core.Part{"type": "text", "text": "[" + name + "：附件内容不可用]"})
				loss(typ, "dropped", "附件内容不可用")
				changed = true
				continue
			}
		}
		if typ == "image" {
			supported := media == "image/png" || media == "image/jpeg" || media == "image/gif" || media == "image/webp"
			if o.Capabilities.Write == nil || !o.Capabilities.Write.Images || !supported || role == "assistant" && o.SinkHarness != "dsh" {
				p = copyPart(p)
				p["type"] = "file"
				if core.S(p["name"]) == "" {
					p["name"] = "image." + strings.TrimPrefix(media, "image/")
				}
				changed = true
			}
		} else {
			changed = true
		} // embedded or side-file documents are re-encoded with independent assets.
		out = append(out, p)
	}
	return out, changed
}
