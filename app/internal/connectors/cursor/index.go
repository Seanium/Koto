package cursor

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"koto/internal/core"
)

// Only list-visible user text is decoded. The JSON decoder skips image bytes,
// tool arguments/results and assistant bodies without materializing them.
func indexModelMessage(raw []byte) (string, []core.Part, error) {
	var header struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return "", nil, err
	}
	if header.Role != "user" {
		return header.Role, nil, nil
	}
	var message struct {
		Content indexContent `json:"content"`
	}
	if err := json.Unmarshal(raw, &message); err != nil {
		return "", nil, err
	}
	return header.Role, message.Content, nil
}

type indexContent []core.Part

func (p *indexContent) UnmarshalJSON(raw []byte) error {
	if len(raw) > 0 && raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return err
		}
		*p = []core.Part{{"type": "text", "text": text}}
		return nil
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return err
	}
	for _, b := range blocks {
		switch b.Type {
		case "text", "reasoning":
			*p = append(*p, core.Part{"type": b.Type, "text": b.Text})
		case "image", "file":
			*p = append(*p, core.Part{"type": b.Type})
		case "tool-call":
			*p = append(*p, core.Part{"type": "tool_call"})
		case "tool-result":
			*p = append(*p, core.Part{"type": "tool_result"})
		default:
			return fmt.Errorf("unsupported Cursor content type %q", b.Type)
		}
	}
	return nil
}

func indexSelectedFiles(user wireMessage, parts *[]core.Part) error {
	selected, err := user.message(3)
	if err != nil {
		return err
	}
	if selected.has(23) {
		return fmt.Errorf("Cursor video attachments are not yet supported")
	}
	var expansions []string
	for _, field := range []int{1, 4, 25} {
		for _, raw := range selected.all(field) {
			f, err := parseWire(raw)
			if err != nil {
				return err
			}
			kind := "file"
			if field == 1 {
				kind = "image"
			}
			*parts = append(*parts, core.Part{"type": kind})
			if field != 25 {
				continue
			}
			// Match the complete native hash, not just an attachment-looking
			// prefix. This is the same exact-byte normalization as a full read,
			// without querying or decoding any selected-document blob.
			prefix := "[Attached file: " + f.text(3) + "]\n"
			for _, p := range *parts {
				text := core.S(p["text"])
				if p["type"] != "text" || !strings.HasPrefix(text, prefix) {
					continue
				}
				data := []byte(strings.TrimPrefix(text, prefix))
				match := f.has(1) && core.SHA256Hex(data) == hex.EncodeToString(f.bytes(1)) || f.has(8) && string(data) == string(f.bytes(8))
				if match && inlineFile(data, f.text(4)) {
					expansions = append(expansions, text)
					break
				}
			}
		}
	}
	*parts = normalizeExpandedFiles(*parts, user.text(1), expansions)
	return nil
}
