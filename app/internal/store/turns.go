package store

import (
	"koto/internal/core"
	"koto/internal/hub"
	"strings"
)

// LastTurnComplete: the turn ending at leaf is finished (the Agent answered, no tool call left without a result).
func LastTurnComplete(h *hub.Hub, leaf string) bool {
	ids := h.PathIDs(leaf)
	start := len(ids)
	for i := len(ids) - 1; i >= 0; i-- {
		if h.IsTurnStart(ids[i]) {
			start = i
			break
		}
	}
	calls := map[string]bool{}
	answered := false
	for _, id := range ids[start:] {
		e := h.GetEvent(id)
		if e == nil {
			continue
		}
		for _, p := range e.Parts {
			switch core.S(p["type"]) {
			case "tool_call":
				calls[core.S(p["callId"])] = true
			case "tool_result":
				delete(calls, core.S(p["callId"]))
			}
		}
		if e.Kind == "message" && e.Actor == "assistant" {
			for _, p := range e.Parts {
				if typ := core.S(p["type"]); typ == "image" || typ == "file" || typ == "text" && strings.TrimSpace(core.S(p["text"])) != "" {
					answered = true
				}
			}
		}
	}
	return answered && len(calls) == 0
}
