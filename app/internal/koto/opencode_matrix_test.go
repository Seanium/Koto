package koto

import (
	"slices"
	"testing"
)

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
	ok(t, !slices.Contains(threadIDs(api), "@"+a.Registry.Get("opencode").ID+":"+archived), "archived session must stay out of discovery")
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
		ok(t, slices.Contains(threadIDs(api), "@"+a.Registry.Get("opencode").ID+":"+id), "restored and unrelated sessions must all stay visible")
	}
	eq(t, len(api.TrashList()), 0)
}
