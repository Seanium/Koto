// Package hub decodes native session paths in memory. No copy history or managed tree is stored.
package hub

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"koto/internal/core"

	"github.com/klauspost/compress/zstd"
)

// Binding is a native session of a conversation: where it stands (Leaf) and its metadata.
type Binding struct {
	Harness   string          `json:"harness"`
	Session   string          `json:"session"`
	Connector string          `json:"connector"`
	Thread    core.ThreadID   `json:"thread"`
	Leaf      string          `json:"leaf"` // "" = nothing yet
	Path      string          `json:"path"`
	Meta      core.JsonObject `json:"meta"`
	Trashed   string          `json:"trashed"`
	updated   int64
}

type IngestResult struct {
	Thread     core.ThreadID
	Added      int
	Deduped    int
	Skipped    int
	Superseded int
	Leaf       string
}

type ConnectorInfo struct {
	ID, Harness, Version, Frontend string
}

// DeliveryRecord is one record a delivery writes: its native id and the node it stands for.
type DeliveryRecord struct {
	NativeID string       `json:"nativeId"`
	EventID  core.EventID `json:"eventId"`
	SHA256   string       `json:"sha256"`
}

// IDRow maps a native record to the node it stands for.
type IDRow struct {
	Hash   string
	Event  core.EventID // "" for an alias with no target
	Origin string       // observed | alias
}

type nativeKey struct{ harness, session, native string }
type sessKey struct{ harness, session string }

type node struct {
	id       core.EventID
	thread   core.ThreadID
	kind     string
	actor    string
	parents  []core.EventID
	payload  []byte // 0|json or 1|zstd(json): {parts, replaces, supersedes, attrs}
	prov     core.Provenance
	time     *float64
	origin   string // harness/session that first wrote it
	seq      int
	turn     bool // a user message with text: a turn starts here
	turnsTo  int  // turns from the root (memo, -1 unknown)
	children []core.EventID
}

// Hub is one in-memory working copy. Safe for one goroutine at a time; callers serialize.
type Hub struct {
	events   map[core.EventID]*node
	order    map[core.ThreadID][]*node
	threads  map[core.ThreadID]bool
	idmap    map[nativeKey]*IDRow
	byNative map[[2]string][]nativeKey // harness, native id → keys
	byEvent  map[core.EventID][]nativeKey
	bindings map[sessKey]*Binding
	offsets  map[[2]string]core.SourceOffset
	pending  map[sessKey][]pendingRecord

	cas map[string][]byte

	seq     int
	updates int64
}

var enc, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1))
var dec, _ = zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))

// New makes an empty working copy.
func New() *Hub {
	return &Hub{
		events: map[core.EventID]*node{}, order: map[core.ThreadID][]*node{}, threads: map[core.ThreadID]bool{},
		idmap: map[nativeKey]*IDRow{}, byNative: map[[2]string][]nativeKey{}, byEvent: map[core.EventID][]nativeKey{},
		bindings: map[sessKey]*Binding{},
		offsets:  map[[2]string]core.SourceOffset{},
		pending:  map[sessKey][]pendingRecord{},
		cas:      map[string][]byte{},
	}
}

// Close drops the working copy.
func (h *Hub) Close() {
	*h = Hub{}
}

// RetainedBytes estimates cached payloads and index overhead, not total process memory.
func (h *Hub) RetainedBytes() int64 {
	n := int64(len(h.events))*1024 + int64(len(h.idmap))*512
	for _, event := range h.events {
		n += int64(len(event.payload))
	}
	for _, blob := range h.cas {
		n += int64(len(blob))
	}
	for _, records := range h.pending {
		for _, record := range records {
			n += int64(record.record.Bytes) + 1024
		}
	}
	return n
}

func nowMs() int64 { return time.Now().UnixMilli() }

// stamp is a strictly increasing "updated" value (bindings order by it).
func (h *Hub) stamp() int64 {
	t := nowMs()
	if t <= h.updates {
		t = h.updates + 1
	}
	h.updates = t
	return t
}

// ------------------------------------------------------------------ CAS

// PutBlob stores bytes by content and returns {sha256, bytes}.
func (h *Hub) PutBlob(b []byte) core.JsonObject {
	sha := core.SHA256Hex(b)
	if _, ok := h.cas[sha]; !ok {
		h.cas[sha] = append([]byte(nil), b...)
	}
	return core.JsonObject{"sha256": sha, "bytes": len(b)}
}

// Blob returns stored bytes.
func (h *Hub) Blob(sha string) ([]byte, bool) {
	b, ok := h.cas[sha]
	return b, ok
}

// ------------------------------------------------------------------ ingest

func (h *Hub) setID(k nativeKey, r *IDRow) {
	old := h.idmap[k]
	if old == nil {
		h.byNative[[2]string{k.harness, k.native}] = append(h.byNative[[2]string{k.harness, k.native}], k)
	} else if old.Event != "" && old.Event != r.Event {
		h.byEvent[old.Event] = removeKey(h.byEvent[old.Event], k)
	}
	if r.Event != "" && (old == nil || old.Event != r.Event) {
		h.byEvent[r.Event] = append(h.byEvent[r.Event], k)
	}
	h.idmap[k] = r
}

func removeKey(ks []nativeKey, k nativeKey) []nativeKey {
	for i, x := range ks {
		if x == k {
			return append(ks[:i:i], ks[i+1:]...)
		}
	}
	return ks
}

var ordinalRe = regexp.MustCompile(`^o(\d+)$`)

// Ingest adds what one read of a native session yields.
func (h *Hub) Ingest(batch core.SourceBatch, conn ConnectorInfo) IngestResult {
	ref := batch.Ref
	harness := ref.Harness
	binding := h.bindings[sessKey{harness, ref.Session}]
	lineage := batch.Lineage
	// A new Claude Code session whose first message reuses another session's uuid is a copy of it
	// (/branch before forkedFrom existed, a hand copy): CC uuids are random v4, so they never collide.
	if binding == nil && lineage == nil && harness == "claude-code" && len(batch.Records) > 0 && batch.Records[0].Index == 0 && len(batch.Drafts) > 0 {
		for _, k := range h.byNative[[2]string{harness, batch.Drafts[0].NativeID}] {
			r := h.idmap[k]
			if k.session != ref.Session && r.Event != "" && r.Origin != "alias" {
				lineage = &core.Lineage{Kind: "fork", Harness: harness, Session: k.session, Copied: true}
				break
			}
		}
	}
	lineageBase := ""
	if lineage != nil {
		pb := h.bindings[sessKey{lineage.Harness, lineage.Session}]
		if lineage.AfterNative != "" {
			if r := h.idmap[nativeKey{lineage.Harness, lineage.Session, lineage.AfterNative}]; r != nil {
				lineageBase = r.Event
			}
			// An ordinal on a record that made no event (a Codex task_complete): the last event at or before it.
			if m := ordinalRe.FindStringSubmatch(lineage.AfterNative); lineageBase == "" && m != nil {
				want, _ := strconv.Atoi(m[1])
				best := -1
				for k, r := range h.idmap {
					if k.harness != lineage.Harness || k.session != lineage.Session || r.Event == "" {
						continue
					}
					if mm := ordinalRe.FindStringSubmatch(k.native); mm != nil {
						n, _ := strconv.Atoi(mm[1])
						if n <= want && n > best {
							best, lineageBase = n, r.Event
						}
					}
				}
			}
		} else if pb != nil && pb.Leaf != "" {
			lineageBase = pb.Leaf
		}
		if binding == nil && pb != nil {
			binding = h.createBinding(harness, ref.Session, conn.ID, pb.Thread, ref.Path)
		}
	}
	if binding == nil {
		binding = h.createBinding(harness, ref.Session, conn.ID, core.ThreadIDFor(harness, ref.Session), ref.Path)
	}
	thread := binding.Thread
	res := IngestResult{Thread: thread, Leaf: binding.Leaf}

	// A batch that starts at the session's first record chains from the lineage base; an incremental
	// batch continues from the binding's leaf. A copied prefix chains from the root instead.
	base := lineageBase
	if lineage != nil && lineage.Copied {
		base = ""
	}
	prev := binding.Leaf
	if prev == "" || batch.Rewritten != nil || (len(batch.Records) > 0 && batch.Records[0].Index == 0) {
		prev = base
	}
	drafts := make(map[string]*core.DraftEvent, len(batch.Drafts))
	for i := range batch.Drafts {
		drafts[batch.Drafts[i].NativeID] = &batch.Drafts[i]
	}
	blobBytes := map[string][]byte{}
	for _, b := range batch.Blobs {
		blobBytes[core.SHA256Hex(b)] = b
	}
	edited := false
	holder := harness + "/" + ref.Session
	for _, rec := range h.causalRecords(batch, drafts, blobBytes) {
		d := drafts[rec.NativeID]
		key := nativeKey{harness, ref.Session, rec.NativeID}
		existing := h.idmap[key]
		if d == nil {
			// A record without an event but with a native id: an alias, so children pointing at it
			// (CC parentUuid chains through system/attachment records) resolve to its parent.
			if rec.NativeID != "" && !strings.HasPrefix(rec.NativeID, "#") && existing == nil {
				target := prev
				if rec.Alias != nil {
					ps := h.resolveParent(rec.Alias, prev, harness, ref.Session)
					target = ""
					if len(ps) > 0 {
						target = ps[0]
					}
				}
				h.setID(key, &IDRow{Hash: rec.SHA256, Event: target, Origin: "alias"})
			}
			continue
		}
		if existing != nil && existing.Hash == rec.SHA256 && existing.Event != "" {
			// Unchanged — unless an earlier record of this batch was edited in place (agy rewrites
			// steps): then it is re-parented onto the edited chain as a superseding event.
			if !edited || h.parentOf(existing.Event) == prev {
				prev = existing.Event
				res.Skipped++
				continue
			}
		}
		parents := h.resolveParent(d.Parent, prev, harness, ref.Session)
		var replaces *core.Replaces
		if d.Replaces != nil {
			replaces = h.resolveReplaces(d.Replaces, harness, ref.Session)
		}
		supersedes := ""
		if existing != nil && existing.Event != "" && existing.Origin != "alias" {
			supersedes = existing.Event
		}
		ident := core.IdentifyEv1(core.IdentityInput{Thread: thread, Parents: parents, Kind: d.Kind, Actor: d.Actor, Parts: d.Parts, Replaces: replaces, Supersedes: supersedes})
		for _, text := range ident.Blobs {
			h.PutBlob([]byte(text))
		}
		for sha := range CollectBlobRefs(core.PartsJSON(d.Parts)) {
			if b, ok := blobBytes[sha]; ok {
				h.PutBlob(b)
			}
		}
		idx := rec.Index
		identity := d.Identity
		if identity == "" {
			identity = "native"
		}
		prov := core.Provenance{
			Source:   core.Source{Connector: conn.ID, Harness: harness, Version: conn.Version, Frontend: conn.Frontend},
			Native:   core.Native{Session: ref.Session, ID: rec.NativeID, Index: &idx, Offset: rec.Offset},
			Identity: identity,
			Residual: &core.Residual{SHA256: rec.SHA256, Bytes: rec.Bytes},
		}
		inserted := h.insert(ident.ID, thread, d.Kind, d.Actor, parents, ident.Stored, replaces, supersedes, d.Attrs, d.Time, prov, holder, hasText(d))
		if inserted {
			res.Added++
		} else {
			res.Deduped++
		}
		if supersedes != "" {
			res.Superseded++
			edited = true
		}
		h.setID(key, &IDRow{Hash: rec.SHA256, Event: ident.ID, Origin: "observed"})
		prev = ident.ID
	}
	meta := core.JsonObject{}
	for k, v := range binding.Meta {
		meta[k] = v
	}
	for k, v := range batch.Meta {
		meta[k] = v
	}
	for k, v := range ref.Meta {
		meta[k] = v
	}
	binding.Leaf = prev
	if ref.Path != "" {
		binding.Path = ref.Path
	}
	binding.Meta = meta
	binding.updated = h.stamp()
	h.offsets[[2]string{conn.ID, ref.Session}] = batch.Offset
	res.Leaf = prev
	return res
}

func hasText(d *core.DraftEvent) bool {
	if d.Kind != "message" || d.Actor != "user" {
		return false
	}
	for _, p := range d.Parts {
		if t := core.S(p["type"]); t == "text" || t == "image" || t == "file" || t == "opaque" {
			return true
		}
	}
	return false
}

func (h *Hub) resolveReplaces(r *core.DraftReplaces, harness, session string) *core.Replaces {
	to := h.resolveRef(r.To, harness, session)
	if to != "" && r.ToExclusive {
		to = h.parentOf(to)
	}
	if to == "" {
		return nil
	}
	from := ""
	if r.From != nil {
		from = h.resolveRef(r.From, harness, session)
	}
	if from == "" {
		from = h.RootOf(to)
	}
	if from == "" {
		return nil
	}
	return &core.Replaces{From: from, To: to}
}

// RootOf is the root of id's first-parent path.
func (h *Hub) RootOf(id core.EventID) core.EventID {
	cur := id
	seen := map[string]bool{}
	for {
		if seen[cur] {
			return cur
		}
		seen[cur] = true
		p := h.parentOf(cur)
		if p == "" {
			return cur
		}
		cur = p
	}
}

func (h *Hub) parentOf(id core.EventID) core.EventID {
	if n := h.events[id]; n != nil && len(n.parents) > 0 {
		return n.parents[0]
	}
	return ""
}

// ParentOf is the first parent of id ("" for a root or an unknown node).
func (h *Hub) ParentOf(id core.EventID) core.EventID { return h.parentOf(id) }

func (h *Hub) resolveRef(r *core.ParentRef, harness, session string) core.EventID {
	if r == nil || r.Null {
		return ""
	}
	if r.Event != "" {
		return r.Event
	}
	hs, ss := harness, session
	if r.Harness != "" {
		hs = r.Harness
	}
	if r.Session != "" {
		ss = r.Session
	}
	if row := h.idmap[nativeKey{hs, ss, r.Native}]; row != nil {
		return row.Event
	}
	return ""
}

func (h *Hub) resolveParent(p *core.ParentRef, prev core.EventID, harness, session string) []core.EventID {
	if p == nil {
		if prev != "" {
			return []core.EventID{prev}
		}
		return []core.EventID{}
	}
	if p.Null {
		return []core.EventID{}
	}
	if id := h.resolveRef(p, harness, session); id != "" {
		return []core.EventID{id}
	}
	// Dangling native parent (record not seen / skipped without alias): fall back to stream order.
	if prev != "" {
		return []core.EventID{prev}
	}
	return []core.EventID{}
}

type payloadJSON struct {
	Parts      []any           `json:"parts"`
	Replaces   *core.Replaces  `json:"replaces,omitempty"`
	Supersedes string          `json:"supersedes,omitempty"`
	Attrs      core.JsonObject `json:"attrs,omitempty"`
}

func encodePayload(p payloadJSON) []byte {
	raw := []byte(core.ToJSON(p))
	if len(raw) < 512 {
		return append([]byte{0}, raw...)
	}
	return append([]byte{1}, enc.EncodeAll(raw, nil)...)
}

func decodePayload(b []byte) payloadJSON {
	var body []byte
	if len(b) > 0 && b[0] == 1 {
		body, _ = dec.DecodeAll(b[1:], nil)
	} else if len(b) > 0 {
		body = b[1:]
	}
	var out struct {
		Parts      []any           `json:"parts"`
		Replaces   *core.Replaces  `json:"replaces"`
		Supersedes string          `json:"supersedes"`
		Attrs      core.JsonObject `json:"attrs"`
	}
	d := jsonDecoder(body)
	_ = d.Decode(&out)
	return payloadJSON(out)
}

func (h *Hub) insert(id core.EventID, thread core.ThreadID, kind, actor string, parents []core.EventID, stored []any, replaces *core.Replaces, supersedes string, attrs core.JsonObject, t *float64, prov core.Provenance, holder string, turn bool) bool {
	if _, ok := h.events[id]; ok {
		return false
	}
	h.seq++
	n := &node{id: id, thread: thread, kind: kind, actor: actor, parents: parents, prov: prov, time: t, origin: holder, seq: h.seq, turn: turn, turnsTo: -1,
		payload: encodePayload(payloadJSON{Parts: stored, Replaces: replaces, Supersedes: supersedes, Attrs: attrs})}
	h.events[id] = n
	h.order[thread] = append(h.order[thread], n)
	for _, p := range parents {
		if pn := h.events[p]; pn != nil {
			pn.children = append(pn.children, id)
		}
	}
	return true
}

func (h *Hub) createBinding(harness, session, connector string, thread core.ThreadID, path string) *Binding {
	h.threads[thread] = true
	b := &Binding{Harness: harness, Session: session, Connector: connector, Thread: thread, Path: path, Meta: core.JsonObject{}, updated: h.stamp()}
	h.bindings[sessKey{harness, session}] = b
	return b
}

// ------------------------------------------------------------------ reads

// LookupNative is the id_map row of a native record.
func (h *Hub) LookupNative(harness, session, nativeID string) *IDRow {
	return h.idmap[nativeKey{harness, session, nativeID}]
}

// NativeRef is a native record standing for an event.
type NativeRef struct {
	Session, NativeID, Origin string
}

// NativeIDsOf are the native records (in one harness, optionally one session) that map to an event.
func (h *Hub) NativeIDsOf(eventID core.EventID, harness, session string) []NativeRef {
	var out []NativeRef
	for _, k := range h.byEvent[eventID] {
		r := h.idmap[k]
		if r == nil || r.Origin == "alias" || k.harness != harness || (session != "" && k.session != session) {
			continue
		}
		out = append(out, NativeRef{Session: k.session, NativeID: k.native, Origin: r.Origin})
	}
	return out
}

// Binding of a native session, or nil.
func (h *Hub) Binding(harness, session string) *Binding {
	b := h.bindings[sessKey{harness, session}]
	if b == nil {
		return nil
	}
	c := *b
	return &c
}

// Bindings of a thread ("" = all), oldest update first.
func (h *Hub) Bindings(thread core.ThreadID) []Binding {
	var out []*Binding
	for _, b := range h.bindings {
		if thread == "" || b.Thread == thread {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].updated < out[j].updated })
	res := make([]Binding, len(out))
	for i, b := range out {
		res[i] = *b
	}
	return res
}

// ThreadRow summarizes a thread.
type ThreadRow struct {
	ID       core.ThreadID `json:"id"`
	Events   int           `json:"events"`
	Bindings int           `json:"bindings"`
	Updated  int64         `json:"updated"`
}

// Threads with events, newest first.
func (h *Hub) Threads() []ThreadRow {
	var out []ThreadRow
	for t := range h.threads {
		if len(h.order[t]) == 0 {
			continue
		}
		r := ThreadRow{ID: t, Events: len(h.order[t])}
		for _, b := range h.bindings {
			if b.Thread == t {
				r.Bindings++
				if b.updated > r.Updated {
					r.Updated = b.updated
				}
			}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated > out[j].Updated })
	return out
}

// Leaves (versions) of a thread, in insertion order.
func (h *Hub) Leaves(thread core.ThreadID) []core.EventID {
	var out []core.EventID
	for _, n := range h.order[thread] {
		if len(n.children) == 0 {
			out = append(out, n.id)
		}
	}
	return out
}

// MergeMeta merges metadata into a binding.
func (h *Hub) MergeMeta(harness, session string, meta core.JsonObject) {
	b := h.bindings[sessKey{harness, session}]
	if b == nil {
		return
	}
	for k, v := range meta {
		b.Meta[k] = v
	}
}

func (h *Hub) HasChildren(id core.EventID) bool {
	n := h.events[id]
	return n != nil && len(n.children) > 0
}

func (h *Hub) HasEvent(id core.EventID) bool { _, ok := h.events[id]; return ok }

// Children of a node.
func (h *Hub) Children(id core.EventID) []core.EventID {
	if n := h.events[id]; n != nil {
		return append([]core.EventID(nil), n.children...)
	}
	return nil
}

// GetEvent decodes a node (nil when absent).
func (h *Hub) GetEvent(id core.EventID) *core.Event {
	n := h.events[id]
	if n == nil {
		return nil
	}
	p := decodePayload(n.payload)
	var missing []any
	parts := core.Rehydrate(p.Parts, func(sha string) string {
		if b, ok := h.cas[sha]; ok {
			return string(b)
		}
		missing = append(missing, sha)
		return "[koto: content unavailable (blob " + sha[:12] + ")]"
	})
	e := &core.Event{ID: n.id, Parents: append([]core.EventID{}, n.parents...), Thread: n.thread, Kind: n.kind, Actor: n.actor,
		Parts: core.Parts(parts), Replaces: p.Replaces, Supersedes: p.Supersedes, Time: n.time, Attrs: p.Attrs, Provenance: n.prov}
	if e.Parts == nil {
		e.Parts = []core.Part{}
	}
	if len(missing) > 0 {
		if e.Attrs == nil {
			e.Attrs = core.JsonObject{}
		}
		e.Attrs["kotoMissingBlobs"] = missing
	}
	return e
}

// Kind and actor of a node without decoding it.
func (h *Hub) KindActor(id core.EventID) (string, string) {
	if n := h.events[id]; n != nil {
		return n.kind, n.actor
	}
	return "", ""
}

// Time of a node (nil when unknown).
func (h *Hub) Time(id core.EventID) *float64 {
	if n := h.events[id]; n != nil {
		return n.time
	}
	return nil
}

// Source of a node (the harness and session it was first read from).
func (h *Hub) Source(id core.EventID) (harness, session string) {
	if n := h.events[id]; n != nil {
		return n.prov.Source.Harness, n.prov.Native.Session
	}
	return "", ""
}

// PathIDs are the ids from the root to head along first parents.
func (h *Hub) PathIDs(head core.EventID) []core.EventID {
	var out []core.EventID
	seen := map[string]bool{}
	for cur := head; cur != "" && !seen[cur]; cur = h.parentOf(cur) {
		if _, ok := h.events[cur]; !ok {
			break
		}
		seen[cur] = true
		out = append(out, cur)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Path decodes root → head.
func (h *Hub) Path(head core.EventID) []*core.Event {
	ids := h.PathIDs(head)
	out := make([]*core.Event, 0, len(ids))
	for _, id := range ids {
		if e := h.GetEvent(id); e != nil {
			out = append(out, e)
		}
	}
	return out
}

// PathTail is the end of head's path: the last `turns` turns, or the `turns` before node `before`,
// stopping at maxEvents (mid-turn if need be). Offset is where the slice starts on the path.
func (h *Hub) PathTail(head core.EventID, turns int, before core.EventID, maxEvents int) (events []*core.Event, offset, total int) {
	ids := h.PathIDs(head)
	end := len(ids)
	if before != "" {
		for i, id := range ids {
			if id == before {
				end = i
				break
			}
		}
	}
	if maxEvents <= 0 {
		maxEvents = 1500
	}
	start, found := end, 0
	for start > 0 && end-start < maxEvents {
		start--
		if h.IsTurnStart(ids[start]) {
			found++
			if found >= turns {
				break
			}
		}
	}
	for _, id := range ids[start:end] {
		if e := h.GetEvent(id); e != nil {
			events = append(events, e)
		}
	}
	return events, start, len(ids)
}

// IsTurnStart includes a leading reply-only excerpt as a standalone turn.
func (h *Hub) IsTurnStart(id core.EventID) bool {
	n := h.events[id]
	if n == nil {
		return false
	}
	if n.turn {
		return true
	}
	if h.TurnsAt(id) != 1 || (n.kind != "message" && n.kind != "tool_result" && n.kind != "compaction") {
		return false
	}
	return len(n.parents) == 0 || h.TurnsAt(n.parents[0]) == 0
}

// TurnsAt is how many turns the path from the root to id holds.
func (h *Hub) TurnsAt(id core.EventID) int {
	var stack []*node
	for cur := h.events[id]; cur != nil && cur.turnsTo < 0; {
		stack = append(stack, cur)
		if len(cur.parents) == 0 {
			break
		}
		cur = h.events[cur.parents[0]]
	}
	for i := len(stack) - 1; i >= 0; i-- {
		n := stack[i]
		base := 0
		if len(n.parents) > 0 {
			if p := h.events[n.parents[0]]; p != nil && p.turnsTo >= 0 {
				base = p.turnsTo
			}
		}
		if n.turn || base == 0 && (n.kind == "message" || n.kind == "tool_result" || n.kind == "compaction") {
			base++
		}
		n.turnsTo = base
	}
	if n := h.events[id]; n != nil && n.turnsTo >= 0 {
		return n.turnsTo
	}
	return 0
}

// IsAncestor: is ancestor on of's first-parent path (or the same node)?
func (h *Hub) IsAncestor(ancestor, of core.EventID) bool {
	if ancestor == of {
		return true
	}
	if h.events[ancestor] == nil {
		return false
	}
	// A delivered record can reference an event whose source is read later. Insertion order is
	// therefore not ancestry order; follow parent IDs, bounded even if an imported graph is bad.
	for cur, steps := h.parentOf(of), 0; cur != "" && steps < len(h.events); cur, steps = h.parentOf(cur), steps+1 {
		if cur == ancestor {
			return true
		}
	}
	return false
}

// ThreadEvents decodes every node of a thread in insertion order.
func (h *Hub) ThreadEvents(thread core.ThreadID) []*core.Event {
	out := []*core.Event{}
	for _, n := range h.order[thread] {
		out = append(out, h.GetEvent(n.id))
	}
	return out
}

// ThreadHead: among the sessions' leaves, drop any another leaf moved past; of the rest prefer the
// session most recently updated. Public APIs select a native session explicitly.
func (h *Hub) ThreadHead(thread core.ThreadID) core.EventID {
	var bs []*Binding
	for _, b := range h.bindings {
		if b.Thread == thread && b.Leaf != "" {
			bs = append(bs, b)
		}
	}
	sort.Slice(bs, func(i, j int) bool { return bs[i].updated > bs[j].updated })
	var tips []*Binding
	for _, b := range bs {
		moved := false
		for _, o := range bs {
			if o.Leaf != b.Leaf && h.IsAncestor(b.Leaf, o.Leaf) {
				moved = true
				break
			}
		}
		if !moved {
			tips = append(tips, b)
		}
	}
	if len(tips) > 0 {
		return tips[0].Leaf
	}
	if len(bs) > 0 {
		return bs[0].Leaf
	}
	return ""
}

// ErrAmbiguous: a selector matches more than one node.
var ErrAmbiguous = errors.New("ambiguous")

var shortSel = regexp.MustCompile(`^ev:([0-9a-f]{4,64})$`)

// ResolveHead resolves a selector: event id/prefix (or ev:<hex>), thread id/prefix, or harness:session.
func (h *Hub) ResolveHead(selector string) (core.ThreadID, core.EventID, error) {
	if strings.HasPrefix(selector, "ev") {
		prefix := selector
		if m := shortSel.FindStringSubmatch(selector); m != nil {
			prefix = "ev1:sha256:" + m[1]
		}
		var hit *node
		for id, n := range h.events {
			if strings.HasPrefix(id, prefix) {
				if hit != nil {
					return "", "", fmt.Errorf("%w: %s matches more than one node; give more of its id", ErrAmbiguous, selector)
				}
				hit = n
			}
		}
		if hit == nil {
			return "", "", nil
		}
		return hit.thread, hit.id, nil
	}
	if strings.HasPrefix(selector, "th:") {
		for t := range h.threads {
			if strings.HasPrefix(t, selector) {
				head := h.ThreadHead(t)
				if head == "" {
					return "", "", nil
				}
				return t, head, nil
			}
		}
		return "", "", nil
	}
	if i := strings.Index(selector, ":"); i > 0 {
		harness, session := selector[:i], selector[i+1:]
		var best *Binding
		for k, b := range h.bindings {
			if k.harness == harness && strings.HasPrefix(k.session, session) && (best == nil || b.updated > best.updated) {
				best = b
			}
		}
		if best != nil && best.Leaf != "" {
			return best.Thread, best.Leaf, nil
		}
	}
	return "", "", nil
}

// ------------------------------------------------------------------ offsets

func (h *Hub) Offset(connector, session string) (core.SourceOffset, bool) {
	o, ok := h.offsets[[2]string{connector, session}]
	return o, ok
}

func (h *Hub) SetOffset(connector, session string, o core.SourceOffset) {
	h.offsets[[2]string{connector, session}] = o
}

// ShortID is ev:<10 hex>, accepted wherever a selector is.
func ShortID(id core.EventID) string {
	i := strings.LastIndex(id, ":")
	s := id[i+1:]
	if len(s) > 10 {
		s = s[:10]
	}
	return "ev:" + s
}

var spaces = regexp.MustCompile(`\s+`)

// Preview is one line of an event's text (tool calls as [name]), up to 120 characters.
func Preview(e *core.Event) string {
	if e == nil {
		return ""
	}
	var bits []string
	var walk func(ps []any)
	walk = func(ps []any) {
		for _, x := range ps {
			p := core.Obj(x)
			switch core.S(p["type"]) {
			case "text":
				if t := core.S(p["text"]); t != "" {
					bits = append(bits, t)
				}
			case "tool_call":
				bits = append(bits, "["+core.S(p["rawName"])+"]")
			case "tool_result":
				walk(core.Arr(p["parts"]))
			}
		}
	}
	walk(core.PartsJSON(e.Parts))
	t := strings.TrimSpace(spaces.ReplaceAllString(strings.Join(bits, " "), " "))
	r := []rune(t)
	if len(r) > 120 {
		return string(r[:119]) + "…"
	}
	return t
}

// CollectBlobRefs are the sha256s of blobs parts point at.
func CollectBlobRefs(parts any) map[string]bool {
	out := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			if b, ok := x["blob"].(map[string]any); ok {
				if s, ok := b["sha256"].(string); ok {
					out[s] = true
				}
			}
			if s, ok := x["sealed"]; ok {
				walk([]any{s})
			}
			if ps, ok := x["parts"].([]any); ok {
				walk(ps)
			}
		}
	}
	walk(parts)
	return out
}

func jsonDecoder(b []byte) interface{ Decode(any) error } {
	return newDecoder(bytes.NewReader(b))
}

// EventCount is how many nodes the working copy holds.
func (h *Hub) EventCount() int { return len(h.events) }
