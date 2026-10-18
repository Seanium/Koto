package koto

import (
	"fmt"
	"path/filepath"
	"testing"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/store"
)

func TestIndexedSessionSearch(t *testing.T) {
	s := newSandbox(t)
	a := s.app(enable("claude-code"))
	for i := range 510 {
		a.Store.PutSession(store.SessionRow{Harness: "claude-code", Connector: a.Registry.Get("claude-code").ID, Session: fmt.Sprintf("search-%04d-ABC", i), Path: filepath.Join(s.Claude, fmt.Sprintf("missing-%d.jsonl", i)), Title: "indexed title", Cwd: CWD, Turns: 2, Updated: float64(i), Meta: engine.ProfileMeta(a.Registry.Get("claude-code"), core.JsonObject{})})
	}
	a.Store.PutSession(store.SessionRow{Harness: "claude-code", Connector: a.Registry.Get("claude-code").ID, Session: "desktop-old", Title: "Desktop fixture", Cwd: "/work/elsewhere", Turns: 1, Updated: -1, Meta: engine.ProfileMeta(a.Registry.Get("claude-code"), core.JsonObject{"frontend": "claude-desktop"})})
	eq(t, len(newAPI(a).Threads(500)), 500)
	// These nonexistent native files cannot be opened: search must use only metadata.
	for _, query := range []string{"CLAUDE-CODE:SEARCH-0000-ABC", "0000-aBc", " search-0000 "} {
		rows := a.SearchThreads(query, "all", "", 500)
		eq(t, len(rows), 1)
		eq(t, rows[0].ID, core.SessionSelector(a.Registry.Get("claude-code").ID, "claude-code", "search-0000-ABC"))
	}
	eq(t, len(a.SearchThreads("indexed title", "claude-code", CWD, 500)), 500)
	eq(t, len(a.SearchThreads("search-0000", "pi", "", 500)), 0)
	eq(t, len(a.SearchThreads("search-0000", "all", "/wrong", 500)), 0)
	eq(t, len(a.SearchThreads("desktop", "claude-desktop", "", 500)), 1)
	eq(t, len(a.SearchThreads("desktop", "claude-cli", "", 500)), 0)
	eq(t, len(a.SearchThreads("elsewhere", "all", "", 500)), 1)
}
