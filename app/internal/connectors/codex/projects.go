package codex

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
	_ "modernc.org/sqlite"
)

// Codex project membership is mutable desktop metadata, not a rewrite of the
// rollout's historical cwd. Read only the fields needed to resolve that link.
type desktopProjects struct {
	Projects map[string]struct {
		ID    string   `json:"id"`
		Roots []string `json:"rootPaths"`
	} `json:"local-projects"`
	Assignments map[string]struct {
		Kind string `json:"projectKind"`
		ID   string `json:"projectId"`
	} `json:"thread-project-assignments"`
	Hosts map[string]string `json:"thread-project-membership-host-ids"`
}

type currentLocation struct {
	cwd     string
	project core.JsonObject
}
type projectCache struct {
	mu        sync.Mutex
	witness   string
	locations map[string]currentLocation
}

func metadataWitness(paths ...string) (string, bool) {
	var b strings.Builder
	stable := true
	for _, p := range paths {
		witness, quiet, err := engine.SourceWitness(core.NativeRef{Path: p})
		if err != nil {
			fmt.Fprintf(&b, "%s:missing|", p)
			stable = stable && os.IsNotExist(err)
			continue
		}
		fmt.Fprintf(&b, "%s:%s|", p, witness)
		stable = stable && quiet
	}
	return b.String(), stable
}

func localRoot(roots []string) string {
	if len(roots) != 1 || !filepath.IsAbs(roots[0]) || strings.ContainsRune(roots[0], '\x00') {
		return ""
	}
	return filepath.Clean(roots[0])
}

func (c *codexConn) currentLocations(ctx context.Context) map[string]currentLocation {
	file := filepath.Join(c.home, ".codex-global-state.json")
	database := filepath.Join(c.home, "state_5.sqlite")
	witness, stable := metadataWitness(file, database, database+"-wal")
	c.projects.mu.Lock()
	defer c.projects.mu.Unlock()
	if stable && witness == c.projects.witness && c.projects.locations != nil {
		return c.projects.locations
	}
	locations := map[string]currentLocation{}
	var desktop desktopProjects
	valid := true
	if st, err := os.Stat(file); err == nil {
		valid = st.Mode().IsRegular() && st.Size() <= 32<<20
		if valid {
			data, err := os.ReadFile(file)
			valid = err == nil && json.Unmarshal(data, &desktop) == nil
		}
	} else {
		valid = os.IsNotExist(err)
	}
	if !valid {
		// A torn desktop save is not a detach. Keep the last coherent value,
		// without advancing its witness, and retry on the next pass.
		if c.projects.locations != nil {
			return c.projects.locations
		}
		desktop = desktopProjects{}
	}
	projects := map[string]string{}
	for id, p := range desktop.Projects {
		if p.ID == "" || p.ID == id {
			projects[id] = localRoot(p.Roots)
		}
	}
	for thread, a := range desktop.Assignments {
		if a.Kind != "local" || desktop.Hosts[thread] != "local" {
			continue
		}
		if cwd := projects[a.ID]; cwd != "" {
			locations[thread] = currentLocation{cwd, core.JsonObject{"id": a.ID, "cwd": cwd, "source": "codex-desktop-project"}}
		}
	}
	// The local app-server's database is optional. Never create it or read auth
	// metadata. An unavailable/new schema leaves the explicit desktop mapping.
	if st, err := os.Stat(database); err == nil && st.Mode().IsRegular() {
		readCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
		defer cancel()
		u := shared.FileURL(database)
		q := u.Query()
		q.Set("mode", "ro")
		q.Add("_pragma", "busy_timeout(200)")
		u.RawQuery = q.Encode()
		if db, err := sql.Open("sqlite", u.String()); err == nil {
			db.SetMaxOpenConns(1)
			defer db.Close()
			tx, err := db.BeginTx(readCtx, &sql.TxOptions{ReadOnly: true})
			if err == nil {
				defer tx.Rollback()
				valid = c.readSQLLocations(readCtx, tx, desktop.Hosts, locations) && valid
				if valid {
					valid = tx.Commit() == nil
				}
			} else {
				valid = false
			}
		} else {
			valid = false
		}
	}
	// A concurrent desktop save is retried next pass, not cached as authoritative.
	after, quiet := metadataWitness(file, database, database+"-wal")
	if valid && ctx.Err() == nil && quiet && after == witness {
		c.projects.witness, c.projects.locations = witness, locations
	}
	return locations
}

func (c *codexConn) readSQLLocations(ctx context.Context, db *sql.Tx, hosts map[string]string, locations map[string]currentLocation) bool {
	rows, err := db.QueryContext(ctx, `SELECT id,cwd,project_id FROM threads`)
	hasProjects := err == nil
	if err != nil {
		rows, err = db.QueryContext(ctx, `SELECT id,cwd,NULL FROM threads`)
	}
	if err != nil {
		return false
	}
	type thread struct{ id, cwd, project string }
	var threads []thread
	for rows.Next() {
		var id, cwd string
		var project sql.NullString
		if rows.Scan(&id, &cwd, &project) != nil {
			rows.Close()
			return false
		}
		threads = append(threads, thread{id, cwd, project.String})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false
	}
	roots := map[string][]string{}
	if hasProjects {
		if rows, err := db.QueryContext(ctx, `SELECT r.project_id,r.path FROM project_roots r JOIN projects p ON p.id=r.project_id ORDER BY r.project_id,r.position`); err == nil {
			for rows.Next() {
				var id, path string
				if rows.Scan(&id, &path) != nil {
					rows.Close()
					return false
				}
				roots[id] = append(roots[id], path)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return false
			}
		} else {
			for _, t := range threads {
				if t.project != "" {
					return false
				}
			}
		}
	}
	for _, t := range threads {
		if host := hosts[t.id]; host != "" && host != "local" {
			delete(locations, t.id)
			continue
		}
		if t.project != "" {
			// Explicit SQL membership supersedes a stale legacy assignment. Only
			// a proven local host and one root authorize a project override.
			delete(locations, t.id)
			if hosts[t.id] == "local" {
				if cwd := localRoot(roots[t.project]); cwd != "" {
					locations[t.id] = currentLocation{cwd, core.JsonObject{"id": t.project, "cwd": cwd, "source": "codex-state-project"}}
				}
			}
		}
		if _, ok := locations[t.id]; !ok && localRoot([]string{t.cwd}) != "" {
			locations[t.id] = currentLocation{cwd: filepath.Clean(t.cwd)}
		}
	}
	return true
}
