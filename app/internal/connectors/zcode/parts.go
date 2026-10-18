package zcode

import (
	"fmt"
	"koto/internal/core"
	"strings"
)

func (c *connector) decodePart(p core.JsonObject, put func([]byte) core.JsonObject, indexMode ...bool) ([]core.Part, error) {
	indexOnly := len(indexMode) > 0 && indexMode[0]
	switch core.S(p["type"]) {
	case "text":
		if p["ignored"] == true {
			return nil, nil
		}
		return []core.Part{{"type": "text", "text": core.S(p["text"])}}, nil
	case "reasoning":
		return []core.Part{{"type": "reasoning", "text": core.S(p["text"])}}, nil
	case "file":
		mime := core.S(p["mime"])
		typ := "file"
		if strings.HasPrefix(mime, "image/") {
			typ = "image"
		}
		out := core.Part{"type": typ, "mediaType": mime, "name": core.S(p["filename"])}
		if indexOnly {
			return []core.Part{out}, nil
		}
		uri := core.S(p["url"])
		if artifact := core.S(core.Obj(p["metadata"])["artifactUri"]); strings.HasPrefix(artifact, "zcode-artifact://") {
			uri = artifact
		}
		if data, ok := c.attachmentBytes(uri); ok {
			if hash := core.S(core.Obj(p["metadata"])["sha256"]); hash != "" && hash != core.SHA256Hex(data) {
				return nil, fmt.Errorf("ZCode attachment changed while reading")
			}
			out["blob"] = put(data)
		} else if strings.HasPrefix(uri, "https://") || strings.HasPrefix(uri, "http://") {
			out["uri"] = uri
		} else {
			return nil, fmt.Errorf("ZCode attachment became unavailable while reading")
		}
		return []core.Part{out}, nil
	default:
		return []core.Part{{"type": "opaque", "nativeType": p["type"], "value": p}}, nil
	}
}
