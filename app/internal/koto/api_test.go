package koto

import (
	"context"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"testing"
	"time"

	"koto/internal/engine"
)

func TestAPIThreadsCarryAndIndexWatch(t *testing.T) {
	s := newSandbox(t)
	file := writeCcSession(t, s.Claude, "", false)
	a := s.app(enable("claude-code", "pi"))
	var mu sync.Mutex
	var cycles []Event
	api := &API{App: a, Emit: func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		if e["type"] == "cycle" {
			cycles = append(cycles, e)
		}
	}}
	wctx, stop := context.WithCancel(ctx)
	defer stop()
	go api.Watch(wctx, 100*time.Millisecond)
	time.Sleep(200 * time.Millisecond)

	threads := api.Threads(0)
	eq(t, len(threads), 1)
	sel := "claude-code:" + CCSession
	shown, err := api.Thread(ctx, sel, 0, "")
	must(t, err)
	ok(t, len(shown.Events) > 3)
	page, err := api.Thread(ctx, sel, 1, "")
	must(t, err)
	ok(t, *page.Total == len(shown.Events) && len(page.Events) < len(shown.Events), "a page from the end")

	dry, err := api.Carry(ctx, CarryRequest{Selector: sel, To: "pi", DryRun: true})
	must(t, err)
	ok(t, !dry.Applied)
	eq(t, dry.Plan.Mode, "create")
	_, err = api.Carry(ctx, CarryRequest{To: "pi"})
	status, _ := apiCode(err)
	eq(t, status, 400)

	// a copy, then a new turn in the source: the watcher re-reads the index; nothing is written on its own
	done, err := api.Carry(ctx, CarryRequest{Selector: sel, To: "pi"})
	must(t, err)
	ok(t, done.Applied)
	eq(t, len(piFilesIn(s.Pi)), 1)
	appendLines(t, file, ccTurn(CCSession, "a5", 1, ""))
	changed := func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range cycles {
			if r, ok := c["report"].(engine.ScanReport); ok && r.Changed > 0 {
				return true
			}
		}
		return false
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && !changed(); {
		time.Sleep(50 * time.Millisecond)
	}
	ok(t, changed(), "index cycles see the change")
	eq(t, len(piFilesIn(s.Pi)), 1, "no automatic delivery")
	list := api.Threads(0)
	eq(t, len(list), 2)
	var hs []string
	for _, row := range list {
		for _, b := range row.Bindings {
			hs = append(hs, b.Harness)
		}
	}
	sort.Strings(hs)
	jsonEq(t, hs, []string{"claude-code", "pi"}, "the copy is a separate native session")
}

func TestAPIConnectorRootsPersist(t *testing.T) {
	s := newSandbox(t)
	extraRoot := filepath.Join(s.Root, "claude-2")
	writeCcSession(t, extraRoot, "", true)
	a := s.app(enable("claude-code", "pi"))
	api := newAPI(a)
	type row struct {
		ID, Root  string
		Enabled   bool
		Removable bool
	}
	rows := func(api *API) []row {
		var out []row
		for _, c := range api.Connectors() {
			out = append(out, row{c.ID, c.Root, c.Enabled, c.Removable})
		}
		return out
	}
	jsonEq(t, rows(api), []row{{"claude-code", s.Claude, true, false}, {"pi", s.Pi, true, false}})

	// extra storage root: validated, registered at once, read
	for _, bad := range [][2]string{{"claude-code", filepath.Join(s.Root, "missing")}, {"nope", extraRoot}, {"claude-code", s.Claude}} {
		_, err := api.AddRoot(bad[0], bad[1])
		status, _ := apiCode(err)
		eq(t, status, 400, bad)
	}
	added, err := api.AddRoot("claude-code", extraRoot)
	must(t, err)
	ok(t, regexp.MustCompile(`^claude-code@[0-9a-f]{8}$`).MatchString(added.ID), added.ID)
	ok(t, added.Removable)
	api.Reindex(ctx)
	sel := "claude-code:" + CCSession
	row1, _ := a.Store.Session("claude-code", CCSession)
	eq(t, row1.Connector, added.ID)

	// disabling a sink: copies to it are refused
	off, err := api.SetConnectorEnabled("pi", false)
	must(t, err)
	jsonEq(t, []any{off.Enabled, off.Status}, []any{false, "disabled"})
	_, err = api.Carry(ctx, CarryRequest{Selector: sel, To: "pi"})
	ok(t, err != nil)
	ok(t, api.RemoveRoot(ctx, "claude-code") != nil, "built-ins are disabled, not removed")
	a.Close()

	a = s.app(enable("claude-code", "pi"))
	api = newAPI(a)
	var ids []string
	for _, c := range api.Connectors() {
		ids = append(ids, c.ID+":"+map[bool]string{true: "on", false: "off"}[c.Enabled])
	}
	jsonEq(t, ids, []string{"claude-code:on", "pi:off", added.ID + ":on"})
	_, err = api.SetConnectorEnabled("pi", true)
	must(t, err)
	api.Reindex(ctx)
	_, err = api.Carry(ctx, CarryRequest{Selector: sel, To: "pi"})
	must(t, err)

	// remove the extra root: gone now and after a restart
	must(t, api.RemoveRoot(ctx, added.ID))
	a.Close()
	a = s.app(enable("claude-code", "pi"))
	ids = nil
	for _, c := range newAPI(a).Connectors() {
		ids = append(ids, c.ID)
	}
	jsonEq(t, ids, []string{"claude-code", "pi"})
}

// A copy's dry run in one conversation never reads other conversations into its working copy.
func TestAPIWorkingCopyStaysOwn(t *testing.T) {
	s := newSandbox(t)
	writeCcSession(t, s.Claude, "", true)
	writePiSession(t, s.Pi, "")
	a := s.app(enable("claude-code", "pi"))
	api := newAPI(a)
	api.Reindex(ctx)
	ls := api.Threads(0)
	eq(t, len(ls), 2)
	tree, err := api.Thread(ctx, "claude-code:"+CCSession, 1, "")
	must(t, err)
	_, err = api.Carry(ctx, CarryRequest{From: tree.Head, To: "pi", DryRun: true})
	must(t, err)
	t2, err := api.Thread(ctx, "pi:"+PISession, 1, "")
	must(t, err)
	d, err := api.Thread(ctx, t2.Head, 30, "")
	must(t, err)
	eq(t, d.Thread, t2.Thread, "a node is found in its own conversation")
	for _, v := range a.Sessions.Views() {
		if v.Group.ID == tree.Thread {
			ok(t, v.Hub.Binding("pi", PISession) == nil, "the other session was read into this working copy")
		}
	}
}
