package koto

import (
	"slices"
	"strings"
	"testing"

	"koto/internal/core"
)

func TestOpenCodeSourceSelectionTargets(t *testing.T) {
	for _, to := range copyAgents {
		t.Run(to, func(t *testing.T) {
			requireCopyAgent(t, "opencode", to)
			s := newSandbox(t)
			a := s.app(enable("claude-code", "opencode", to))
			api := newAPI(a)
			file, session := openCodeLinearSource(t, s, api)
			before := nativeSessionSnapshot(t, file, "opencode", session)
			d, err := api.Thread(ctx, "opencode:"+session, 0, "")
			must(t, err)
			eq(t, len(d.Events), 8, "four complete native turns")
			for _, test := range []struct {
				name    string
				indices []int
				exclude bool
			}{
				{"reply-only", []int{5}, false},
				{"question-only", []int{2}, false},
				{"nonconsecutive", []int{0, 1, 5}, false},
				{"exclude-tail", []int{4, 5, 6, 7}, true},
			} {
				t.Run(test.name, func(t *testing.T) {
					req := CarryRequest{From: d.Head, To: to, Cwd: s.Root}
					ids := []string{}
					for _, index := range test.indices {
						ids = append(ids, d.Events[index].ID)
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
					ok(t, out.Turns != nil && *out.Turns > 0)
					body := core.ToJSON(out.Events)
					for i := range 8 {
						role := "question"
						if i%2 == 1 {
							role = "answer"
						}
						marker := "x " + role + " " + itoa(i/2+1)
						wanted := slices.Contains(test.indices, i) != test.exclude
						eq(t, strings.Contains(body, marker), wanted, marker)
					}
					eq(t, nativeSessionSnapshot(t, file, "opencode", session), before, "OpenCode source rows unchanged")
				})
			}
		})
	}
}

func TestOpenCodeAPIArchiveRestoreIsolation(t *testing.T) {
	requireCopyAgent(t, "opencode", "opencode")
	s := newSandbox(t)
	a := s.app(enable("claude-code", "opencode"))
	api := newAPI(a)
	file, session := openCodeLinearSource(t, s, api)
	source := "opencode:" + session
	copySession := func() string {
		r, err := api.Carry(ctx, CarryRequest{Selector: source, To: "opencode", Cwd: s.Root})
		must(t, err)
		ok(t, r.Applied)
		return r.Plan.Target.Session
	}
	archived, sibling := copySession(), copySession()
	before := map[string]string{}
	for _, id := range []string{session, archived, sibling} {
		before[id] = nativeSessionSnapshot(t, file, "opencode", id)
	}
	entry, err := api.TrashThread(ctx, "opencode:"+archived, "")
	must(t, err)
	eq(t, len(entry.Items), 1)
	eq(t, entry.Items[0].Via, "archive")
	api.Reindex(ctx)
	ok(t, !slices.Contains(threadIDs(api), "opencode:"+archived), "archived session must stay out of discovery")
	for _, id := range []string{session, sibling} {
		eq(t, nativeSessionSnapshot(t, file, "opencode", id), before[id], "archiving changed another session")
		_, err := api.Thread(ctx, "opencode:"+id, 0, "")
		must(t, err)
	}
	// A session created while another is archived must survive restoration too.
	newcomer := copySession()
	before[newcomer] = nativeSessionSnapshot(t, file, "opencode", newcomer)
	_, err = api.Restore(ctx, entry.TrashID)
	must(t, err)
	api.Reindex(ctx)
	for id, snapshot := range before {
		eq(t, nativeSessionSnapshot(t, file, "opencode", id), snapshot, "restore changed session ", id)
		ok(t, slices.Contains(threadIDs(api), "opencode:"+id), "restored and unrelated sessions must all stay visible")
	}
	eq(t, len(api.TrashList()), 0)
}
