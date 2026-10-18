// Package zcode implements ZCode's native SQLite transcript (CLI 0.16.9).
// Session storage is authoritative; the desktop task index is synchronized when present.
package zcode

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"koto/internal/core"
	"koto/internal/engine"
	_ "modernc.org/sqlite"
)

const Version = "zcode-connector@0.1.0"

var Capabilities = core.Capabilities{Read: core.ReadCaps{Images: true, Reasoning: "plain", Subagents: false, Compaction: false}, Write: &core.WriteCaps{Mode: "native-file", Images: true, Reasoning: "plain", Tools: "canonical"}, FormatVersions: []string{"zcode-sqlite-v1"}}

type Options struct {
	Root, ID, DBPath string
	Command          []string
}
type connector struct {
	id, root, db, index string
	command             []string
}

func New(o Options) *engine.Connector {
	if o.ID == "" {
		o.ID = "zcode"
	}
	explicit := o.Root != ""
	if o.Root == "" {
		o.Root = os.Getenv("ZCODE_STORAGE_DIR")
	}
	if o.Root == "" {
		base := os.Getenv("ZCODE_DATA_BASE_DIR")
		if base == "" {
			base, _ = os.UserHomeDir()
		}
		o.Root = filepath.Join(base, ".zcode")
	}
	if !filepath.IsAbs(o.Root) {
		if root, err := filepath.Abs(o.Root); err == nil {
			o.Root = root
		}
	}
	if o.DBPath == "" && !explicit {
		o.DBPath = os.Getenv("ZCODE_SESSION_DB_PATH")
		if o.DBPath == "" {
			o.DBPath = os.Getenv("ZCODE_SESSION_DB")
		}
	}
	if o.DBPath == "" {
		o.DBPath = filepath.Join(o.Root, "cli", "db", "db.sqlite")
	}
	if !filepath.IsAbs(o.DBPath) {
		o.DBPath, _ = filepath.Abs(o.DBPath)
	}
	c := &connector{id: o.ID, root: o.Root, db: o.DBPath, index: filepath.Join(o.Root, "v2", "tasks-index.sqlite"), command: append([]string{}, o.Command...)}
	return &engine.Connector{ID: c.id, Root: c.root, Harness: "zcode", Version: Version, Capabilities: Capabilities, Vocabulary: Vocabulary{}, Source: c, Sink: c, Trash: c}
}
func (c *connector) DatabasePath() string { return c.db }
func (c *connector) WatchRoots() []string {
	return []string{filepath.Dir(c.db), filepath.Dir(c.index), filepath.Join(c.root, "cli", "artifacts")}
}
func (c *connector) NewSessionID(key string) string {
	return "sess_" + core.UUID5("zcode\x00"+key)
}
func (c *connector) ResumeCommand() string {
	launch, err := c.nativeLaunch()
	if err == nil && len(launch.argv) == 1 && !strings.ContainsAny(launch.argv[0], " \t\r\n") {
		return launch.argv[0] + " --resume {id}"
	}
	return "" // Bundled paths require NativeResume's argv, not a split template.
}
func (c *connector) open(ctx context.Context, write bool) (*sql.DB, error) {
	if _, err := os.Stat(c.db); err != nil {
		return nil, err
	}
	p, err := filepath.Abs(c.db)
	if err != nil {
		return nil, err
	}
	p = filepath.ToSlash(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p}
	q := u.Query()
	q.Set("mode", "ro")
	if write {
		q.Set("mode", "rw")
	}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
func (c *connector) initialize(ctx context.Context) error {
	if _, err := os.Stat(c.db); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd, err := c.nativeCommand(ctx, "app-server", "--prepare-storage", "--stdio")
	if err != nil {
		return err
	}
	cmd.Env = append(cmd.Env, "ZCODE_STORAGE_DIR="+c.root, "ZCODE_SESSION_DB_PATH="+c.db)
	cmd.Stdin = strings.NewReader("{\"method\":\"startup/storagePathReady\"}\n")
	// Preparation only opens/migrates storage; it never loads provider credentials or starts a model.
	if out, err := cmd.Output(); err != nil {
		return fmt.Errorf("initialize ZCode with its installed CLI: %w", err)
	} else if !strings.Contains(string(out), `"startup/storagePrepared"`) {
		return fmt.Errorf("ZCode storage preparation did not confirm completion")
	}
	return nil
}
func (c *connector) Discover(ctx context.Context) ([]core.NativeRef, error) {
	db, err := c.open(ctx, false)
	if os.IsNotExist(err) {
		if entries, e := os.ReadDir(filepath.Join(c.root, "v2", "sessions")); e == nil && len(entries) > 0 {
			return nil, fmt.Errorf("legacy ZCode JSON sessions require migration by ZCode before importing")
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT id,directory,title,time_created,time_updated FROM session WHERE time_archived IS NULL ORDER BY time_updated DESC,id`)
	if err != nil {
		return nil, fmt.Errorf("read ZCode sessions: %w", err)
	}
	defer rows.Close()
	out := []core.NativeRef{}
	for rows.Next() {
		var id, cwd, title string
		var created, updated int64
		if err := rows.Scan(&id, &cwd, &title, &created, &updated); err != nil {
			return nil, err
		}
		out = append(out, core.NativeRef{Harness: "zcode", Connector: c.id, Session: id, Path: c.db, MtimeMs: float64(updated), Meta: core.JsonObject{"cwd": cwd, "title": title, "createdAt": float64(created), "updatedAt": float64(updated)}})
	}
	return out, rows.Err()
}

var projectChars = regexp.MustCompile(`[^a-z0-9._-]+`)

func projectID(cwd string) string {
	s := strings.Trim(projectChars.ReplaceAllString(strings.ToLower(cwd), "-"), "-")
	if len(s) > 80 {
		s = s[:80]
	}
	if s == "" {
		s = "default"
	}
	return s
}
func (c *connector) attachIndex(ctx context.Context, db *sql.DB) (bool, error) {
	if _, err := os.Stat(c.index); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	_, err := db.ExecContext(ctx, `ATTACH DATABASE ? AS desktop`, c.index)
	return err == nil, err
}
func (c *connector) indexSession(ctx context.Context, tx *sql.Tx, p *engine.DeliveryPlan, d delivery) error {
	title := p.Target.Title
	if title == "" {
		title = "Copied conversation"
	}
	meta, _ := json.Marshal(core.JsonObject{"taskId": p.Target.Session, "workspacePath": p.Target.Cwd, "title": title, "provider": "glm", "mode": "build", "status": "completed", "createdAt": d.Created, "updatedAt": d.Created})
	_, err := tx.ExecContext(ctx, `INSERT INTO desktop.tasks(workspace_key,workspace_path,task_id,title,task_status,provider,mode,created_at,updated_at,meta_json) VALUES(?,?,?,?,?,'glm','build',?,?,?) ON CONFLICT(workspace_key,task_id) DO NOTHING`, p.Target.Cwd, p.Target.Cwd, p.Target.Session, title, "completed", d.Created, d.Created, string(meta))
	return err
}
func (c *connector) Archive(ctx context.Context, s engine.SessionRef) (core.JsonObject, error) {
	db, err := c.open(ctx, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	has, err := c.attachIndex(ctx, db)
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	stamp := time.Now().UnixMilli()
	r, err := tx.ExecContext(ctx, `UPDATE session SET time_archived=? WHERE id=? AND time_archived IS NULL`, stamp, s.Session)
	if err != nil {
		return nil, err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return nil, fmt.Errorf("ZCode session is absent or already archived")
	}
	states := []any{}
	if has {
		rows, e := tx.QueryContext(ctx, `SELECT workspace_key,archived FROM desktop.tasks WHERE task_id=?`, s.Session)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var key string
			var archived int
			if e = rows.Scan(&key, &archived); e != nil {
				rows.Close()
				return nil, e
			}
			states = append(states, core.JsonObject{"workspace": key, "archived": archived})
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
		if _, err = tx.ExecContext(ctx, `UPDATE desktop.tasks SET archived=1 WHERE task_id=?`, s.Session); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return core.JsonObject{"archivedAt": stamp, "desktopStates": states}, nil
}
func (c *connector) Unarchive(ctx context.Context, s engine.SessionRef, receipt core.JsonObject) error {
	stamp, ok := core.Num(receipt["archivedAt"])
	if !ok {
		return fmt.Errorf("invalid ZCode archive receipt")
	}
	db, err := c.open(ctx, true)
	if err != nil {
		return err
	}
	defer db.Close()
	has, err := c.attachIndex(ctx, db)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, `UPDATE session SET time_archived=NULL WHERE id=? AND time_archived=?`, s.Session, int64(stamp))
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return fmt.Errorf("ZCode archive changed; refusing to overwrite")
	}
	if has {
		for _, v := range core.Arr(receipt["desktopStates"]) {
			state := core.Obj(v)
			old, ok := core.Num(state["archived"])
			if !ok {
				return fmt.Errorf("invalid ZCode desktop archive receipt")
			}
			if _, err = tx.ExecContext(ctx, `UPDATE desktop.tasks SET archived=? WHERE task_id=? AND workspace_key=? AND archived=1`, int(old), s.Session, core.S(state["workspace"])); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
