package codex

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"koto/internal/connectors/shared"
	"koto/internal/core"
)

func putDesktopProject(t *testing.T, home, id, host, kind string, roots []string) {
	t.Helper()
	data, _ := json.Marshal(core.JsonObject{
		"local-projects":                     core.JsonObject{"legacy": core.JsonObject{"id": "legacy", "rootPaths": roots}},
		"thread-project-assignments":         core.JsonObject{id: core.JsonObject{"projectKind": kind, "projectId": "legacy"}},
		"thread-project-membership-host-ids": core.JsonObject{id: host},
	})
	if err := os.WriteFile(filepath.Join(home, ".codex-global-state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopProjectDirectoryNeedsExplicitLocalSingleRoot(t *testing.T) {
	home := t.TempDir()
	c := New(Options{Home: home}).Source.(*codexConn)
	root := filepath.Join(home, "新项目 [copy]")
	for _, tc := range []struct {
		name, host, kind string
		roots            []string
		want             string
	}{
		{"local", "local", "local", []string{root}, root},
		{"remote-host", "remote-id", "local", []string{root}, ""},
		{"unknown-host", "", "local", []string{root}, ""},
		{"cloud-project", "local", "cloud", []string{root}, ""},
		{"multi-root", "local", "local", []string{root, root + "-two"}, ""},
		{"relative", "local", "local", []string{"relative"}, ""},
		{"empty", "local", "local", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			putDesktopProject(t, home, "thread", tc.host, tc.kind, tc.roots)
			got := c.currentLocations(context.Background())["thread"]
			if got.cwd != tc.want {
				t.Fatalf("cwd %q want %q", got.cwd, tc.want)
			}
		})
	}
}

func TestSQLProjectOverridesLegacyAndWALChangesRefresh(t *testing.T) {
	home := t.TempDir()
	c := New(Options{Home: home}).Source.(*codexConn)
	old, newRoot, other := filepath.Join(home, "old"), filepath.Join(home, "new"), filepath.Join(home, "new-two")
	putDesktopProject(t, home, "thread", "local", "local", []string{old})
	db, err := sql.Open("sqlite", shared.FileURL(filepath.Join(home, "state_5.sqlite")).String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{`PRAGMA journal_mode=WAL`, `CREATE TABLE threads(id TEXT PRIMARY KEY,cwd TEXT,project_id TEXT)`, `CREATE TABLE projects(id TEXT PRIMARY KEY)`, `CREATE TABLE project_roots(project_id TEXT,position INT,path TEXT)`} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO threads VALUES('thread',?,NULL)`, old); err != nil {
		t.Fatal(err)
	}
	if got := c.currentLocations(context.Background())["thread"]; got.cwd != old || core.S(got.project["source"]) != "codex-desktop-project" {
		t.Fatal(got)
	}
	if _, err = db.Exec(`INSERT INTO projects VALUES('sql'); INSERT INTO project_roots VALUES('sql',0,?)`, newRoot); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE threads SET project_id='sql'`); err != nil {
		t.Fatal(err)
	}
	if got := c.currentLocations(context.Background())["thread"]; got.cwd != newRoot || core.S(got.project["source"]) != "codex-state-project" {
		t.Fatal(got)
	}
	if _, err = db.Exec(`UPDATE project_roots SET path=?`, other); err != nil {
		t.Fatal(err)
	}
	if got := c.currentLocations(context.Background())["thread"].cwd; got != other {
		t.Fatalf("stale WAL cwd %q", got)
	}
	if _, err = db.Exec(`INSERT INTO project_roots VALUES('sql',1,?)`, newRoot); err != nil {
		t.Fatal(err)
	}
	if got := c.currentLocations(context.Background())["thread"]; got.cwd != old || got.project != nil {
		t.Fatalf("ambiguous project guessed: %#v", got)
	}
	putDesktopProject(t, home, "thread", "remote", "local", []string{other})
	if got := c.currentLocations(context.Background())["thread"]; got.cwd != "" {
		t.Fatalf("remote assigned to local root: %#v", got)
	}
}

func TestProjectMetadataSameSizeMtimeChangeIsSeen(t *testing.T) {
	home := t.TempDir()
	c := New(Options{Home: home}).Source.(*codexConn)
	a, b := filepath.Join(home, "project-a"), filepath.Join(home, "project-b")
	putDesktopProject(t, home, "thread", "local", "local", []string{a})
	time.Sleep(1100 * time.Millisecond)
	file := filepath.Join(home, ".codex-global-state.json")
	st, _ := os.Stat(file)
	if c.currentLocations(context.Background())["thread"].cwd != a {
		t.Fatal("first")
	}
	putDesktopProject(t, home, "thread", "local", "local", []string{b})
	if err := os.Chtimes(file, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	if c.currentLocations(context.Background())["thread"].cwd != b {
		t.Fatal("same size/mtime update missed")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if len(c.currentLocations(context.Background())) != 0 {
		t.Fatal("removed assignment retained")
	}
}

func TestTornProjectMetadataDoesNotReplaceValidatedCache(t *testing.T) {
	home := t.TempDir()
	c := New(Options{Home: home}).Source.(*codexConn)
	root := filepath.Join(home, "current")
	putDesktopProject(t, home, "thread", "local", "local", []string{root})
	time.Sleep(1100 * time.Millisecond)
	if c.currentLocations(context.Background())["thread"].cwd != root || c.projects.locations == nil {
		t.Fatal("coherent metadata not cached")
	}
	file := filepath.Join(home, ".codex-global-state.json")
	if err := os.WriteFile(file, []byte(`{"local-projects":`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := c.currentLocations(context.Background())["thread"].cwd; got != root {
		t.Fatalf("torn save erased current root: %q", got)
	}
	if err := os.WriteFile(file, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if len(c.currentLocations(context.Background())) != 0 {
		t.Fatal("real detach retained stale project")
	}
}

func TestSQLScanErrorDoesNotPublishOrCachePartialLocations(t *testing.T) {
	home := t.TempDir()
	c := New(Options{Home: home}).Source.(*codexConn)
	db, err := sql.Open("sqlite", shared.FileURL(filepath.Join(home, "state_5.sqlite")).String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE threads(id TEXT,cwd TEXT); INSERT INTO threads VALUES('one',?),('two',NULL)`, home)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.currentLocations(context.Background()); len(got) != 0 {
		t.Fatalf("partial SQL rows published: %v", got)
	}
	if c.projects.locations != nil {
		t.Fatal("failed SQL snapshot cached")
	}
	_, err = db.Exec(`UPDATE threads SET cwd=? WHERE id='two'`, home)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.currentLocations(context.Background()); len(got) != 2 {
		t.Fatalf("repaired snapshot not retried: %v", got)
	}
}
