package store

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/hub"
)

// Group is the native sessions that are one conversation.
type Group struct {
	// ID is th:… stable while its earliest session exists.
	ID      string       `json:"id"`
	Members []SessionRow `json:"members"`
}

// OpenThread is a conversation's working copy.
type OpenThread struct {
	Hub    *hub.Hub
	Engine *engine.Engine
	// Thread is the group's thread inside the working copy (content-derived).
	Thread string
	Group  Group
}

// ErrNotFound: nothing matches a selector.
type ErrNotFound struct{ Selector string }

func (e ErrNotFound) Error() string { return "nothing matches " + e.Selector }

func keyOf(h, s string) string { return h + ":" + s }

// Sessions: the index (stat-only for unchanged files), the groups, and the working copies.
type Sessions struct {
	Jobs     *engine.CopyJobs
	Store    *Store
	Registry *engine.Registry
	Log      engine.Logger
	LiveMs   *int64

	mu     sync.Mutex
	loadMu sync.Mutex // avoid parsing the same large session concurrently
	groups []Group
	open   map[string]*openEntry
	// recent: conversations opened lately, newest first (kept after their working copies go), so a
	// node id the app got from one can still be found when its working copy was dropped.
	recent []Member
}

// Member is one session of a conversation, enough to find its conversation again.
type Member struct{ Harness, Session string }

type openEntry struct {
	key   string
	view  *OpenThread
	used  time.Time
	bytes int64
}

// NewSessions makes the service.
func NewSessions(s *Store, r *engine.Registry) *Sessions {
	return &Sessions{Store: s, Registry: r, Log: engine.SilentLogger, open: map[string]*openEntry{}}
}

// ------------------------------------------------------------------ index

// Scan is an index pass over every source (or the named connectors).
func (ss *Sessions) Scan(ctx context.Context, connectors []string, onProgress func(engine.ScanProgress)) engine.ScanReport {
	report := engine.ScanReport{Errors: []engine.ScanError{}}
	for _, c := range ss.Registry.Sources() {
		if len(connectors) > 0 && !contains(connectors, c.ID) {
			continue
		}
		if err := ss.scanConnector(ctx, c, &report, onProgress); err != nil {
			ss.Registry.MarkFailed(c.ID, err)
			report.Errors = append(report.Errors, engine.ScanError{Connector: c.ID, Error: err.Error()})
		}
	}
	if report.Changed > 0 {
		ss.Invalidate()
	}
	// A first pass reads hundreds of MB: give it back to the system.
	if report.Changed > 20 {
		debug.FreeOSMemory()
	}
	return report
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func (ss *Sessions) scanConnector(ctx context.Context, c *engine.Connector, report *engine.ScanReport, onProgress func(engine.ScanProgress)) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	refs, err := c.Source.Discover(ctx)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	done := 0
	last := time.Now()
	for _, ref := range refs {
		if ctx.Err() != nil {
			return nil
		}
		seen[ref.Session] = true
		report.Sessions++
		row, ok := ss.Store.Session(ref.Harness, ref.Session)
		if ok && row.Offset != nil && engine.Unchanged(ref, row.Offset) {
			// Metadata from beside the file (a Codex rename, a Desktop title) can change on its own.
			moved := false
			for k, v := range ref.Meta {
				if core.Stringify(orNull(row.Meta[k])) != core.Stringify(orNull(v)) {
					moved = true
					break
				}
			}
			if moved {
				for k, v := range ref.Meta {
					row.Meta[k] = v
				}
				if t, ok := ref.Meta["title"].(string); ok && t != "" {
					row.Title = t
				}
				ss.Store.PutSession(row)
			}
		} else {
			var prev *SessionRow
			if ok {
				prev = &row
			}
			if err := ss.indexOne(ctx, c, ref, prev); err != nil {
				report.Errors = append(report.Errors, engine.ScanError{Connector: c.ID, Session: ref.Session, Error: err.Error()})
			} else {
				next, _ := ss.Store.Session(ref.Harness, ref.Session)
				if row.Offset == nil || next.Offset == nil || row.Offset.Records != next.Offset.Records || row.Offset.PrefixHash != next.Offset.PrefixHash {
					report.Changed++
				}
			}
		}
		done++
		if onProgress != nil && time.Since(last) > 500*time.Millisecond {
			last = time.Now()
			onProgress(engine.ScanProgress{Connector: c.ID, Harness: c.Harness, Done: done, Total: len(refs), Added: report.Changed})
		}
	}
	if ctx.Err() == nil {
		ss.Store.DropUnseen(c.ID, seen)
	}
	return nil
}

func orNull(v any) any { return v }

var errRewritten = fmt.Errorf("rewritten")

// indexOne reads a session from where the index stopped (from the start if new or rewritten).
func (ss *Sessions) indexOne(ctx context.Context, c *engine.Connector, ref core.NativeRef, prev *SessionRow) error {
	run := func(from *core.SourceOffset, base *SessionRow) (*SessionRow, error) {
		var s SessionRow
		if base != nil {
			s = *base
			s.Meta = core.JsonObject{}
			for k, v := range base.Meta {
				s.Meta[k] = v
			}
		} else {
			s = SessionRow{Harness: ref.Harness, Session: ref.Session, Connector: c.ID, Path: ref.Path, Meta: core.JsonObject{}}
		}
		first := base == nil
		err := c.Source.Tail(ctx, ref, from, func(b core.SourceBatch) error {
			if b.Rewritten != nil && base != nil {
				return errRewritten
			}
			if first && len(b.Records) > 0 && b.Records[0].Index == 0 && len(b.Drafts) > 0 {
				s.FirstNative = b.Records[0].NativeID
			}
			first = false
			if b.Lineage != nil && s.Parent == nil {
				s.Parent = &Ref{b.Lineage.Harness, b.Lineage.Session}
			}
			meta := core.JsonObject{}
			for k, v := range b.Meta {
				meta[k] = v
			}
			for k, v := range ref.Meta {
				meta[k] = v
			}
			for k, v := range meta {
				s.Meta[k] = v
			}
			if t, ok := meta["title"].(string); ok && t != "" {
				s.Title = t
			}
			if cw, ok := meta["cwd"].(string); ok && cw != "" {
				s.Cwd = cw
			}
			for i := range b.Drafts {
				note(&s, &b.Drafts[i])
			}
			o := b.Offset
			s.Offset = &o
			return nil
		})
		if err != nil {
			return nil, err
		}
		if ref.Path != "" {
			s.Path = ref.Path
		}
		s.Size, s.Mtime = ref.Size, ref.MtimeMs
		if s.Offset == nil {
			s.Offset = from
		}
		return &s, nil
	}
	var from *core.SourceOffset
	var base *SessionRow
	if prev != nil && prev.Offset != nil {
		from, base = prev.Offset, prev
	}
	out, err := run(from, base)
	if err == errRewritten {
		out, err = run(nil, nil)
	}
	if err != nil {
		return err
	}
	ss.Store.PutSession(*out)
	return nil
}

var ws = regexp.MustCompile(`\s+`)

// note is one decoded event's contribution to the list row.
func note(s *SessionRow, d *core.DraftEvent) {
	if d.Time != nil && *d.Time != 0 {
		t := *d.Time
		if s.Created == 0 || t < s.Created {
			s.Created = t
		}
		if t > s.Updated {
			s.Updated = t
		}
	}
	if d.Kind == "message" && d.Actor == "user" {
		for _, p := range d.Parts {
			if kind := core.S(p["type"]); kind == "text" || kind == "image" || kind == "file" || kind == "opaque" {
				s.Turns++
				break
			}
		}
		for _, p := range d.Parts {
			if core.S(p["type"]) != "text" {
				continue
			}
			if text := core.S(p["text"]); text != "" {
				if s.FirstPrompt == "" {
					r := []rune(strings.TrimSpace(ws.ReplaceAllString(text, " ")))
					if len(r) > 200 {
						r = r[:200]
					}
					s.FirstPrompt = string(r)
				}
			}
			break
		}
	}
}

// ------------------------------------------------------------------ groups

// Groups: native lineage, Claude Code copies that reuse the original's record ids, within one Agent.
func (ss *Sessions) Groups() []Group {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.groups != nil {
		return ss.groups
	}
	rows := ss.Store.Sessions()
	byKey := map[string]SessionRow{}
	for _, r := range rows {
		byKey[keyOf(r.Harness, r.Session)] = r
	}
	up := map[string]string{}
	var find func(k string) string
	find = func(k string) string {
		r := k
		for up[r] != "" && up[r] != r {
			r = up[r]
		}
		up[k] = r
		return r
	}
	join := func(a, b string) {
		x, y := find(a), find(b)
		if x != y {
			up[x] = y
		}
	}
	for _, r := range rows {
		k := keyOf(r.Harness, r.Session)
		if up[k] == "" {
			up[k] = k
		}
		if r.Parent != nil {
			if _, ok := byKey[keyOf(r.Parent.Harness, r.Parent.Session)]; ok {
				join(k, keyOf(r.Parent.Harness, r.Parent.Session))
			}
		}
	}
	// Claude Code copies (/branch before forkedFrom, a hand copy) start with the original's record id.
	firsts := map[string]string{}
	for _, r := range rows {
		if r.Harness != "claude-code" || r.FirstNative == "" {
			continue
		}
		if k, ok := firsts[r.FirstNative]; ok {
			join(keyOf(r.Harness, r.Session), k)
		} else {
			firsts[r.FirstNative] = keyOf(r.Harness, r.Session)
		}
	}
	members := map[string][]SessionRow{}
	var order []string
	for _, r := range rows {
		root := find(keyOf(r.Harness, r.Session))
		if _, ok := members[root]; !ok {
			order = append(order, root)
		}
		members[root] = append(members[root], r)
	}
	created := func(r SessionRow) float64 {
		if r.Created == 0 {
			return math.Inf(1)
		}
		return r.Created
	}
	groups := []Group{}
	for _, root := range order {
		ms := members[root]
		sort.SliceStable(ms, func(i, j int) bool {
			a, b := created(ms[i]), created(ms[j])
			if a != b {
				return a < b
			}
			return keyOf(ms[i].Harness, ms[i].Session) < keyOf(ms[j].Harness, ms[j].Session)
		})
		groups = append(groups, Group{ID: "th:" + core.SHA256Hex(keyOf(ms[0].Harness, ms[0].Session))[:32], Members: ms})
	}
	ss.groups = groups
	return groups
}

// Group a selector names: th:… (a prefix is fine) or harness:session.
func (ss *Sessions) Group(selector string) (Group, bool) {
	gs := ss.Groups()
	if strings.HasPrefix(selector, "th:") {
		for _, g := range gs {
			if strings.HasPrefix(g.ID, selector) {
				return g, true
			}
		}
		return Group{}, false
	}
	if i := strings.Index(selector, ":"); i > 0 {
		h, s := selector[:i], selector[i+1:]
		for _, g := range gs {
			for _, m := range g.Members {
				if m.Harness == h && strings.HasPrefix(m.Session, s) {
					return g, true
				}
			}
		}
	}
	return Group{}, false
}

// Invalidate drops the cached groups.
func (ss *Sessions) Invalidate() {
	ss.mu.Lock()
	ss.groups = nil
	ss.mu.Unlock()
}

// ------------------------------------------------------------------ working copies

// OpenThread parses a conversation's sessions into a working copy (cached while its files do not change).
func (ss *Sessions) OpenThread(ctx context.Context, selector string) (*OpenThread, error) {
	ss.loadMu.Lock()
	defer ss.loadMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g, ok := ss.Group(selector)
	if !ok {
		return nil, ErrNotFound{selector}
	}
	var kb strings.Builder
	for _, m := range g.Members {
		fmt.Fprintf(&kb, "%s@%d:%v|", keyOf(m.Harness, m.Session), m.Size, m.Mtime)
	}
	key := kb.String()
	ss.mu.Lock()
	if hit := ss.open[g.ID]; hit != nil {
		if hit.key == key {
			hit.used = time.Now()
			ss.mu.Unlock()
			return hit.view, nil
		}
		// A request may still hold this view. Drop the cache reference; GC reclaims it when done.
		delete(ss.open, g.ID)
	}
	ss.mu.Unlock()
	h := hub.New()
	trashed := map[string]bool{}
	for _, e := range ss.Store.Trash(false) {
		for _, i := range e.Items {
			trashed[keyOf(i.Harness, i.Session)] = true
		}
	}
	for _, m := range g.Members {
		c := ss.Registry.Get(m.Connector)
		if c == nil || c.Source == nil || m.Path == "" || trashed[keyOf(m.Harness, m.Session)] {
			continue
		}
		meta := core.JsonObject{}
		for k, v := range m.Meta {
			meta[k] = v
		}
		if m.Title != "" {
			meta["title"] = m.Title
		}
		ref := core.NativeRef{Harness: m.Harness, Connector: m.Connector, Session: m.Session, Path: m.Path, Size: m.Size, MtimeMs: m.Mtime, Meta: meta}
		err := c.Source.Tail(ctx, ref, nil, func(b core.SourceBatch) error {
			h.Ingest(b, hub.ConnectorInfo{ID: c.ID, Harness: c.Harness, Version: c.Version})
			return nil
		})
		if err != nil {
			ss.Log.Warn(fmt.Sprintf("%s:%s: %v", m.Harness, m.Session, err))
		}
	}
	// The group's thread: the one most of its sessions landed in.
	counts := map[string]int{}
	var order []string
	for _, m := range g.Members {
		if b := h.Binding(m.Harness, m.Session); b != nil {
			if _, ok := counts[b.Thread]; !ok {
				order = append(order, b.Thread)
			}
			counts[b.Thread]++
		}
	}
	thread := ""
	for _, t := range order {
		if thread == "" || counts[t] > counts[thread] {
			thread = t
		}
	}
	if harness, session, native := strings.Cut(selector, ":"); native && harness != "th" {
		for _, m := range g.Members {
			if m.Harness == harness && strings.HasPrefix(m.Session, session) {
				if b := h.Binding(m.Harness, m.Session); b != nil {
					thread = b.Thread
					break
				}
			}
		}
	}
	e := engine.New(h, ss.Registry)
	e.Log, e.Locks = ss.Log, EngineLocks{ss.Store}
	if ss.LiveMs != nil {
		e.LiveMs = *ss.LiveMs
	}
	e.Jobs = ss.Jobs
	// A working copy refreshes only its own sessions (a copy plans after a rescan): reading every
	// session on disk into it would mix conversations and cost the memory the index saves.
	own := map[string]bool{}
	for _, m := range g.Members {
		if !trashed[keyOf(m.Harness, m.Session)] {
			own[keyOf(m.Harness, m.Session)] = true
		}
	}
	e.Filter = func(r core.NativeRef) bool { return own[keyOf(r.Harness, r.Session)] }
	view := &OpenThread{Hub: h, Engine: e, Thread: thread, Group: g}
	if len(g.Members) > 0 {
		m := Member{g.Members[0].Harness, g.Members[0].Session}
		ss.mu.Lock()
		ss.recent = append([]Member{m}, slices.DeleteFunc(ss.recent, func(x Member) bool { return x == m })...)
		if len(ss.recent) > 32 {
			ss.recent = ss.recent[:32]
		}
		ss.mu.Unlock()
	}
	ss.mu.Lock()
	ss.open[g.ID] = &openEntry{key: key, view: view, used: time.Now(), bytes: h.RetainedBytes()}
	var retained int64
	for _, entry := range ss.open {
		retained += entry.bytes
	}
	// A few conversations stay parsed; the least recently used goes.
	for len(ss.open) > 3 || len(ss.open) > 1 && retained > 128<<20 {
		var oldK string
		var oldT time.Time
		for k, o := range ss.open {
			if oldK == "" || o.used.Before(oldT) {
				oldK, oldT = k, o.used
			}
		}
		retained -= ss.open[oldK].bytes
		delete(ss.open, oldK)
	}
	ss.mu.Unlock()
	return view, nil
}

// Views are the open working copies.
func (ss *Sessions) Views() []*OpenThread {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	var out []*OpenThread
	for _, o := range ss.open {
		out = append(out, o.view)
	}
	return out
}

// Recent is the conversations opened lately, newest first, as harness:session selectors.
func (ss *Sessions) Recent() []string {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	out := make([]string, len(ss.recent))
	for i, m := range ss.recent {
		out[i] = m.Harness + ":" + m.Session
	}
	return out
}

// Close drops every working copy.
func (ss *Sessions) Close() {
	ss.loadMu.Lock()
	defer ss.loadMu.Unlock()
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.open = map[string]*openEntry{}
	ss.groups, ss.recent = nil, nil
}
