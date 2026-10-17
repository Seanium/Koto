package koto

import (
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/core"
)

// Copy the second turn of a four-turn session through the same preview/apply API as the UI.
// Read the target back from its native file: a success receipt alone cannot prove the boundary.
func TestCopyPrefixStopsAtSelectedTurn(t *testing.T) {
	for _, to := range copyAgents {
		t.Run(to, func(t *testing.T) {
			requireCopyAgent(t, "claude-code", to)
			s := newSandbox(t)
			file := filepath.Join(ccDir(s.Claude), CCA+".jsonl")
			writeLines(t, file, ccLinear(CCA, 4, ""))
			a := s.app(enable("claude-code", to))
			api := newAPI(a)
			api.Reindex(ctx)
			d, err := api.Thread(ctx, "claude-code:"+CCA, 30, "")
			must(t, err)
			eq(t, d.CompleteTurns, 4)
			req := CarryRequest{From: d.Events[3].ID, To: to, Cwd: s.Root, RequestID: "stop-at-two", DryRun: true}
			p, err := api.Carry(ctx, req)
			must(t, err)
			eq(t, p.Plan.Head, req.From)
			// A source refresh between preview and apply must not move the frozen cutoff.
			appendLines(t, file, ccTurn(CCA, "xa4", 5, ""))
			before := text(t, file)
			api.Reindex(ctx)
			req.DryRun = false
			result, err := api.Carry(ctx, req)
			must(t, err)
			ok(t, result.Applied)
			out, err := api.Thread(ctx, to+":"+result.Plan.Target.Session, 30, "")
			must(t, err)
			eq(t, out.CompleteTurns, 2)
			body := core.ToJSON(out.Events)
			for n := 1; n <= 5; n++ {
				for _, role := range []string{"question", "answer"} {
					marker := "x " + role + " " + itoa(n)
					eq(t, strings.Contains(body, marker), n <= 2, marker)
				}
			}
			eq(t, text(t, file), before)
		})
	}
}
