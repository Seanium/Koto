package koto

import (
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/core"
)

// The same selection contract must survive every native reader/writer pair.
// Fixtures exercise the actual copy API; native Agent continuation is verified separately.
func TestCopySelectionTargets(t *testing.T) {
	for _, from := range copyAgents {
		for _, to := range copyAgents {
			t.Run(from+"-to-"+to, func(t *testing.T) {
				requireCopyAgent(t, "claude-code", from)
				requireCopyAgent(t, from, to)
				s := newSandbox(t)
				a := s.app(enable("claude-code", from, to))
				api := newAPI(a)
				file, session := linearMatrixSource(t, s, api, from)
				before := nativeSessionSnapshot(t, file, from, session)
				d, err := api.Thread(ctx, from+":"+session, 0, "")
				must(t, err)
				events := []*core.Event{}
				for _, event := range d.Events {
					if event.Kind == "message" {
						events = append(events, event)
					}
				}
				eq(t, len(events), 8, "four complete native turns")
				for _, test := range []struct {
					name    string
					indices []int
					exclude bool
				}{
					{"all", []int{0, 1, 2, 3, 4, 5, 6, 7}, false},
					{"prefix", []int{0, 1, 2, 3}, false},
					{"middle", []int{2, 3}, false},
					{"last", []int{6, 7}, false},
					{"reply-only", []int{5}, false},
					{"question-only", []int{2}, false},
					{"nonconsecutive-turns", []int{0, 1, 4, 5}, false},
					{"mixed-messages", []int{0, 1, 5}, false},
					{"exclude-tail", []int{4, 5, 6, 7}, true},
				} {
					t.Run(test.name, func(t *testing.T) {
						req := CarryRequest{From: d.Head, To: to, Cwd: s.Root}
						ids := []string{}
						selected := map[int]bool{}
						for _, index := range test.indices {
							ids = append(ids, events[index].ID)
							selected[index] = true
						}
						if test.exclude {
							req.ExcludeEventIDs = ids
						} else {
							req.EventIDs = ids
						}
						r, err := api.Carry(ctx, req)
						must(t, err)
						ok(t, r.Applied)
						out, err := api.Thread(ctx, to+":"+r.Plan.Target.Session, 30, "")
						must(t, err)
						ok(t, out.Turns != nil && *out.Turns > 0, "selected excerpt must remain visible")
						body := core.ToJSON(out.Events)
						for i := range 8 {
							role := "question"
							if i%2 == 1 {
								role = "answer"
							}
							marker := "x " + role + " " + itoa(i/2+1)
							eq(t, strings.Contains(body, marker), selected[i] != test.exclude, marker)
						}
						eq(t, nativeSessionSnapshot(t, file, from, session), before, "source session unchanged")
					})
				}
			})
		}
	}
}

func TestCopyExcludedTailTargets(t *testing.T) {
	for _, to := range copyAgents {
		t.Run(to, func(t *testing.T) {
			requireCopyAgent(t, "claude-code", to)
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
