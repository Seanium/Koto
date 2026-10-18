package codex

import (
	"bytes"
	"encoding/json"

	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
)

// ParseIndex leaves tool bodies, image data and assistant output in the input.
// Only fields consumed by the summary decoder are materialized.
func (Decoder) ParseIndex(raw []byte) (any, error) {
	fields, err := shared.IndexJSONFields(raw)
	if err != nil {
		return nil, err
	}
	row, err := shared.IndexJSONValues(fields, "type", "timestamp", "ordinal")
	if err != nil {
		return nil, err
	}
	payload, ok := fields["payload"]
	if !ok {
		return row, nil
	}
	pf, err := shared.IndexJSONFields(payload)
	if err != nil {
		return nil, err
	}
	p, err := shared.IndexJSONValues(pf, "type", "role", "phase", "id", "cwd", "model", "model_provider", "effort", "source", "originator", "cli_version", "creator_account_id", "creator_user_id", "forked_from_id", "forked_from_ordinal_exclusive", "history_base", "parent_thread_id")
	if err != nil {
		return nil, err
	}
	if p["type"] == "message" && p["role"] == "user" {
		if raw, ok := pf["content"]; ok {
			content, err := parseIndexContent(raw)
			if err != nil {
				return nil, err
			}
			p["content"] = content
		}
		if raw, ok := pf["internal_chat_message_metadata_passthrough"]; ok {
			meta, err := shared.IndexJSONFields(raw)
			if err != nil {
				return nil, err
			}
			metadata, err := shared.IndexJSONValues(meta, "content_item_kinds")
			if err != nil {
				return nil, err
			}
			p["internal_chat_message_metadata_passthrough"] = metadata
		}
	}
	row["payload"] = p
	return row, nil
}

func parseIndexContent(raw []byte) (any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var text string
		err := json.Unmarshal(raw, &text)
		return text, err
	}
	parts, err := shared.IndexJSONElements(raw)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(parts))
	for _, raw := range parts {
		fields, err := shared.IndexJSONFields(raw)
		if err != nil {
			return nil, err
		}
		p, err := shared.IndexJSONValues(fields, "type")
		if err != nil {
			return nil, err
		}
		switch p["type"] {
		case "input_text", "output_text", "text":
			text, err := shared.IndexJSONValues(fields, "text")
			if err != nil {
				return nil, err
			}
			for k, v := range text {
				p[k] = v
			}
		}
		out = append(out, p)
	}
	return out, nil
}

func indexContentItems(content any, assetDir string) []core.Part {
	if s, ok := content.(string); ok {
		if s == "" {
			return nil
		}
		return shared.DecodeAttachmentTextIndex(s, assetDir)
	}
	var out []core.Part
	for _, v := range core.Arr(content) {
		p := core.Obj(v)
		switch core.S(p["type"]) {
		case "input_text", "output_text", "text":
			if s, ok := p["text"].(string); ok {
				out = append(out, shared.DecodeAttachmentTextIndex(s, assetDir)...)
			}
		case "input_image":
			out = append(out, core.Part{"type": "image"})
		case "input_audio":
			out = append(out, core.Part{"type": "file"})
		}
	}
	return out
}

func indexResponseItem(p core.JsonObject, kind string, ev evFn, model func() map[string]any) (engine.DecodeOut, bool) {
	switch kind {
	case "message":
		if core.S(p["role"]) == "user" {
			return engine.DecodeOut{}, false
		}
		if core.S(p["role"]) == "assistant" {
			a := core.JsonObject{"model": model()}
			if phase := core.S(p["phase"]); phase != "" {
				a["phase"] = phase
			}
			return ev("message", "assistant", nil, a, nil), true
		}
		return ev("context", "system", nil, core.JsonObject{"role": p["role"]}, nil), true
	case "reasoning":
		return ev("reasoning", "assistant", nil, core.JsonObject{"model": model()}, nil), true
	case "function_call", "custom_tool_call", "local_shell_call":
		return ev("tool_call", "assistant", nil, core.JsonObject{"model": model(), "codexType": kind}, nil), true
	case "function_call_output", "custom_tool_call_output", "local_shell_call_output":
		return ev("tool_result", "tool", nil, nil, nil), true
	case "web_search_call":
		return ev("message", "assistant", nil, core.JsonObject{"model": model(), "codexType": kind}, nil), true
	case "agent_message":
		return ev("x-codex-agent-message", "harness", nil, core.JsonObject{"codexType": kind}, nil), true
	case "tool_search_call", "tool_search_output":
		return ev("context", "harness", nil, core.JsonObject{"codexType": kind}, nil), true
	default:
		return ev("message", "harness", nil, nil, nil), true
	}
}
