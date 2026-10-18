package claudecode

import (
	"koto/internal/connectors/shared"
	"koto/internal/core"
)

func indexBlocks(content any, assetDir string) []core.Part {
	if s, ok := content.(string); ok {
		if s == "" {
			return nil
		}
		return shared.DecodeAttachmentTextIndex(s, assetDir)
	}
	var out []core.Part
	for _, x := range core.Arr(content) {
		b := core.Obj(x)
		switch core.S(b["type"]) {
		case "text":
			if s, ok := b["text"].(string); ok {
				out = append(out, shared.DecodeAttachmentTextIndex(s, assetDir)...)
			}
		case "image":
			src := core.Obj(b["source"])
			_, hasData := src["data"].(string)
			if src["type"] == "base64" && hasData || core.S(src["url"]) != "" {
				out = append(out, core.Part{"type": "image"})
			}
		case "document":
			out = append(out, core.Part{"type": "file"})
		case "tool_result":
			out = append(out, core.Part{"type": "tool_result"})
		case "tool_use":
			out = append(out, core.Part{"type": "tool_call"})
		case "thinking", "redacted_thinking":
			out = append(out, core.Part{"type": "reasoning"})
		default:
			out = append(out, core.Part{"type": "opaque"})
		}
	}
	return out
}
