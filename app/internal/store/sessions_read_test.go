package store

import (
	"context"
	"errors"
	"koto/internal/core"
	"koto/internal/engine"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type readFixture struct {
	mu         sync.Mutex
	blockIndex chan struct{}
	block      map[string]chan struct{}
	started    chan string
	calls      map[string]int
	lineages   map[string]*core.Lineage
}

func (f *readFixture) Discover(context.Context) ([]core.NativeRef, error) {
	var refs []core.NativeRef
	for _, id := range []string{"a", "b", "c"} {
		refs = append(refs, core.NativeRef{Harness: "fixture", Session: id, Path: "/fixture/" + id, Meta: core.JsonObject{"title": "Title " + id, "cwd": "/fixture"}})
	}
	return refs, nil
}
func (f *readFixture) WatchRoots() []string { return nil }
func (f *readFixture) Tail(ctx context.Context, r core.NativeRef, _ *core.SourceOffset, yield func(core.SourceBatch) error) error {
	light := engine.IsIndexOnly(ctx)
	f.mu.Lock()
	gate := f.block[r.Session]
	lineage := f.lineages[r.Session]
	if light {
		gate = f.blockIndex
	} else {
		f.calls[r.Session]++
	}
	f.mu.Unlock()
	if gate != nil {
		f.started <- r.Session
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	id := r.Session + "-message"
	at := float64(1)
	return yield(core.SourceBatch{Ref: r, Lineage: lineage, Records: []core.BatchRecord{{NativeID: id, Index: 0, SHA256: core.SHA256Hex(id)}}, Drafts: []core.DraftEvent{{NativeID: id, Actor: "user", Kind: "message", Time: &at, Parts: []core.Part{{"type": "text", "text": "Prompt " + r.Session}}}}, Offset: core.SourceOffset{Records: 1, PrefixHash: core.SHA256Hex(id)}})
}
func readSessions(t *testing.T) (*Sessions, *readFixture) {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "index"))
	if e != nil {
		t.Fatal(e)
	}
	f := &readFixture{block: map[string]chan struct{}{}, started: make(chan string, 10), calls: map[string]int{}}
	reg := engine.NewRegistry()
	if e = reg.Register(&engine.Connector{ID: "fixture-root", Harness: "fixture", Root: "/fixture", Version: "1", Source: f}); e != nil {
		t.Fatal(e)
	}
	ss := NewSessions(s, reg)
	t.Cleanup(func() { ss.Shutdown(); s.Close() })
	return ss, f
}
func awaitRead(t *testing.T, ch <-chan string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("read did not start")
	}
}
func TestColdDiscoveryVisibleBeforeHistoryAndNativeNavigationStable(t *testing.T) {
	ss, f := readSessions(t)
	f.blockIndex = make(chan struct{})
	done := make(chan engine.ScanReport, 1)
	go func() { done <- ss.Scan(context.Background(), nil, nil) }()
	awaitRead(t, f.started)
	rows := ss.Rows()
	if len(rows) != 3 || rows[0].Title == "" || rows[0].Cwd == "" {
		t.Fatal("discovery is not usable before full history")
	}
	selector := core.SessionSelector("fixture-root", "fixture", "a")
	v, e := ss.OpenThread(context.Background(), selector)
	if e != nil {
		t.Fatal(e)
	}
	if v.Hub.Binding("fixture", "a", "fixture-root") == nil {
		t.Fatal("wrong native binding")
	}
	close(f.blockIndex)
	if r := <-done; len(r.Errors) > 0 {
		t.Fatal(r.Errors)
	}
	v, e = ss.OpenThread(context.Background(), selector)
	if e != nil {
		t.Fatal(e)
	}
	if v.Hub.Binding("fixture", "a", "fixture-root") == nil {
		t.Fatal("enrichment changed selection")
	}
	f.mu.Lock()
	reads := f.calls["a"]
	f.mu.Unlock()
	if reads != 1 {
		t.Fatalf("enrichment redecoded unchanged source %d times", reads)
	}
}
func TestCanceledReaderDoesNotBlockOtherConversationOrCachePartial(t *testing.T) {
	ss, f := readSessions(t)
	ss.Scan(context.Background(), nil, nil)
	f.block["a"] = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := ss.OpenThread(ctx, "fixture:a"); done <- e }()
	awaitRead(t, f.started)
	other, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if _, e := ss.OpenThread(other, "fixture:b"); e != nil {
		t.Fatal(e)
	}
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	f.mu.Lock()
	delete(f.block, "a")
	f.mu.Unlock()
	if _, e := ss.OpenThread(context.Background(), "fixture:a"); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	n := f.calls["a"]
	f.mu.Unlock()
	if n != 2 {
		t.Fatalf("partial result cached: %d reads", n)
	}
}
func TestConcurrentSameConversationDeduplicatesAndGenerationKeepsCache(t *testing.T) {
	ss, f := readSessions(t)
	ss.Scan(context.Background(), nil, nil)
	f.block["a"] = make(chan struct{})
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, e := ss.OpenThread(context.Background(), "fixture:a"); done <- e }()
	}
	awaitRead(t, f.started)
	close(f.block["a"])
	for i := 0; i < 2; i++ {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
	if e := ss.Store.bumpIndexGeneration(); e != nil {
		t.Fatal(e)
	}
	ss.Groups()
	if _, e := ss.OpenThread(context.Background(), "fixture:a"); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	n := f.calls["a"]
	f.mu.Unlock()
	if n != 1 {
		t.Fatalf("unchanged conversation decoded %d times", n)
	}
}
func TestShutdownCancelsOwnedReadersAndRejectsNewReads(t *testing.T) {
	ss, f := readSessions(t)
	ss.Scan(context.Background(), nil, nil)
	f.block["a"] = make(chan struct{})
	done := make(chan error, 1)
	go func() { _, e := ss.OpenThread(context.Background(), "fixture:a"); done <- e }()
	awaitRead(t, f.started)
	stopped := make(chan struct{})
	go func() { ss.Shutdown(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not join canceled reader")
	}
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e := ss.OpenThread(context.Background(), "fixture:b"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if len(ss.Views()) != 0 {
		t.Fatal("shutdown retained views")
	}
}

func TestNativeReadSkipsDescendantsAndNeedsParents(t *testing.T) {
	ss, f := readSessions(t)
	ss.Scan(context.Background(), nil, nil)
	for child, parent := range map[string]string{"b": "a", "c": "b"} {
		r, _ := ss.Store.Session("fixture", child, "fixture-root")
		r.Parent = &Ref{Harness: "fixture", Session: parent}
		if err := ss.Store.PutSession(r); err != nil {
			t.Fatal(err)
		}
	}
	ss.Invalidate()
	// A broken descendant must not block opening its parent or a sibling.
	f.block["c"] = make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	v, err := ss.OpenThread(ctx, "fixture:b")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Group.Members) != 2 {
		t.Fatal("native read did not restrict ancestry")
	}
	f.mu.Lock()
	calls := map[string]int{}
	for k, n := range f.calls {
		calls[k] = n
	}
	f.mu.Unlock()
	if calls["a"] != 1 || calls["b"] != 1 || calls["c"] != 0 {
		t.Fatalf("read unrelated history: %v", calls)
	}
	if _, err = ss.OpenThread(ctx, "fixture:b"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	delete(f.block, "c")
	f.mu.Unlock()
	all, err := ss.OpenThread(ctx, v.Group.ID)
	if err != nil || len(all.Group.Members) != 3 {
		t.Fatalf("explicit group lost members: %v", err)
	}
	// Missing required ancestry cannot be reported as a complete native copy.
	r, _ := ss.Store.Session("fixture", "b", "fixture-root")
	r.Parent = &Ref{Harness: "fixture", Session: "missing"}
	if err = ss.Store.PutSession(r); err != nil {
		t.Fatal(err)
	}
	ss.Invalidate()
	if _, err = ss.OpenThread(ctx, "fixture:b"); err == nil {
		t.Fatal("missing parent silently dropped")
	}
}

func TestIdleViewSweepPreservesActiveReader(t *testing.T) {
	ss, _ := readSessions(t)
	ss.Scan(context.Background(), nil, nil)
	v, err := ss.OpenThread(context.Background(), "fixture:a")
	if err != nil {
		t.Fatal(err)
	}
	ss.SweepViews(time.Now().Add(time.Minute))
	if len(ss.Views()) != 1 {
		t.Fatal("recent view evicted")
	}
	ss.SweepViews(time.Now().Add(3 * time.Minute))
	if len(ss.Views()) != 0 {
		t.Fatal("idle view retained")
	}
	if v.Hub.Binding("fixture", "a", "fixture-root") == nil {
		t.Fatal("active reader was destroyed")
	}
}

func TestPendingNativeReadResolvesActualAncestryAndKeepsCache(t *testing.T) {
	ss, f := readSessions(t)
	f.lineages = map[string]*core.Lineage{"b": {Kind: "continue", Harness: "fixture", Session: "a"}}
	f.blockIndex = make(chan struct{})
	done := make(chan engine.ScanReport, 1)
	go func() { done <- ss.Scan(context.Background(), nil, nil) }()
	awaitRead(t, f.started)
	v, err := ss.OpenThread(context.Background(), "fixture:b")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Group.Members) != 2 {
		t.Fatal("pending source lost real ancestry")
	}
	b := v.Hub.Binding("fixture", "b", "fixture-root")
	if b == nil || len(v.Hub.Path(b.Leaf)) != 2 {
		t.Fatal("cold child returned suffix without parent")
	}
	close(f.blockIndex)
	if r := <-done; len(r.Errors) > 0 {
		t.Fatal(r.Errors)
	}
	again, err := ss.OpenThread(context.Background(), "fixture:b")
	if err != nil || again.Hub != v.Hub {
		t.Fatalf("summary enrichment reloaded same native scope: %v", err)
	}
	ss.SweepViews(time.Now().Add(3 * time.Minute))
	recent := ss.Recent()
	if len(recent) == 0 || recent[0] != core.SessionSelector("fixture-root", "fixture", "b") {
		t.Fatalf("recent stored ancestor instead of selected native: %v", recent)
	}
	reopened, err := ss.OpenThread(context.Background(), recent[0])
	if err != nil || reopened.Hub.GetEvent(b.Leaf) == nil {
		t.Fatalf("reopened native scope lost descendant event: %v", err)
	}
}

func TestCopiedLineageDoesNotRequireOriginal(t *testing.T) {
	ss, f := readSessions(t)
	f.lineages = map[string]*core.Lineage{"b": {Kind: "fork", Harness: "fixture", Session: "missing", Copied: true}}
	if r := ss.Scan(context.Background(), nil, nil); len(r.Errors) > 0 {
		t.Fatal(r.Errors)
	}
	v, err := ss.OpenThread(context.Background(), "fixture:b")
	if err != nil || len(v.Group.Members) != 1 {
		t.Fatalf("self-contained native copy requires original: %v", err)
	}
	r, _ := ss.Store.Session("fixture", "b", "fixture-root")
	if r.Parent == nil || core.Obj(r.Meta["_nativeLineage"])["copied"] != true {
		t.Fatal("thin index lost lineage semantics")
	}
}

func TestRequiredNativeAncestorCannotBeSilentlySkipped(t *testing.T) {
	for _, state := range []string{"missing-path", "trashed"} {
		t.Run(state, func(t *testing.T) {
			ss, f := readSessions(t)
			f.lineages = map[string]*core.Lineage{"b": {Kind: "continue", Harness: "fixture", Session: "a"}}
			ss.Scan(context.Background(), nil, nil)
			r, _ := ss.Store.Session("fixture", "a", "fixture-root")
			if state == "missing-path" {
				r.Path = ""
				if err := ss.Store.PutSession(r); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := ss.Store.AddTrash(TrashEntry{TrashID: "fixture-trash", Items: []TrashItem{{Harness: "fixture", Connector: "fixture-root", Session: "a"}}}); err != nil {
					t.Fatal(err)
				}
			}
			ss.Invalidate()
			if _, err := ss.OpenThread(context.Background(), "fixture:b"); err == nil {
				t.Fatal("unavailable required source silently omitted")
			}
		})
	}
}
