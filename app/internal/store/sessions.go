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

func keyOf(h, s string, connector ...string) string {
	if len(connector) > 0 && connector[0] != "" && connector[0] != h {
		return core.SessionSelector(connector[0], h, s)
	}
	return h + ":" + s
}

// Sessions: the index (stat-only for unchanged files), the groups, and the working copies.
type Sessions struct {
	Jobs     *engine.CopyJobs
	Store    *Store
	Registry *engine.Registry
	Log      engine.Logger

	mu        sync.Mutex
	life      context.Context
	stop      context.CancelFunc
	active    sync.WaitGroup
	closed    bool
	loads     map[string]chan struct{} // same-conversation dedup; other reads never queue behind it
	readSlots chan struct{}
	groups    []Group
	open      map[string]*openEntry
	// recent: conversations opened lately, newest first (kept after their working copies go), so a
	// node id the app got from one can still be found when its working copy was dropped.
	recent     []string
	generation string
}

type openEntry struct {
	key   string
	view  *OpenThread
	used  time.Time
	bytes int64
}

// NewSessions makes the service.
func NewSessions(s *Store, r *engine.Registry) *Sessions {
	life, stop := context.WithCancel(context.Background())
	return &Sessions{life: life, stop: stop, Store: s, Registry: r, Log: engine.SilentLogger, open: map[string]*openEntry{}, loads: map[string]chan struct{}{}, readSlots: make(chan struct{}, 2)}
}

// begin keeps lifecycle cancellation independent of individual UI requests.
func (ss *Sessions) begin(ctx context.Context) (context.Context, func(), error) {
	ss.mu.Lock()
	if ss.closed {
		ss.mu.Unlock()
		return nil, nil, context.Canceled
	}
	ss.active.Add(1)
	ss.mu.Unlock()
	child, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(ss.life, cancel)
	return child, func() { stop(); cancel(); ss.active.Done() }, nil
}

// Shutdown cancels and joins owned readers before the database is closed.
func (ss *Sessions) Shutdown() {
	ss.mu.Lock()
	ss.closed = true
	ss.stop()
	ss.mu.Unlock()
	ss.active.Wait()
	ss.Close()
}

// ------------------------------------------------------------------ index

// Scan is an index pass over every source (or the named connectors).
type indexPublish func(func() error) error

func (ss *Sessions) Scan(ctx context.Context, connectors []string, onProgress func(engine.ScanProgress), resetOffsets ...map[string]bool) engine.ScanReport {
	return ss.scan(ctx, connectors, onProgress, nil, resetOffsets...)
}

// ScanIncremental reads native histories without the copy lease. Only bounded
// index publication is serialized with copying, after rechecking the source.
func (ss *Sessions) ScanIncremental(ctx context.Context, onProgress func(engine.ScanProgress), reset map[string]bool, publish func(func() error) error) engine.ScanReport {
	return ss.scan(ctx, nil, onProgress, publish, reset)
}

func (ss *Sessions) scan(ctx context.Context, connectors []string, onProgress func(engine.ScanProgress), publish indexPublish, resetOffsets ...map[string]bool) engine.ScanReport {
	if publish == nil {
		publish = func(fn func() error) error { return fn() }
	}
	report := engine.ScanReport{Errors: []engine.ScanError{}}
	ctx, finish, err := ss.begin(ctx)
	if err != nil {
		report.Errors = append(report.Errors, engine.ScanError{Error: err.Error()})
		return report
	}
	defer finish()
	type discovered struct {
		connector *engine.Connector
		refs      []core.NativeRef
	}
	var sources []discovered
	// Publish discovery metadata before decoding any history. A cold list must
	// not wait for every transcript, image and tool result to be parsed.
	for _, c := range ss.Registry.Sources() {
		if len(connectors) > 0 && !contains(connectors, c.ID) {
			continue
		}
		before := report.Changed
		var refs []core.NativeRef
		err := publish(func() error {
			var err error
			refs, err = discover(ctx, c.Source)
			if err != nil {
				return err
			}
			return ss.publishDiscovered(ctx, c, refs, &report)
		})
		if err != nil {
			ss.Registry.MarkFailed(c.ID, err)
			report.Errors = append(report.Errors, engine.ScanError{Connector: c.ID, Error: err.Error()})
			continue
		}
		sources = append(sources, discovered{c, refs})
		if onProgress != nil && report.Changed != before {
			onProgress(engine.ScanProgress{Connector: c.ID, Harness: c.Harness, Total: len(refs), Added: report.Changed})
		}
	}
	for _, source := range sources {
		c := source.connector
		if err := ss.scanConnector(ctx, c, source.refs, &report, onProgress, len(resetOffsets) > 0 && resetOffsets[0][c.ID], publish); err != nil {
			ss.Registry.MarkFailed(c.ID, err)
			report.Errors = append(report.Errors, engine.ScanError{Connector: c.ID, Error: err.Error()})
		}
	}
	if len(connectors) == 0 && ctx.Err() == nil {
		for _, row := range ss.Store.Sessions() {
			if ss.Registry.Get(row.Connector) == nil {
				removed := 0
				err := publish(func() error {
					var err error
					if ss.Registry.Get(row.Connector) == nil {
						removed, err = ss.Store.DropUnseen(row.Connector, map[string]bool{})
					}
					return err
				})
				if err != nil {
					report.Errors = append(report.Errors, engine.ScanError{Connector: row.Connector, Error: err.Error()})
				} else {
					report.Changed += removed
				}
			}
		}
	}
	if report.Changed > 0 {
		if err := publish(ss.Store.bumpIndexGeneration); err != nil {
			report.Errors = append(report.Errors, engine.ScanError{Error: err.Error()})
		}
		ss.Invalidate()
	}
	// A first pass reads hundreds of MB: give it back to the system.
	if report.Changed > 20 {
		debug.FreeOSMemory()
	}
	return report
}

func discover(ctx context.Context, source engine.Source) (refs []core.NativeRef, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("discover: %v", r)
		}
	}()
	return source.Discover(ctx)
}

func (ss *Sessions) publishDiscovered(ctx context.Context, c *engine.Connector, refs []core.NativeRef, report *engine.ScanReport) error {
	added := false
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if row, ok := ss.Store.Session(ref.Harness, ref.Session, c.ID); ok && ss.matchesProfile(row) {
			continue
		}
		m := compactSession(SessionRow{Meta: engine.ProfileMeta(c, ref.Meta)}).Meta
		normalized := ref
		normalized.Meta = m
		m["_nativeRevision"] = nativeRevision(c, normalized)
		created, _ := core.Num(m["createdAt"])
		updated, _ := core.Num(m["updatedAt"])
		if updated == 0 {
			updated = ref.MtimeMs
		}
		r := SessionRow{Harness: ref.Harness, Session: ref.Session, Connector: c.ID, Path: ref.Path, Size: ref.Size, Mtime: ref.MtimeMs, Title: core.S(m["title"]), Cwd: core.WorkingDirectory(m), Created: created, Updated: updated, Meta: m}
		if err := ss.Store.PutSession(r); err != nil {
			return err
		}
		report.Changed++
		added = true
	}
	if added {
		ss.Invalidate()
	}
	return nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func (ss *Sessions) scanConnector(ctx context.Context, c *engine.Connector, refs []core.NativeRef, report *engine.ScanReport, onProgress func(engine.ScanProgress), resetOffsets bool, publish indexPublish) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	sort.SliceStable(refs, func(i, j int) bool { return refs[i].MtimeMs > refs[j].MtimeMs })
	seen := map[string]bool{}
	done := 0
	before := report.Changed
	started := time.Now()
	last := started
	for _, ref := range refs {
		ref.Connector = c.ID
		ref.Meta = compactSession(SessionRow{Meta: engine.ProfileMeta(c, ref.Meta)}).Meta
		ref.Meta["_nativeRevision"] = nativeRevision(c, ref)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		seen[ref.Session] = true
		report.Sessions++
		row, ok := ss.Store.Session(ref.Harness, ref.Session, c.ID)
		ok = ok && ss.matchesProfile(row)
		if ok && !resetOffsets && row.Offset != nil && engine.Unchanged(ref, row.Offset) {
			// Metadata from beside the file (a Codex rename, a Desktop title) can change on its own.
			moved := false
			for k, v := range ref.Meta {
				if k == "identity" {
					v = core.IdentityMeta(core.MergeIdentity(core.ReadIdentity(row.Meta), core.ReadIdentity(ref.Meta)))
				}
				if core.Stringify(orNull(row.Meta[k])) != core.Stringify(orNull(v)) {
					moved = true
					break
				}
			}
			if moved {
				for k, v := range ref.Meta {
					if k == "identity" {
						v = core.IdentityMeta(core.MergeIdentity(core.ReadIdentity(row.Meta), core.ReadIdentity(ref.Meta)))
					}
					row.Meta[k] = v
				}
				if t, ok := ref.Meta["title"].(string); ok && t != "" {
					row.Title = t
				}
				row.Cwd = core.WorkingDirectory(row.Meta)
				if err := publish(func() error {
					if nativeRevision(c, ref) != core.S(ref.Meta["_nativeRevision"]) {
						return nil
					}
					return ss.Store.PutSession(row)
				}); err != nil {
					return err
				}
				report.Changed++
			}
		} else {
			var prev *SessionRow
			if ok && !resetOffsets {
				prev = &row
			}
			if err := ss.indexOne(ctx, c, ref, prev, publish); err != nil {
				if err == errSourceChanged {
					report.Skipped++
				} else {
					report.Errors = append(report.Errors, engine.ScanError{Connector: c.ID, Session: ref.Session, Error: err.Error()})
				}
			} else {
				next, _ := ss.Store.Session(ref.Harness, ref.Session, c.ID)
				if row.Offset == nil || next.Offset == nil || row.Offset.Records != next.Offset.Records || row.Offset.PrefixHash != next.Offset.PrefixHash {
					report.Changed++
				}
			}
		}
		done++
		if onProgress != nil && time.Since(last) > 500*time.Millisecond {
			last = time.Now()
			ss.Invalidate()
			onProgress(engine.ScanProgress{Connector: c.ID, Harness: c.Harness, Done: done, Total: len(refs), Added: report.Changed})
		}
	}
	if ctx.Err() == nil {
		err := publish(func() error {
			// Copy may have created a session after initial discovery. Recheck under
			// its lease before removing anything from the disposable index.
			current, err := discover(ctx, c.Source)
			if err != nil {
				return err
			}
			latest := map[string]bool{}
			for _, r := range current {
				latest[r.Session] = true
			}
			removed, err := ss.Store.DropUnseen(c.ID, latest)
			report.Changed += removed
			return err
		})
		if err != nil {
			return err
		}
	}
	if onProgress != nil && (report.Changed != before || time.Since(started) > 500*time.Millisecond) {
		onProgress(engine.ScanProgress{Connector: c.ID, Harness: c.Harness, Done: done, Total: len(refs), Added: report.Changed})
	}
	return nil
}

func orNull(v any) any { return v }

var errRewritten = fmt.Errorf("rewritten")
var errSourceChanged = fmt.Errorf("source changed during summary read")

// indexOne reads a session from where the index stopped (from the start if new or rewritten).
func (ss *Sessions) indexOne(ctx context.Context, c *engine.Connector, ref core.NativeRef, prev *SessionRow, publishers ...indexPublish) error {
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
		err := c.Source.Tail(engine.WithIndexOnly(ctx), ref, from, func(b core.SourceBatch) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if b.Rewritten != nil && base != nil {
				return errRewritten
			}
			if s.FirstNative == "" {
				for _, d := range b.Drafts {
					if d.Kind == "message" && (d.Actor == "user" || d.Actor == "assistant") && d.NativeID != "" && !(c.Harness == "claude-code" && strings.HasPrefix(d.NativeID, "#")) {
						s.FirstNative = d.NativeID
						break
					}
				}
			}
			if b.Lineage != nil && s.Parent == nil {
				s.Parent = &Ref{b.Lineage.Harness, b.Lineage.Session}
				s.Meta["_nativeLineage"] = core.JsonObject{"kind": b.Lineage.Kind, "copied": b.Lineage.Copied, "afterNative": b.Lineage.AfterNative}
			}
			meta := core.JsonObject{}
			for k, v := range b.Meta {
				if k == "summary" {
					continue
				}
				meta[k] = v
			}
			for k, v := range ref.Meta {
				if k == "identity" {
					meta[k] = core.IdentityMeta(core.MergeIdentity(core.ReadIdentity(meta), core.ReadIdentity(ref.Meta)))
					continue
				}
				meta[k] = v
			}
			for k, v := range meta {
				if k == "identity" {
					v = core.IdentityMeta(core.MergeIdentity(core.ReadIdentity(s.Meta), core.ReadIdentity(meta)))
				}
				s.Meta[k] = v
			}
			if t, ok := meta["title"].(string); ok && t != "" {
				s.Title = t
			}
			s.Cwd = core.WorkingDirectory(s.Meta)
			for i := range b.Drafts {
				if identity := core.IdentityOfDraft(&b.Drafts[i]); len(identity.Providers) > 0 {
					s.Meta["identity"] = core.IdentityMeta(core.MergeIdentity(core.ReadIdentity(s.Meta), identity))
				}
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
	if err := ctx.Err(); err != nil {
		return err
	}
	publish := indexPublish(func(fn func() error) error { return fn() })
	if len(publishers) > 0 {
		publish = publishers[0]
	}
	return publish(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if ss.Registry.Get(c.ID) == nil {
			return errSourceChanged
		}
		if expected := core.S(ref.Meta["_nativeRevision"]); expected != "" && nativeRevision(c, ref) != expected {
			return errSourceChanged
		}
		return ss.Store.PutSession(*out)
	})
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
		if text, ok := d.Attrs["displayText"].(string); ok {
			if s.FirstPrompt == "" {
				s.FirstPrompt = promptExcerpt(text)
			}
			return
		}
		for _, p := range d.Parts {
			if core.S(p["type"]) != "text" {
				continue
			}
			if text := core.S(p["text"]); text != "" {
				if s.FirstPrompt == "" {
					s.FirstPrompt = promptExcerpt(text)
				}
			}
			break
		}
	}
}

func promptExcerpt(text string) string {
	r := []rune(strings.TrimSpace(ws.ReplaceAllString(text, " ")))
	if len(r) > 200 {
		r = r[:200]
	}
	return string(r)
}

// ------------------------------------------------------------------ groups

// Groups: native lineage, Claude Code copies that reuse the original's record ids, within one Agent.
func (ss *Sessions) Groups() []Group {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.checkGenerationLocked()
	if ss.groups != nil {
		return ss.groups
	}
	rows := ss.Rows()
	byKey := map[string]SessionRow{}
	for _, r := range rows {
		byKey[keyOf(r.Harness, r.Session, r.Connector)] = r
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
		k := keyOf(r.Harness, r.Session, r.Connector)
		if up[k] == "" {
			up[k] = k
		}
		if r.Parent != nil {
			if _, ok := byKey[keyOf(r.Parent.Harness, r.Parent.Session, r.Connector)]; ok {
				join(k, keyOf(r.Parent.Harness, r.Parent.Session, r.Connector))
			}
		}
	}
	// Claude Code copies (/branch before forkedFrom, a hand copy) start with the original's record id.
	firsts := map[string]string{}
	for _, r := range rows {
		if r.Harness != "claude-code" || r.FirstNative == "" {
			continue
		}
		if k, ok := firsts[r.Connector+"\x00"+r.FirstNative]; ok {
			join(keyOf(r.Harness, r.Session, r.Connector), k)
		} else {
			firsts[r.Connector+"\x00"+r.FirstNative] = keyOf(r.Harness, r.Session, r.Connector)
		}
	}
	members := map[string][]SessionRow{}
	var order []string
	for _, r := range rows {
		root := find(keyOf(r.Harness, r.Session, r.Connector))
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
			return keyOf(ms[i].Harness, ms[i].Session, ms[i].Connector) < keyOf(ms[j].Harness, ms[j].Session, ms[j].Connector)
		})
		groups = append(groups, Group{ID: "th:" + core.SHA256Hex(keyOf(ms[0].Harness, ms[0].Session, ms[0].Connector))[:32], Members: ms})
	}
	ss.groups = groups
	return groups
}

// Group a selector names: th:… (a prefix is fine) or harness:session.
func (ss *Sessions) Group(selector string) (Group, bool) {
	g, err := ss.ResolveGroup(selector)
	return g, err == nil
}
func (ss *Sessions) ResolveGroup(selector string) (Group, error) {
	var result Group
	var matches []string
	for _, g := range ss.Groups() {
		if strings.HasPrefix(selector, "th:") {
			if strings.HasPrefix(g.ID, selector) {
				result = g
				matches = append(matches, g.ID)
			}
			continue
		}
		for _, m := range g.Members {
			if core.MatchesSessionSelector(selector, m.Connector, m.Harness, m.Session) {
				result = g
				matches = append(matches, core.SessionSelector(m.Connector, m.Harness, m.Session))
			}
		}
	}
	if len(matches) > 1 {
		sort.Strings(matches)
		return Group{}, &engine.CodedError{Code: "AMBIGUOUS", Msg: fmt.Sprintf("%s matches multiple sessions: %s", selector, strings.Join(matches, ", "))}
	}
	if len(matches) == 0 {
		return Group{}, ErrNotFound{selector}
	}
	return result, nil
}

// NativeHistory follows only same-profile ancestry required by one session.
// A missing parent is an error; silently copying a suffix would lose context.
func (ss *Sessions) NativeHistory(source SessionRow) ([]SessionRow, error) {
	var history []SessionRow
	seen := map[string]bool{}
	for {
		k := keyOf(source.Harness, source.Session, source.Connector)
		if seen[k] {
			return nil, fmt.Errorf("cyclic native ancestry")
		}
		seen[k] = true
		history = append(history, source)
		if source.Parent == nil || source.Parent.Harness != source.Harness || core.Obj(source.Meta["_nativeLineage"])["copied"] == true || core.S(core.Obj(source.Meta["_nativeLineage"])["kind"]) == "spawn" {
			break
		}
		parent := source.Parent
		row, ok := ss.Store.Session(parent.Harness, parent.Session, source.Connector)
		if !ok || !ss.matchesProfile(row) {
			return nil, fmt.Errorf("required parent session unavailable: %s", keyOf(parent.Harness, parent.Session, source.Connector))
		}
		source = row
	}
	slices.Reverse(history)
	return history, nil
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
	ctx, finish, err := ss.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g, err := ss.ResolveGroup(selector)
	if err != nil {
		return nil, err
	}
	cacheID := g.ID
	recentSelector := g.ID
	_, _, _, nativeScope := core.ParseSessionSelector(selector)
	if nativeScope {
		// A native view needs its history, not descendants or siblings. Keep
		// whole-group reads for an explicit th: selector; comparison separately
		// reads the selected sessions and their same-profile ancestry.
		var selected SessionRow
		for _, m := range g.Members {
			if core.MatchesSessionSelector(selector, m.Connector, m.Harness, m.Session) {
				selected = m
				break
			}
		}
		cacheID = core.SessionSelector(selected.Connector, selected.Harness, selected.Session)
		recentSelector = cacheID
		g.Members, err = ss.NativeHistory(selected)
		if err != nil {
			return nil, err
		}
	}
	for {
		ss.mu.Lock()
		wait := ss.loads[cacheID]
		if wait == nil {
			wait = make(chan struct{})
			ss.loads[cacheID] = wait
			ss.mu.Unlock()
			defer func() { ss.mu.Lock(); delete(ss.loads, cacheID); close(wait); ss.mu.Unlock() }()
			break
		}
		ss.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-wait:
		}
	}
	cacheKey := func(members []SessionRow, latest bool) string {
		var kb strings.Builder
		for _, m := range members {
			current := m
			if latest {
				var ok bool
				current, ok = ss.Store.Session(m.Harness, m.Session, m.Connector)
				if !ok {
					return ""
				}
			}
			c := ss.Registry.Get(m.Connector)
			if c == nil || !ss.matchesProfile(current) {
				return ""
			}
			fmt.Fprintf(&kb, "%s:%s:%s|", keyOf(m.Harness, m.Session, m.Connector), DecoderFingerprint(c), RowRevision(current))
		}
		return kb.String()
	}
	ss.mu.Lock()
	if hit := ss.open[cacheID]; hit != nil {
		if key := cacheKey(hit.view.Group.Members, true); key != "" && hit.key == key {
			hit.used = time.Now()
			ss.touchRecentLocked(recentSelector)
			ss.mu.Unlock()
			return hit.view, nil
		}
		// A request may still hold this view. Drop the cache reference; GC reclaims it when done.
		delete(ss.open, cacheID)
	}
	ss.mu.Unlock()
	select {
	case ss.readSlots <- struct{}{}:
		defer func() { <-ss.readSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	h := hub.New()
	readSet, reading := map[string]bool{}, map[string]bool{}
	var readMembers []SessionRow
	var read func(SessionRow) error
	read = func(m SessionRow) error {
		k := keyOf(m.Harness, m.Session, m.Connector)
		if readSet[k] {
			return nil
		}
		if reading[k] {
			return fmt.Errorf("cyclic native ancestry")
		}
		c := ss.Registry.Get(m.Connector)
		if c == nil || c.Source == nil || m.Path == "" {
			if nativeScope {
				return fmt.Errorf("required source unavailable: %s", k)
			}
			return nil
		}
		reading[k] = true
		defer delete(reading, k)
		meta := core.JsonObject{}
		for key, value := range m.Meta {
			meta[key] = value
		}
		if m.Title != "" {
			meta["title"] = m.Title
		}
		ref := core.NativeRef{Harness: m.Harness, Connector: m.Connector, Session: m.Session, Path: m.Path, Size: m.Size, MtimeMs: m.Mtime, Meta: meta}
		err := c.Source.Tail(ctx, ref, nil, func(b core.SourceBatch) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			// A cold list row can precede its summary. Resolve an actual native
			// history reference before ingesting the first child batch, not by
			// assuming the provisional row has no ancestors.
			if l := b.Lineage; nativeScope && l != nil && !l.Copied && l.Kind != "spawn" {
				parent, ok := ss.Store.Session(l.Harness, l.Session, m.Connector)
				if l.Harness != m.Harness || !ok || !ss.matchesProfile(parent) {
					return fmt.Errorf("required parent session unavailable: %s", keyOf(l.Harness, l.Session, m.Connector))
				}
				if err := read(parent); err != nil {
					return err
				}
			}
			h.Ingest(b, hub.ConnectorInfo{ID: c.ID, Harness: c.Harness, Version: c.Version})
			return nil
		})
		if err != nil {
			return fmt.Errorf("read %s: %w", k, err)
		}
		readSet[k] = true
		readMembers = append(readMembers, m)
		return nil
	}
	for _, m := range g.Members {
		if err := read(m); err != nil {
			return nil, err
		}
	}
	g.Members = readMembers
	key := cacheKey(g.Members, false)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// The group's thread: the one most of its sessions landed in.
	counts := map[string]int{}
	var order []string
	for _, m := range g.Members {
		if b := h.Binding(m.Harness, m.Session, m.Connector); b != nil {
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
	if _, _, _, native := core.ParseSessionSelector(selector); native {
		for _, m := range g.Members {
			if core.MatchesSessionSelector(selector, m.Connector, m.Harness, m.Session) {
				if b := h.Binding(m.Harness, m.Session, m.Connector); b != nil {
					thread = b.Thread
					break
				}
			}
		}
	}
	e := engine.New(h, ss.Registry)
	e.Log, e.Locks = ss.Log, NewEngineLocks(ss.Store)
	e.Jobs = ss.Jobs
	// A working copy refreshes only its own sessions (a copy plans after a rescan): reading every
	// session on disk into it would mix conversations and cost the memory the index saves.
	own := map[string]bool{}
	for _, m := range g.Members {
		own[keyOf(m.Harness, m.Session, m.Connector)] = true
	}
	e.Filter = func(r core.NativeRef) bool { return own[keyOf(r.Harness, r.Session, r.Connector)] }
	view := &OpenThread{Hub: h, Engine: e, Thread: thread, Group: g}
	ss.mu.Lock()
	ss.touchRecentLocked(recentSelector)
	ss.mu.Unlock()
	ss.mu.Lock()
	ss.open[cacheID] = &openEntry{key: key, view: view, used: time.Now(), bytes: h.RetainedBytes()}
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
	ss.checkGenerationLocked()
	var out []*OpenThread
	for _, o := range ss.open {
		out = append(out, o.view)
	}
	return out
}

func (ss *Sessions) touchRecentLocked(selector string) {
	ss.recent = append([]string{selector}, slices.DeleteFunc(ss.recent, func(x string) bool { return x == selector })...)
	if len(ss.recent) > 32 {
		ss.recent = ss.recent[:32]
	}
}

// Recent preserves the actual native or explicit group scope for event lookup.
func (ss *Sessions) Recent() []string {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return append([]string(nil), ss.recent...)
}

// Close drops every working copy.
func (ss *Sessions) Close() {
	released := ss.dropViews(time.Time{})
	if released >= 32<<20 {
		debug.FreeOSMemory()
	}
}

func (ss *Sessions) dropViews(olderThan time.Time) int64 {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	var released int64
	for id, entry := range ss.open {
		if olderThan.IsZero() || !entry.used.After(olderThan) {
			released += entry.bytes
			delete(ss.open, id)
		}
	}
	if olderThan.IsZero() {
		ss.groups, ss.recent = nil, nil
	}
	return released
}

// SweepViews releases idle parsed conversations. Readers holding a view keep
// their own reference; removing a cache entry never mutates its working copy.
func (ss *Sessions) SweepViews(now time.Time) {
	if ss.dropViews(now.Add(-2*time.Minute)) >= 32<<20 {
		debug.FreeOSMemory()
	}
}
