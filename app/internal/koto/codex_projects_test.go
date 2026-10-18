package koto

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/store"
)

type projectCountSource struct {
	engine.Source
	tails atomic.Int64
}

func (s *projectCountSource) Tail(ctx context.Context, r core.NativeRef, o *core.SourceOffset, y func(core.SourceBatch) error) error {
	s.tails.Add(1)
	return s.Source.Tail(ctx, r, o, y)
}

func TestCodexProjectMoveUpdatesNavigationAndCopyWithoutReparse(t *testing.T) {
	s := newSandbox(t)
	file := writeCodexRollout(t, s.Codex, "", nil)
	before, err := os.ReadFile(file)
	must(t, err)
	oldTime := time.Now().Add(-time.Hour)
	must(t, os.Chtimes(file, oldTime, oldTime))
	// The ctime witness must also become stable before testing stat-only scans.
	time.Sleep(1100 * time.Millisecond)
	a := s.app(enable("codex", "pi"))
	c := a.Registry.Get("codex")
	count := &projectCountSource{Source: c.Source}
	c.Source = count
	ctx := context.Background()
	report := a.ScanIndex(ctx, nil)
	eq(t, len(report.Errors), 0)
	selector := core.SessionSelector(c.ID, c.Harness, CodexThread)
	view, err := a.Sessions.OpenThread(ctx, selector)
	must(t, err)
	row, _ := a.Store.Session(c.Harness, CodexThread, c.ID)
	historical := core.S(row.Meta["cwd"])
	revision := store.RowRevision(row)
	if historical == "" {
		t.Fatal("fixture has no historical cwd")
	}
	initialTails := count.tails.Load()
	writeProject := func(dir string) {
		data, _ := json.Marshal(core.JsonObject{"local-projects": core.JsonObject{"project": core.JsonObject{"id": "project", "rootPaths": []string{dir}}}, "thread-project-assignments": core.JsonObject{CodexThread: core.JsonObject{"projectKind": "local", "projectId": "project"}}, "thread-project-membership-host-ids": core.JsonObject{CodexThread: "local"}})
		must(t, os.WriteFile(filepath.Join(s.Codex, ".codex-global-state.json"), data, 0600))
	}
	current := filepath.Join(s.Root, "迁移后的项目 [copy]")
	must(t, os.MkdirAll(current, 0700))
	writeProject(current)
	report = a.ScanIndex(ctx, nil)
	eq(t, len(report.Errors), 0)
	ok(t, report.Changed > 0, "metadata change not published")
	row, _ = a.Store.Session(c.Harness, CodexThread, c.ID)
	eq(t, row.Cwd, current)
	eq(t, core.S(row.Meta["cwd"]), historical)
	eq(t, store.RowRevision(row), revision)
	cached, err := a.Sessions.OpenThread(ctx, selector)
	must(t, err)
	eq(t, cached, view, "project move evicted message view")
	eq(t, count.tails.Load(), initialTails, "metadata-only change reread rollout")
	b := a.WithResume(*view.Hub.Binding(c.Harness, CodexThread, c.ID))
	eq(t, core.S(b.Meta["cwd"]), current)
	eq(t, core.S(b.Meta["historicalCwd"]), historical)
	api := &API{App: a}
	r, err := api.Carry(ctx, CarryRequest{From: selector, To: "pi", DryRun: true})
	must(t, err)
	eq(t, r.Plan.Target.Cwd, current, "copy default followed old event cwd")
	explicit := filepath.Join(s.Root, "chosen")
	r, err = api.Carry(ctx, CarryRequest{From: selector, To: "pi", Cwd: explicit, DryRun: true})
	must(t, err)
	eq(t, r.Plan.Target.Cwd, explicit)
	// Detaching the project restores the historical directory, without stale
	// project metadata sticking in the persistent summary or cached detail.
	must(t, os.Remove(filepath.Join(s.Codex, ".codex-global-state.json")))
	report = a.ScanIndex(ctx, nil)
	eq(t, len(report.Errors), 0)
	row, _ = a.Store.Session(c.Harness, CodexThread, c.ID)
	eq(t, row.Cwd, historical)
	b = a.WithResume(*view.Hub.Binding(c.Harness, CodexThread, c.ID))
	eq(t, core.S(b.Meta["cwd"]), historical)
	after, err := os.ReadFile(file)
	must(t, err)
	eq(t, core.SHA256Hex(after), core.SHA256Hex(before), "source changed")
}
