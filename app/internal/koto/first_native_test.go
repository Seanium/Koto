package koto

import (
	"path/filepath"
	"testing"
)

func TestClaudeMetadataAliasesNeverMergeIndependentSessions(t *testing.T) {
	const CCB = "22222222-3333-4444-8555-666666666666"
	s := newSandbox(t)
	for _, id := range []string{CCA, CCB} {
		records := []any{map[string]any{"type": "mode", "mode": "plan"}}
		records = append(records, ccTurn(id, "", 1, id)...)
		writeLines(t, filepath.Join(ccDir(s.Claude), id+".jsonl"), records)
	}
	a := s.app(enable("claude-code"))
	api := newAPI(a)
	eq(t, len(api.Reindex(ctx).Errors), 0)
	eq(t, len(a.Sessions.Groups()), 2, "metadata alias cannot join different conversations")
	for _, r := range a.Store.Sessions() {
		eq(t, r.FirstNative, r.Session+"u1")
	}
	// A real shared first message UUID still represents a native fork/copy.
	records := []any{map[string]any{"type": "mode", "mode": "plan"}}
	records = append(records, ccTurn(CCB, "", 1, CCA)...)
	writeLines(t, filepath.Join(ccDir(s.Claude), CCB+".jsonl"), records)
	eq(t, len(api.Reindex(ctx).Errors), 0)
	eq(t, len(a.Sessions.Groups()), 1)
	// The same transcript copied under another root remains a separate binding.
	other := filepath.Join(s.Root, "claude-second")
	writeLines(t, filepath.Join(ccDir(other), CCA+".jsonl"), records)
	_, err := api.AddRoot("claude-code", other)
	must(t, err)
	eq(t, len(api.Reindex(ctx).Errors), 0)
	eq(t, len(a.Sessions.Groups()), 2, "shared native ID cannot merge profiles")
}
