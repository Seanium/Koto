// Package store holds the rebuildable native index, analysis selections, and settings.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"koto/internal/core"
	"koto/internal/platform"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v TEXT);
CREATE TABLE IF NOT EXISTS sessions (
  harness TEXT NOT NULL, session TEXT NOT NULL, connector TEXT NOT NULL, path TEXT,
  size INTEGER, mtime REAL, title TEXT, cwd TEXT, created INTEGER, updated INTEGER,
  turns INTEGER NOT NULL DEFAULT 0, first_prompt TEXT, first_native TEXT,
  parent_harness TEXT, parent_session TEXT, offset TEXT, meta TEXT, seen INTEGER,
  PRIMARY KEY (connector, session)
);
CREATE INDEX IF NOT EXISTS sessions_first ON sessions(harness, first_native);
CREATE TABLE IF NOT EXISTS analyses (id TEXT PRIMARY KEY, name TEXT NOT NULL, sessions TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS locks (name TEXT PRIMARY KEY, owner TEXT NOT NULL, expires INTEGER NOT NULL, info TEXT);
`

// SessionRow is one native session in the index.
type SessionRow struct {
	Harness     string             `json:"harness"`
	Session     string             `json:"session"`
	Connector   string             `json:"connector"`
	Path        string             `json:"path"`
	Size        int64              `json:"size"`
	Mtime       float64            `json:"mtime"`
	Title       string             `json:"title"`
	Cwd         string             `json:"cwd"`
	Created     float64            `json:"created"`
	Updated     float64            `json:"updated"`
	Turns       int                `json:"turns"`
	FirstPrompt string             `json:"firstPrompt"`
	FirstNative string             `json:"firstNative"`
	Parent      *Ref               `json:"parent"`
	Offset      *core.SourceOffset `json:"offset"`
	Meta        core.JsonObject    `json:"meta"`
}

// Ref is a native session.
type Ref struct {
	Harness string `json:"harness"`
	Session string `json:"session"`
}

// Store is an open koto.sqlite.
type Store struct {
	Dir   string
	File  string
	db    *sql.DB
	Owner string
}

// Open opens (creating) koto.sqlite in dir.
func Open(dir string) (*Store, error) {
	var err error
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	file := filepath.Join(dir, "koto.sqlite")
	uriPath := filepath.ToSlash(file)
	// A drive path needs the leading slash: file:///C:/..., not file://C:/...
	// (SQLite interprets the latter as an unsupported URI authority).
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: uriPath}).String()+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v TEXT)"); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", file, err)
	}
	if err := resetProfileIndex(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize session profiles: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize index: %w", err)
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return &Store{Dir: dir, File: file, db: db, Owner: fmt.Sprintf("%d|%s", os.Getpid(), hex.EncodeToString(b))}, nil
}

// The native index is disposable. An old index is rebuilt from native sources;
// no account migration or alternate historical schema is maintained.
func resetProfileIndex(db *sql.DB) error {
	var version string
	if err := db.QueryRow("SELECT v FROM meta WHERE k='native-index-version'").Scan(&version); err == nil && version == "profiles-1" {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{"DROP TABLE IF EXISTS sessions", schema, "INSERT OR REPLACE INTO meta(k,v) VALUES('native-index-version','profiles-1')"} {
		if _, err = tx.Exec(q); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Close drops this process's locks and closes the file.
func (s *Store) Close() {
	_, _ = s.db.Exec("DELETE FROM locks WHERE owner = ? OR owner LIKE ?", s.Owner, s.Owner+"-%")
	_ = s.db.Close()
}

func nowMs() int64 { return time.Now().UnixMilli() }

// Meta reads a meta value.
func (s *Store) Meta(k string) (string, bool) {
	var v string
	if s.db.QueryRow("SELECT v FROM meta WHERE k = ?", k).Scan(&v) != nil {
		return "", false
	}
	return v, true
}

// SetMeta writes ("" with del deletes).
func (s *Store) SetMeta(k string, v *string) {
	if v == nil {
		_, _ = s.db.Exec("DELETE FROM meta WHERE k = ?", k)
		return
	}
	_, _ = s.db.Exec("INSERT OR REPLACE INTO meta (k, v) VALUES (?, ?)", k, *v)
}

// Setting reads a JSON setting into out; false when unset.
func (s *Store) Setting(key string, out any) bool {
	v, ok := s.Meta("setting:" + key)
	if !ok {
		return false
	}
	return json.Unmarshal([]byte(v), out) == nil
}

// SetSetting writes a JSON setting (nil deletes).
func (s *Store) SetSetting(key string, value any) {
	if value == nil {
		s.SetMeta("setting:"+key, nil)
		return
	}
	b, _ := json.Marshal(value)
	v := string(b)
	s.SetMeta("setting:"+key, &v)
}

// ------------------------------------------------------------------ sessions

const sessionCols = "harness, session, connector, path, size, mtime, title, cwd, created, updated, turns, first_prompt, first_native, parent_harness, parent_session, offset, meta"

func scanSession(sc interface{ Scan(...any) error }) (SessionRow, error) {
	var r SessionRow
	var path, title, cwd, fp, fn, ph, ps, off, meta sql.NullString
	var size sql.NullInt64
	var mtime, created, updated sql.NullFloat64
	err := sc.Scan(&r.Harness, &r.Session, &r.Connector, &path, &size, &mtime, &title, &cwd, &created, &updated, &r.Turns, &fp, &fn, &ph, &ps, &off, &meta)
	if err != nil {
		return r, err
	}
	r.Path, r.Title, r.Cwd, r.FirstPrompt, r.FirstNative = path.String, title.String, cwd.String, fp.String, fn.String
	r.Size, r.Mtime, r.Created, r.Updated = size.Int64, mtime.Float64, created.Float64, updated.Float64
	if ph.Valid && ph.String != "" {
		r.Parent = &Ref{ph.String, ps.String}
	}
	if off.Valid && off.String != "" {
		var o core.SourceOffset
		if json.Unmarshal([]byte(off.String), &o) == nil {
			r.Offset = &o
		}
	}
	r.Meta = core.JsonObject{}
	if meta.Valid && meta.String != "" {
		_ = json.Unmarshal([]byte(meta.String), &r.Meta)
		if r.Meta == nil {
			r.Meta = core.JsonObject{}
		}
	}
	return r, nil
}

// Sessions is the whole index.
func (s *Store) Sessions() []SessionRow {
	rows, err := s.db.Query("SELECT " + sessionCols + " FROM sessions")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []SessionRow
	for rows.Next() {
		if r, err := scanSession(rows); err == nil {
			out = append(out, r)
		}
	}
	return out
}

// Session is one row.
func (s *Store) Session(harness, session string, connector ...string) (SessionRow, bool) {
	q, args := "SELECT "+sessionCols+" FROM sessions WHERE harness = ? AND session = ?", []any{harness, session}
	if len(connector) > 0 {
		q += " AND connector = ?"
		args = append(args, connector[0])
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return SessionRow{}, false
	}
	defer rows.Close()
	if !rows.Next() {
		return SessionRow{}, false
	}
	r, err := scanSession(rows)
	if err != nil || rows.Next() {
		return SessionRow{}, false
	}
	return r, rows.Err() == nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullNum(f float64) any {
	if f == 0 {
		return nil
	}
	return f
}

// PutSession writes a row.
func (s *Store) PutSession(r SessionRow) error {
	return putSession(s.db, r)
}

func putSession(db interface {
	Exec(string, ...any) (sql.Result, error)
}, r SessionRow) error {
	r = compactSession(r)
	var ph, ps, off any
	if r.Parent != nil {
		ph, ps = r.Parent.Harness, r.Parent.Session
	}
	if r.Offset != nil {
		b, _ := json.Marshal(r.Offset)
		off = string(b)
	}
	meta := r.Meta
	if meta == nil {
		meta = core.JsonObject{}
	}
	mb, _ := json.Marshal(meta)
	var size any
	if r.Size != 0 {
		size = r.Size
	}
	_, err := db.Exec("INSERT OR REPLACE INTO sessions ("+sessionCols+", seen) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.Harness, r.Session, r.Connector, nullStr(r.Path), size, nullNum(r.Mtime), nullStr(r.Title), nullStr(r.Cwd), nullNum(r.Created), nullNum(r.Updated), r.Turns,
		nullStr(r.FirstPrompt), nullStr(r.FirstNative), ph, ps, off, string(mb), nowMs())
	return err
}

// DropUnseen forgets a connector's sessions a full scan did not see (deleted or moved by their harness).
func (s *Store) DropUnseen(connector string, seen map[string]bool) (int, error) {
	rows, err := s.db.Query("SELECT harness, session FROM sessions WHERE connector = ?", connector)
	if err != nil {
		return 0, err
	}
	var gone [][2]string
	for rows.Next() {
		var h, ss string
		if rows.Scan(&h, &ss) == nil && !seen[ss] {
			gone = append(gone, [2]string{h, ss})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, g := range gone {
		_, err = s.db.Exec("DELETE FROM sessions WHERE harness = ? AND session = ? AND connector = ?", g[0], g[1], connector)
		if err != nil {
			return 0, err
		}
	}
	return len(gone), nil
}

// ------------------------------------------------------------------ locks

// LockHolder is who holds a lock.
type LockHolder struct {
	Owner string
	Info  core.JsonObject
}

// TryLock takes or renews a named lock shared by every koto process on this data; returns the
// live holder when someone else has it.
func (s *Store) TryLock(name string, ttl time.Duration, info core.JsonObject) (*LockHolder, error) {
	return s.tryLock(name, s.Owner, ttl, info)
}

func (s *Store) tryLock(name, leaseOwner string, ttl time.Duration, info core.JsonObject) (*LockHolder, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var owner string
	var expires int64
	var inf sql.NullString
	err = tx.QueryRow("SELECT owner, expires, info FROM locks WHERE name = ?", name).Scan(&owner, &expires, &inf)
	if err == nil && owner != leaseOwner && expires > nowMs() && ownerAlive(owner) {
		h := &LockHolder{Owner: owner, Info: core.JsonObject{}}
		if inf.Valid {
			_ = json.Unmarshal([]byte(inf.String), &h.Info)
		}
		return h, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	m := core.JsonObject{"pid": os.Getpid()}
	for k, v := range info {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	if _, err := tx.Exec("INSERT OR REPLACE INTO locks (name, owner, expires, info) VALUES (?, ?, ?, ?)", name, leaseOwner, nowMs()+ttl.Milliseconds(), string(b)); err != nil {
		return nil, err
	}
	return nil, tx.Commit()
}

// Unlock releases a lock this process holds.
func (s *Store) Unlock(name string) {
	_, _ = s.db.Exec("DELETE FROM locks WHERE name = ? AND owner = ?", name, s.Owner)
}

func ownerAlive(owner string) bool {
	pid, _ := strconv.Atoi(strings.SplitN(owner, "|", 2)[0])
	if pid == 0 {
		return true
	}
	return platform.ProcessAlive(pid)
}

// EngineLocks adapts the store's locks to the engine.
type EngineLocks struct {
	S     *Store
	owner string
}

// Each engine has a distinct lease owner, including engines sharing one Store.
// A renewal can only extend that engine’s lease, never another view’s operation.
func NewEngineLocks(s *Store) EngineLocks {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return EngineLocks{S: s, owner: s.Owner + "-" + hex.EncodeToString(b)}
}

func (l EngineLocks) TryLock(name string, ttl time.Duration) (bool, error) {
	h, err := l.S.tryLock(name, l.owner, ttl, nil)
	return h != nil, err
}

func (l EngineLocks) Unlock(name string) {
	_, _ = l.S.db.Exec("DELETE FROM locks WHERE name = ? AND owner = ?", name, l.owner)
}
