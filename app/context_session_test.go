package main

import (
	"encoding/json"
	"testing"
)

func TestContextSessionID(t *testing.T) {
	for _, id := range []string{"pi:70357469-4e4e-559e-9bbf-7ab6f0695b2f", "codex:019e7300-9021-7654-8543-cc773bdba03a", "opencode:ses_65ec7fbd2f28dfe22265f6a1c5", `agent:中文/"quoted"\path`} {
		encoded, _ := json.Marshal(id)
		got, err := contextSessionID("  " + string(encoded) + " ")
		if err != nil || got != id {
			t.Fatalf("context ID changed: got %q, want %q, error %v", got, id, err)
		}
	}
	for _, invalid := range []string{"", `""`, "pi:70357469/**/-4e4e", "null", `{ "thread": "pi:1" }`} {
		if _, err := contextSessionID(invalid); err == nil {
			t.Fatalf("accepted invalid context data: %q", invalid)
		}
	}
}
