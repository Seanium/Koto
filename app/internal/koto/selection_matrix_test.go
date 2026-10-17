package koto

import (
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/core"
)

// A selection may start with an answer or end with a question. Native formats, including Codex rollout records, must preserve the selected content without silently bringing omitted turns back.
func TestCopySelectionTargets(t *testing.T) {
	for _, to := range []string{"claude-code", "pi", "codex", "dsh", "antigravity"} {
		t.Run(to, func(t *testing.T) {
			if to == "codex" && !hasCodex() {
				t.Skip("codex CLI not installed")
			}
			s := newSandbox(t)
			file := filepath.Join(ccDir(s.Claude), CCA+".jsonl")
			writeLines(t, file, ccLinear(CCA, 4, ""))
			before := text(t, file)
			a := s.app(enable("claude-code", to))
			api := newAPI(a)
			api.Reindex(ctx)
			d, err := api.Thread(ctx, "claude-code:"+CCA, 0, "")
			must(t, err)
			for _, indices := range [][]int{{5}, {2}, {0, 1, 5}} {
				ids := []string{}
				selected := map[int]bool{}
				for _, i := range indices {
					ids = append(ids, d.Events[i].ID)
					selected[i] = true
				}
				r, err := api.Carry(ctx, CarryRequest{From: d.Head, To: to, Cwd: s.Root, EventIDs: ids})
				must(t, err)
				ok(t, r.Applied)
				native := core.S(r.Receipt["session"])
				if native == "" {
					native = r.Plan.Target.Session
				}
				out, err := api.Thread(ctx, to+":"+native, 30, "")
				must(t, err)
				ok(t, out.Turns != nil && *out.Turns > 0, "reply-only excerpt must remain visible in paged views")
				if len(indices) == 1 && indices[0] == 5 {
					eq(t, out.CompleteTurns, 1, "reply-only excerpt is complete")
				}
				body := core.ToJSON(out.Events)
				for i := range 8 {
					role := "question"
					if i%2 == 1 {
						role = "answer"
					}
					marker := "x " + role + " " + itoa(i/2+1)
					eq(t, strings.Contains(body, marker), selected[i], marker)
				}
			}
			eq(t, text(t, file), before)
		})
	}
}

func TestCopyExcludedTailTargets(t *testing.T) {
	for _, to := range []string{"claude-code", "pi", "codex", "dsh", "antigravity"} {
		t.Run(to, func(t *testing.T) {
			if to == "codex" && !hasCodex() {
				t.Skip("codex CLI not installed")
			}
			s := newSandbox(t)
			file := filepath.Join(ccDir(s.Claude), CCA+".jsonl")
			writeLines(t, file, ccLinear(CCA, 4, ""))
			a := s.app(enable("claude-code", to))
			api := newAPI(a)
			api.Reindex(ctx)
			d, err := api.Thread(ctx, "claude-code:"+CCA, 0, "")
			must(t, err)
			req := CarryRequest{From: d.Head, To: to, Cwd: s.Root, ExcludeEventIDs: []string{d.Events[4].ID, d.Events[5].ID, d.Events[6].ID, d.Events[7].ID}, RequestID: "excluded-tail", DryRun: true}
			_, err = api.Carry(ctx, req)
			must(t, err)
			writeLines(t, file, ccLinear(CCA, 5, "")) // source grows after selection and preview
			before := text(t, file)
			req.DryRun = false
			r, err := api.Carry(ctx, req)
			must(t, err)
			ok(t, r.Applied)
			native := core.S(r.Receipt["session"])
			if native == "" {
				native = r.Plan.Target.Session
			}
			out, err := api.Thread(ctx, to+":"+native, 30, "")
			must(t, err)
			eq(t, *out.Turns, 2)
			body := core.ToJSON(out.Events)
			for i := 1; i <= 5; i++ {
				for _, role := range []string{"question", "answer"} {
					eq(t, strings.Contains(body, "x "+role+" "+itoa(i)), i <= 2)
				}
			}
			again, err := api.Carry(ctx, req)
			must(t, err)
			eq(t, core.S(again.Receipt["session"]), native, "retry reuses receipt")
			eq(t, text(t, file), before, "source remains unchanged")
		})
	}
}

func TestCopyInvalidExclusions(t *testing.T) {
	s := newSandbox(t)
	writeLines(t, filepath.Join(ccDir(s.Claude), CCA+".jsonl"), ccLinear(CCA, 2, ""))
	a := s.app(enable("claude-code"))
	api := newAPI(a)
	api.Reindex(ctx)
	d, err := api.Thread(ctx, "claude-code:"+CCA, 0, "")
	must(t, err)
	all := []string{}
	for _, ev := range d.Events {
		all = append(all, ev.ID)
	}
	for _, excluded := range [][]string{all, {"missing-event"}} {
		_, err := api.Carry(ctx, CarryRequest{From: d.Head, To: "claude-code", ExcludeEventIDs: excluded})
		ok(t, err != nil, "empty or stale selection rejected")
	}
	_, err = api.Carry(ctx, CarryRequest{From: d.Head, To: "claude-code", EventIDs: all, ExcludeEventIDs: all})
	ok(t, err != nil, "ambiguous selection rejected")
}
