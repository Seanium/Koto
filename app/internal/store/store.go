// Package store holds the rebuildable native index, analysis selections, settings and trash.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
  PRIMARY KEY (harness, session)
);
CREATE INDEX IF NOT EXISTS sessions_first ON sessions(harness, first_native);
CREATE TABLE IF NOT EXISTS trash (id TEXT PRIMARY KEY, thread TEXT NOT NULL, harness TEXT, title TEXT, items TEXT NOT NULL, trashed_at INTEGER NOT NULL, restored_at INTEGER);
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

// TrashItem is one native session moved to the trash: its files, or the harness's own archive.
type TrashItem struct {
	Harness      string          `json:"harness"`
	Session      string          `json:"session"`
	Connector    string          `json:"connector"`
	OriginalPath *string         `json:"originalPath"`
	Via          string          `json:"via"` // move | archive | hub
	Files        []TrashFile     `json:"files"`
	Archive      core.JsonObject `json:"archive"`
	Note         *string         `json:"note"`
}

type TrashFile struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// TrashEntry is one move to the trash.
type TrashEntry struct {
	TrashID    string      `json:"trashId"`
	Thread     string      `json:"thread"`
	Harness    *string     `json:"harness"`
	Title      *string     `json:"title"`
	Items      []TrashItem `json:"items"`
	TrashedAt  int64       `json:"trashedAt"`
	RestoredAt *int64      `json:"restoredAt"`
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	file := filepath.Join(dir, "koto.sqlite")
	db, err := sql.Open("sqlite", "file:"+file+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", file, err)
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return &Store{Dir: dir, File: file, db: db, Owner: fmt.Sprintf("%d|%s", os.Getpid(), hex.EncodeToString(b))}, nil
}

// Close drops this process's locks and closes the file.
func (s *Store) Close() {
	_, _ = s.db.Exec("DELETE FROM locks WHERE owner = ?", s.Owner)
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
func (s *Store) Session(harness, session string) (SessionRow, bool) {
	r, err := scanSession(s.db.QueryRow("SELECT "+sessionCols+" FROM sessions WHERE harness = ? AND session = ?", harness, session))
	return r, err == nil
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
func (s *Store) PutSession(r SessionRow) {
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
	_, _ = s.db.Exec("INSERT OR REPLACE INTO sessions ("+sessionCols+", seen) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.Harness, r.Session, r.Connector, nullStr(r.Path), size, nullNum(r.Mtime), nullStr(r.Title), nullStr(r.Cwd), nullNum(r.Created), nullNum(r.Updated), r.Turns,
		nullStr(r.FirstPrompt), nullStr(r.FirstNative), ph, ps, off, string(mb), nowMs())
}

// DropUnseen forgets a connector's sessions a full scan did not see (deleted or moved by their harness).
func (s *Store) DropUnseen(connector string, seen map[string]bool) {
	rows, err := s.db.Query("SELECT harness, session FROM sessions WHERE connector = ?", connector)
	if err != nil {
		return
	}
	var gone [][2]string
	for rows.Next() {
		var h, ss string
		if rows.Scan(&h, &ss) == nil && !seen[ss] {
			gone = append(gone, [2]string{h, ss})
		}
	}
	rows.Close()
	for _, g := range gone {
		_, _ = s.db.Exec("DELETE FROM sessions WHERE harness = ? AND session = ?", g[0], g[1])
	}
}

// ------------------------------------------------------------------ trash

func (s *Store) AddTrash(e TrashEntry) error {
	items, _ := json.Marshal(e.Items)
	_, err := s.db.Exec("INSERT INTO trash (id, thread, harness, title, items, trashed_at, restored_at) VALUES (?, ?, ?, ?, ?, ?, NULL)",
		e.TrashID, e.Thread, e.Harness, e.Title, string(items), e.TrashedAt)
	return err
}

// RestoreTrash marks an entry restored.
func (s *Store) RestoreTrash(id string) {
	_, _ = s.db.Exec("UPDATE trash SET restored_at = ? WHERE id = ?", nowMs(), id)
}

func scanTrash(sc interface{ Scan(...any) error }) (TrashEntry, error) {
	var e TrashEntry
	var h, t sql.NullString
	var items string
	var restored sql.NullInt64
	if err := sc.Scan(&e.TrashID, &e.Thread, &h, &t, &items, &e.TrashedAt, &restored); err != nil {
		return e, err
	}
	if h.Valid {
		x := h.String
		e.Harness = &x
	}
	if t.Valid {
		x := t.String
		e.Title = &x
	}
	if restored.Valid {
		x := restored.Int64
		e.RestoredAt = &x
	}
	_ = json.Unmarshal([]byte(items), &e.Items)
	return e, nil
}

// TrashEntry by id.
func (s *Store) TrashEntry(id string) (TrashEntry, bool) {
	e, err := scanTrash(s.db.QueryRow("SELECT id, thread, harness, title, items, trashed_at, restored_at FROM trash WHERE id = ?", id))
	return e, err == nil
}

// Trash: what is in it now (all = restored ones too), newest first.
func (s *Store) Trash(all bool) []TrashEntry {
	q := "SELECT id, thread, harness, title, items, trashed_at, restored_at FROM trash"
	if !all {
		q += " WHERE restored_at IS NULL"
	}
	rows, err := s.db.Query(q + " ORDER BY trashed_at DESC")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []TrashEntry
	for rows.Next() {
		if e, err := scanTrash(rows); err == nil {
			out = append(out, e)
		}
	}
	return out
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
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var owner string
	var expires int64
	var inf sql.NullString
	err = tx.QueryRow("SELECT owner, expires, info FROM locks WHERE name = ?", name).Scan(&owner, &expires, &inf)
	if err == nil && owner != s.Owner && expires > nowMs() && ownerAlive(owner) {
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
	if _, err := tx.Exec("INSERT OR REPLACE INTO locks (name, owner, expires, info) VALUES (?, ?, ?, ?)", name, s.Owner, nowMs()+ttl.Milliseconds(), string(b)); err != nil {
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
type EngineLocks struct{ S *Store }

func (l EngineLocks) TryLock(name string, ttl time.Duration) (bool, error) {
	h, err := l.S.TryLock(name, ttl, nil)
	return h != nil, err
}

func (l EngineLocks) Unlock(name string) { l.S.Unlock(name) }
