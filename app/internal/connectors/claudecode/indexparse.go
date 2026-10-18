package claudecode

import (
	"bytes"
	"encoding/json"
	"koto/internal/connectors/shared"
	"koto/internal/core"
)

// ParseIndex selects metadata and user content without allocating strings for
// image data, tool results or assistant bodies.
func (Decoder) ParseIndex(raw []byte) (any, error) {
	fields, err := shared.IndexJSONFields(raw)
	if err != nil {
		return nil, err
	}
	row, err := shared.IndexJSONValues(fields, "type", "uuid", "parentUuid", "timestamp", "cwd", "gitBranch", "version", "entrypoint", "isSidechain", "isCompactSummary", "isMeta", "isApiErrorMessage", "origin", "turnOrigin", "subtype", "logicalParentUuid", "relocatedCwd", "customTitle", "aiTitle", "summary", "bridgeSessionId", "forkedFrom", "sessionId")
	if err != nil {
		return nil, err
	}
	if row["type"] == "attachment" {
		if raw, ok := fields["attachment"]; ok && bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
			af, err := shared.IndexJSONFields(raw)
			if err != nil {
				return nil, err
			}
			a, err := shared.IndexJSONValues(af, "type", "filename")
			if err != nil {
				return nil, err
			}
			if raw, ok := af["content"]; ok && a["type"] == "file" && bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
				cf, err := shared.IndexJSONFields(raw)
				if err != nil {
					return nil, err
				}
				c, err := shared.IndexJSONValues(cf, "type")
				if err != nil {
					return nil, err
				}
				if raw, ok := cf["file"]; ok && c["type"] == "text" && bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
					ff, err := shared.IndexJSONFields(raw)
					if err != nil {
						return nil, err
					}
					f, err := shared.IndexJSONValues(ff, "filePath")
					if err != nil {
						return nil, err
					}
					if raw, exists := ff["content"]; exists {
						f["content"] = nil
						if raw = bytes.TrimSpace(raw); len(raw) > 0 && raw[0] == '"' {
							f["content"] = ""
						}
					}
					c["file"] = f
				}
				a["content"] = c
			}
			row["attachment"] = a
		}
	}
	if raw, ok := fields["message"]; ok {
		mf, err := shared.IndexJSONFields(raw)
		if err != nil {
			return nil, err
		}
		message, err := shared.IndexJSONValues(mf, "model", "id", "stop_reason", "usage")
		if err != nil {
			return nil, err
		}
		if row["type"] == "user" {
			if raw, ok := mf["content"]; ok {
				content, err := parseIndexContent(raw)
				if err != nil {
					return nil, err
				}
				message["content"] = content
			}
		}
		row["message"] = message
	}
	return row, nil
}

func parseIndexContent(raw []byte) (any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var text string
		err := json.Unmarshal(raw, &text)
		return text, err
	}
	blocks, err := shared.IndexJSONElements(raw)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(blocks))
	for _, raw := range blocks {
		fields, err := shared.IndexJSONFields(raw)
		if err != nil {
			return nil, err
		}
		block, err := shared.IndexJSONValues(fields, "type")
		if err != nil {
			return nil, err
		}
		switch block["type"] {
		case "text":
			text, err := shared.IndexJSONValues(fields, "text")
			if err != nil {
				return nil, err
			}
			for k, v := range text {
				block[k] = v
			}
		case "image":
			if raw, ok := fields["source"]; ok {
				sf, err := shared.IndexJSONFields(raw)
				if err != nil {
					return nil, err
				}
				source, err := shared.IndexJSONValues(sf, "type")
				if err != nil {
					return nil, err
				}
				if raw := bytes.TrimSpace(sf["data"]); len(raw) > 0 && raw[0] == '"' {
					source["data"] = ""
				}
				if raw := bytes.TrimSpace(sf["url"]); len(raw) > 2 && raw[0] == '"' {
					source["url"] = "present"
				}
				block["source"] = core.JsonObject(source)
			}
		}
		out = append(out, block)
	}
	return out, nil
}
