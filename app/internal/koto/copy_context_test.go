package koto

import (
	"strings"
	"testing"

	"koto/internal/core"
)

// Reproduce selecting the first visible turn when native context was included in its IDs.
// Context may sit between the question and reply, or between two turns (model changes).
func TestCopySelectedTurnWithContext(t *testing.T) {
	for _, from := range []string{"claude-code", "codex"} {
		for _, to := range copyAgents {
			t.Run(from+"/"+to, func(t *testing.T) {
				requireCopyAgent(t, from, to)
				s := newSandbox(t)
				var file, session, wanted, omitted string
				if from == "claude-code" {
					file = writeCcSession(t, s.Claude, "", false)
					session, wanted, omitted = CCSession, "The project has a README describing a demo.", "Thanks! Now summarize it in one line."
				} else {
					file = writeCodexRollout(t, s.Codex, "", nil)
					session, wanted, omitted = CodexThread, "All 12 tests pass.", "Thanks, apply a patch to fix lint"
				}
				before := text(t, file)
				a := s.app(enable(from, to))
				api := newAPI(a)
				api.Reindex(ctx)
				d, err := api.Thread(ctx, from+":"+session, 0, "")
				must(t, err)
				ids, contexts, contextText := []string{}, []string{}, []string{}
				started := false
				for _, ev := range d.Events {
					if ev.Kind == "message" && ev.Actor == "user" {
						if started {
							break
						}
						started = true
					}
					if !started {
						continue
					}
					ids = append(ids, ev.ID)
					if ev.Kind == "context" {
						contexts = append(contexts, ev.ID)
						for _, p := range ev.Parts {
							if txt := core.S(p["text"]); txt != "" {
								contextText = append(contextText, txt)
							}
						}
					}
				}
				ok(t, len(contexts) > 0, "fixture must include hidden context inside the selected turn")
				req := CarryRequest{From: d.Head, To: to, Cwd: s.Root, EventIDs: ids, RequestID: "context-turn", DryRun: true}
				_, err = api.Carry(ctx, req)
				must(t, err)
				req.DryRun = false
				r, err := api.Carry(ctx, req)
				must(t, err)
				ok(t, r.Applied)
				out, err := api.Thread(ctx, to+":"+r.Plan.Target.Session, 30, "")
				must(t, err)
				// Text-only tool conversions can create extra native messages; check actual
				// selected content rather than assuming identical target turn numbering.
				ok(t, out.CompleteTurns > 0, "copied conversation is readable")
				body := core.ToJSON(out.Events)
				ok(t, strings.Contains(body, wanted), "selected reply survives")
				ok(t, !strings.Contains(body, omitted), "unselected next turn stays excluded")
				for _, txt := range contextText {
					encoded := strings.Trim(core.ToJSON(txt), "\"")
					ok(t, !strings.Contains(body, encoded), "source system context must not be copied")
				}
				// Filtering must still reject context-only and stale selections, never copy all.
				_, err = api.Carry(ctx, CarryRequest{From: d.Head, To: to, EventIDs: contexts, DryRun: true})
				ok(t, err != nil, "context-only selection rejected")
				_, err = api.Carry(ctx, CarryRequest{From: d.Head, To: to, EventIDs: append(contexts, "missing-event"), DryRun: true})
				ok(t, err != nil, "unknown event is not silently ignored")
				eq(t, text(t, file), before, "source untouched")
			})
		}
	}
}
