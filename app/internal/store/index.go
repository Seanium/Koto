package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"koto/internal/core"
	"koto/internal/engine"
	"sort"
	"strings"
)

// Bump this when a shared decoder/index rule changes independently of a connector.
const IndexDecoderVersion = "native-summary@4"

func DecoderFingerprint(c *engine.Connector) string {
	return core.SHA256Hex(IndexDecoderVersion + "\x00" + c.Version)
}

// NativeRevision describes the native source, independent of how much of its
// disposable summary has been enriched. It is stable for a pending list row.
func nativeRevision(c *engine.Connector, ref core.NativeRef) string {
	witness, _, _ := engine.SourceWitness(ref)
	meta := core.JsonObject{}
	for k, v := range ref.Meta {
		// Mutable project membership changes navigation/default copy cwd, not
		// message content. It must not evict an already parsed long transcript.
		if k != "_nativeRevision" && k != "currentCwd" && k != "currentProject" {
			meta[k] = v
		}
	}
	return core.SHA256Hex(fmt.Sprintf("%s:%d:%v:%s:%s:%s", ref.Path, ref.Size, ref.MtimeMs, DecoderFingerprint(c), witness, core.ToJSON(meta)))
}

func RowRevision(row SessionRow) string {
	if revision := core.S(row.Meta["_nativeRevision"]); revision != "" {
		return revision
	}
	prefix := ""
	if row.Offset != nil {
		prefix = row.Offset.PrefixHash
	}
	return core.SHA256Hex(fmt.Sprintf("%s:%d:%v:%s", row.Path, row.Size, row.Mtime, prefix))
}

func (s *Store) bumpIndexGeneration() error {
	_, err := s.db.Exec("INSERT OR REPLACE INTO meta(k,v) VALUES('native-index-generation',?)", rand.Text())
	return err
}

func (ss *Sessions) checkGenerationLocked() {
	generation, _ := ss.Store.Meta("native-index-generation")
	if generation != ss.generation {
		ss.generation = generation
		ss.groups = nil
	}
}

func (ss *Sessions) matchesProfile(r SessionRow) bool {
	c := ss.Registry.Get(r.Connector)
	if c == nil || c.Source == nil || c.ID != r.Connector || c.Harness != r.Harness {
		return false
	}
	p := core.Obj(r.Meta["sourceProfile"])
	return core.S(p["id"]) == c.ID && core.S(p["harness"]) == c.Harness && engine.NormalizeRoot(core.S(p["root"])) == engine.NormalizeRoot(c.Root)
}

// Rows rejects records produced for another root even if another process used
// this data directory with different command-line root overrides.
func (ss *Sessions) Rows() []SessionRow {
	rows := ss.Store.Sessions()
	out := make([]SessionRow, 0, len(rows))
	for _, r := range rows {
		if ss.matchesProfile(r) {
			out = append(out, r)
		}
	}
	return out
}

// compactSession bounds list-only text. Full native messages are read on demand.
func compactSession(r SessionRow) SessionRow {
	bound := func(s string) string {
		v := []rune(s)
		if len(v) > 1024 {
			return string(v[:1024])
		}
		return s
	}
	r.Title = bound(r.Title)
	m := core.JsonObject{}
	for k, v := range r.Meta {
		if k == "summary" {
			continue
		}
		if k == "title" || k == "aiTitle" {
			if s, ok := v.(string); ok {
				v = bound(s)
			}
		}
		m[k] = v
	}
	r.Meta = m
	return r
}

// IndexFingerprint includes the actual native namespaces, not their display names.
func IndexFingerprint(r *engine.Registry) string {
	parts := []string{IndexDecoderVersion}
	for _, state := range r.List() {
		c := state.Connector
		if c.Source != nil {
			parts = append(parts, fmt.Sprintf("%s\x00%s\x00%s\x00%s", c.ID, c.Harness, engine.NormalizeRoot(c.Root), c.Version))
		}
	}
	sort.Strings(parts[1:])
	return core.SHA256Hex(strings.Join(parts, "\n"))
}

// memoryIndex stages only summary rows and offsets. No raw records or parsed IR
// are retained, and cancellation cannot empty the visible persistent index.
func memoryIndex() (*Store, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, Owner: "memory-index"}, nil
}

func (s *Store) replaceIndex(ctx context.Context, rows []SessionRow, fingerprint string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
		return err
	}
	for _, r := range rows {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = putSession(tx, r); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT OR REPLACE INTO meta(k,v) VALUES('native-index-fingerprint',?)", fingerprint); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT OR REPLACE INTO meta(k,v) VALUES('native-index-generation',lower(hex(randomblob(16))))"); err != nil {
		return err
	}
	return tx.Commit()
}

// Rebuild is called under the shared engine lease. A failed source or interrupted
// scan leaves the old index and any active readers intact for a subsequent retry.
func (ss *Sessions) Rebuild(ctx context.Context, onProgress func(engine.ScanProgress), allowPartial ...bool) (engine.ScanReport, error) {
	report := engine.ScanReport{Errors: []engine.ScanError{}}
	stage, err := memoryIndex()
	if err != nil {
		return report, err
	}
	defer stage.Close()
	tmp := NewSessions(stage, ss.Registry)
	tmp.Log = ss.Log
	// Retry failed sources on an explicit rebuild; disabled sources stay disabled.
	for _, state := range ss.Registry.List() {
		if state.Status == "failed" {
			_ = ss.Registry.SetEnabled(state.Connector.ID, true)
		}
	}
	report = tmp.Scan(ctx, nil, onProgress)
	if err = ctx.Err(); err != nil {
		return report, err
	}
	if len(report.Errors) > 0 && (len(allowPartial) == 0 || !allowPartial[0]) {
		return report, fmt.Errorf("index rebuild failed: %s", report.Errors[0].Error)
	}
	if err = ss.Store.replaceIndex(ctx, stage.Sessions(), IndexFingerprint(ss.Registry)); err != nil {
		return report, err
	}
	ss.Close()
	return report, nil
}

// RefreshSession publishes one known native result after a copy, without
// waiting behind an unrelated background scan of the complete library.
func (ss *Sessions) RefreshSession(ctx context.Context, connector, session string, publish func(func() error) error) error {
	ctx, finish, err := ss.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	c := ss.Registry.Get(connector)
	if c == nil || c.Source == nil {
		return ErrNotFound{Selector: connector + ":" + session}
	}
	var ref core.NativeRef
	err = publish(func() error {
		refs, err := discover(ctx, c.Source)
		if err != nil {
			return err
		}
		for _, r := range refs {
			if r.Session == session {
				ref = r
				return nil
			}
		}
		return ErrNotFound{Selector: connector + ":" + session}
	})
	if err != nil {
		return err
	}
	ref.Connector = c.ID
	ref.Meta = compactSession(SessionRow{Meta: engine.ProfileMeta(c, ref.Meta)}).Meta
	ref.Meta["_nativeRevision"] = nativeRevision(c, ref)
	if err = ss.indexOne(ctx, c, ref, nil, publish); err != nil {
		return err
	}
	if err = publish(ss.Store.bumpIndexGeneration); err != nil {
		return err
	}
	ss.Invalidate()
	return nil
}
