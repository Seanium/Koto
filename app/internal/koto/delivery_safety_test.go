package koto

import (
	"database/sql"
	"koto/internal/store"
	"testing"
)

func TestCopyHasNoPersistentHistory(t *testing.T) {
	s := newSandbox(t)
	writeCcSession(t, s.Claude, "", false)
	a := s.app(enable("claude-code", "pi"))
	api := newAPI(a)
	api.Reindex(ctx)
	req := CarryRequest{Selector: "claude-code:" + CCSession, To: "pi", RequestID: "attempt"}
	first, err := api.Carry(ctx, req)
	must(t, err)
	again, err := api.Carry(ctx, req)
	must(t, err)
	eq(t, first.Plan.ID, again.Plan.ID)
	eq(t, len(piFilesIn(s.Pi)), 1)
	// Reopening the app retains the actual native session, not its copy operation.
	b := s.app(enable("claude-code", "pi"))
	other := newAPI(b)
	other.Reindex(ctx)
	second, err := other.Carry(ctx, req)
	must(t, err)
	ok(t, second.Plan.ID != first.Plan.ID)
	eq(t, len(piFilesIn(s.Pi)), 2)
	db, err := sql.Open("sqlite", a.Store.File)
	must(t, err)
	defer db.Close()
	var n int
	must(t, db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('copies','copy_ids','version_meta')").Scan(&n))
	eq(t, n, 0)
}
func TestAnalysisCollectionStoresOnlyReferences(t *testing.T) {
	s := newSandbox(t)
	a := s.app(enable("claude-code", "pi"))
	api := newAPI(a)
	c, err := api.SaveAnalysis(store.AnalysisCollection{Name: "实验", Sessions: []string{"claude-code:a", "claude-code:b"}})
	must(t, err)
	c.Name = "比较结果"
	c.Sessions = []string{"claude-code:a", "claude-code:c"}
	_, err = api.SaveAnalysis(c)
	must(t, err)
	b := s.app(enable("claude-code", "pi"))
	list, err := newAPI(b).Analyses()
	must(t, err)
	eq(t, len(list), 1)
	jsonEq(t, list[0], c)
	c.Sessions = []string{"claude-code:a", "pi:b"}
	_, err = api.SaveAnalysis(c)
	ok(t, err != nil)
	must(t, api.DeleteAnalysis(c.ID))
	list, err = api.Analyses()
	must(t, err)
	eq(t, len(list), 0)
	_, err = api.SaveAnalysis(c)
	ok(t, err != nil, "deleted collection cannot be recreated by stale editor")
}
