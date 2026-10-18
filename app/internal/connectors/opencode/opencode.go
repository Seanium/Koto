// Package opencode reads and creates OpenCode's native SQLite sessions (v1 tables,
// as used by OpenCode 1.19.3). It never rewrites existing conversations.
package opencode

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"koto/internal/core"
	"koto/internal/engine"
	_ "modernc.org/sqlite"
)

const Version = "opencode-connector@0.1.0"

var Capabilities = core.Capabilities{Read: core.ReadCaps{Images: true, Reasoning: "plain", Subagents: true, Compaction: true}, Write: &core.WriteCaps{Mode: "native-file", Images: true, Reasoning: "plain", Tools: "canonical"}, FormatVersions: []string{"opencode-sqlite-v1"}}

type Options struct{ Root, ID, DBPath string }
type connector struct {
	id, root, db        string
	isolated            bool
	modelMu             sync.Mutex
	modelCwd            string
	modelUntil          time.Time
	model, modelFixture core.JsonObject
}

func New(o Options) *engine.Connector {
	if o.ID == "" {
		o.ID = "opencode"
	}
	explicit := o.Root != ""
	if o.Root == "" {
		base := os.Getenv("XDG_DATA_HOME")
		if base == "" {
			home, _ := os.UserHomeDir()
			base = filepath.Join(home, ".local", "share")
		}
		o.Root = filepath.Join(base, "opencode")
	}
	if o.DBPath == "" && !explicit {
		o.DBPath = os.Getenv("OPENCODE_DB")
	}
	if o.DBPath == "" {
		o.DBPath = filepath.Join(o.Root, "opencode.db")
	} else if !filepath.IsAbs(o.DBPath) {
		o.DBPath = filepath.Join(o.Root, o.DBPath)
	}
	c := &connector{id: o.ID, root: o.Root, db: o.DBPath, isolated: explicit}
	return &engine.Connector{ID: c.id, Root: c.root, Harness: "opencode", Version: Version, Capabilities: Capabilities, Vocabulary: Vocabulary{}, Source: c, Sink: c}
}
func (c *connector) DatabasePath() string { return c.db }
func (c *connector) WatchRoots() []string { return []string{filepath.Dir(c.db)} }
func (c *connector) NewSessionID(key string) string {
	return "ses_" + core.SHA256Hex("opencode\x00" + key)[:26]
}
func (c *connector) ResumeCommand() string { return "opencode --session {id}" }
func (c *connector) open(ctx context.Context, write bool) (*sql.DB, error) {
	if _, err := os.Stat(c.db); err != nil {
		return nil, err
	}
	path, err := filepath.Abs(c.db)
	if err != nil {
		return nil, err
	}
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := url.URL{Scheme: "file", Path: path}
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
	if err := os.MkdirAll(filepath.Dir(c.db), 0700); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd, err := nativeCommand(ctx, "db", "SELECT 1", "--format", "json")
	if err != nil {
		return err
	}
	cmd.Env = c.nativeEnv()
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("initialize OpenCode with its installed CLI: %w: %.500s", err, b)
	}
	return nil
}
func (c *connector) Discover(ctx context.Context) ([]core.NativeRef, error) {
	db, err := c.open(ctx, false)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT id,directory,title,time_created,time_updated FROM session WHERE time_archived IS NULL ORDER BY time_updated DESC,id`)
	if err != nil {
		return nil, fmt.Errorf("read OpenCode sessions: %w", err)
	}
	defer rows.Close()
	out := []core.NativeRef{}
	for rows.Next() {
		var id, cwd, title string
		var created, updated int64
		if err := rows.Scan(&id, &cwd, &title, &created, &updated); err != nil {
			return nil, err
		}
		out = append(out, core.NativeRef{Harness: "opencode", Connector: c.id, Session: id, Path: c.db, MtimeMs: float64(updated), Meta: core.JsonObject{"cwd": filepath.FromSlash(cwd), "title": title, "createdAt": float64(created), "updatedAt": float64(updated)}})
	}
	return out, rows.Err()
}
