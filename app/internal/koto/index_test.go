package koto

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/store"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type gatedIndexSource struct {
	engine.Source
	enabled          atomic.Bool
	started, release chan struct{}
}

type failedIndexSource struct{ engine.Source }

func assertSameIndexedContent(t *testing.T, got, want []store.SessionRow) {
	t.Helper()
	eq(t, len(got), len(want))
	for i := range want {
		g, w := got[i], want[i]
		// A fresh native file can become stable during a slow/race rebuild.
		// That time-based verification flag is not indexed content. Keep every
		// position, raw-record hash, witness and identity field in the comparison.
		if g.Offset != nil {
			o := *g.Offset
			o.WitnessStable = false
			g.Offset = &o
		}
		if w.Offset != nil {
			o := *w.Offset
			o.WitnessStable = false
			w.Offset = &o
		}
		if core.ToJSON(g) != core.ToJSON(w) {
			t.Fatalf("indexed content changed at row %d (%s:%s)", i, w.Connector, w.Session)
		}
	}
}

func (f failedIndexSource) Discover(context.Context) ([]core.NativeRef, error) {
	return nil, errors.New("fixture source unavailable")
}

func TestRebuildFailureAndDecoderInvalidation(t *testing.T) {
	s := newSandbox(t)
	writeCcSession(t, s.Claude, "", true)
	a := s.app(enable("claude-code"))
	api := newAPI(a)
	eq(t, len(api.Reindex(ctx).Errors), 0)
	rows := a.Store.Sessions()
	before := core.ToJSON(rows)
	c := a.Registry.Get("claude-code")
	original := c.Source
	c.Source = failedIndexSource{original}
	_, err := api.RebuildIndex(ctx)
	ok(t, err != nil)
	eq(t, core.ToJSON(a.Store.Sessions()), before)
	c.Source = original
	_, err = api.RebuildIndex(ctx)
	must(t, err)
	// An unchanged native file must still be reparsed after a decoder change.
	r := a.Store.Sessions()[0]
	r.Turns = 999
	must(t, a.Store.PutSession(r))
	c.Version += "-changed-decoder"
	report := api.Reindex(ctx)
	eq(t, len(report.Errors), 0)
	after := a.Store.Sessions()
	ok(t, core.S(after[0].Meta["_nativeRevision"]) != core.S(rows[0].Meta["_nativeRevision"]), "decoder change invalidates detail revision")
	rows[0].Meta["_nativeRevision"] = after[0].Meta["_nativeRevision"]
	assertSameIndexedContent(t, after, rows)
}

func TestEmptyIndexKeepsReadableSourcesWhenAnotherFails(t *testing.T) {
	s := newSandbox(t)
	writeCcSession(t, s.Claude, "", true)
	a := s.app(enable("claude-code", "pi"))
	c := a.Registry.Get("pi")
	c.Source = failedIndexSource{c.Source}
	report := newAPI(a).Reindex(ctx)
	eq(t, len(report.Errors), 1)
	eq(t, report.Errors[0].Connector, c.ID)
	rows := a.ListThreads()
	eq(t, len(rows), 1)
	eq(t, rows[0].Bindings[0].Harness, "claude-code")
}

func TestCanceledRebuildWaitingForLocalEngineLock(t *testing.T) {
	s := newSandbox(t)
	a := s.app(enable("claude-code"))
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- a.Engine.Locked(ctx, func() error { close(entered); <-release; return nil }) }()
	<-entered
	canceled, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err := newAPI(a).RebuildIndex(canceled)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting canceled: %v", err)
	}
	close(release)
	must(t, <-done)
	_, err = newAPI(a).RebuildIndex(ctx)
	must(t, err)
}

func (g *gatedIndexSource) Discover(ctx context.Context) ([]core.NativeRef, error) {
	if g.enabled.Swap(false) {
		close(g.started)
		select {
		case <-g.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return g.Source.Discover(ctx)
}

func TestRebuildIndexIsAtomicAndSerializesCopy(t *testing.T) {
	for _, separate := range []bool{false, true} {
		t.Run(fmt.Sprintf("separate-store-%v", separate), func(t *testing.T) {
			s := newSandbox(t)
			writeCcSession(t, s.Claude, "", true)
			a := s.app(enable("claude-code", "pi"))
			api := newAPI(a)
			eq(t, len(api.Reindex(ctx).Errors), 0)
			other := api
			if separate {
				other = newAPI(s.app(enable("claude-code", "pi")))
			}
			_, err := other.Thread(ctx, "claude-code:"+CCSession, 0, "")
			must(t, err)
			c := a.Registry.Get("claude-code")
			g := &gatedIndexSource{Source: c.Source, started: make(chan struct{}), release: make(chan struct{})}
			g.enabled.Store(true)
			c.Source = g
			before := core.ToJSON(a.ListThreads())
			rebuilt := make(chan error, 1)
			go func() { _, e := api.RebuildIndex(ctx); rebuilt <- e }()
			<-g.started
			eq(t, core.ToJSON(a.ListThreads()), before, "visible list remains while rebuilding")
			copied := make(chan error, 1)
			go func() {
				_, e := other.Carry(ctx, CarryRequest{Selector: "claude-code:" + CCSession, To: "pi"})
				copied <- e
			}()
			select {
			case e := <-copied:
				t.Fatalf("copy interleaved with rebuild: %v", e)
			case <-time.After(80 * time.Millisecond):
			}
			close(g.release)
			must(t, <-rebuilt)
			must(t, <-copied)
			eq(t, len(piFilesIn(s.Pi)), 1)
			eq(t, len(api.Reindex(ctx).Errors), 0)
			eq(t, len(a.ListThreads()), 2)
		})
	}
}

func TestRebuildIndexCancellationAndRetry(t *testing.T) {
	s := newSandbox(t)
	writeCcSession(t, s.Claude, "", true)
	a := s.app(enable("claude-code"))
	api := newAPI(a)
	eq(t, len(api.Reindex(ctx).Errors), 0)
	beforeRows := a.Store.Sessions()
	before := core.ToJSON(beforeRows)
	c := a.Registry.Get("claude-code")
	g := &gatedIndexSource{Source: c.Source, started: make(chan struct{}), release: make(chan struct{})}
	g.enabled.Store(true)
	c.Source = g
	canceled, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, e := api.RebuildIndex(canceled); done <- e }()
	<-g.started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	eq(t, core.ToJSON(a.Store.Sessions()), before)
	report, err := api.RebuildIndex(ctx)
	must(t, err)
	eq(t, len(report.Errors), 0)
	assertSameIndexedContent(t, a.Store.Sessions(), beforeRows)
	// Repeated rebuilds do not duplicate rows or offsets; the canceled lease was released.
	report, err = api.RebuildIndex(ctx)
	must(t, err)
	eq(t, report.Sessions, 1)
	assertSameIndexedContent(t, a.Store.Sessions(), beforeRows)
}

func TestThinIndexRebuildPreservesContentAndUserData(t *testing.T) {
	s := newSandbox(t)
	files := map[string]string{}
	var nativeBytes int64
	for i := 0; i < 24; i++ {
		id := fmt.Sprintf("11111111-2222-4333-8444-%012d", i)
		file := writeCcSession(t, s.Claude, id, false)
		appendLines(t, file, []any{map[string]any{"type": "summary", "summary": strings.Repeat("summary-body-", 16000)}, map[string]any{"type": "custom-title", "customTitle": strings.Repeat("long title", 2000)}, map[string]any{"type": "assistant", "uuid": fmt.Sprintf("big-%d", i), "parentUuid": "a5", "sessionId": id, "message": map[string]any{"role": "assistant", "content": strings.Repeat("full-native-content ", 8000)}}})
		b, e := os.ReadFile(file)
		must(t, e)
		nativeBytes += int64(len(b))
		files[file] = core.SHA256Hex(string(b))
	}
	a := s.app(enable("claude-code", "pi"))
	api := newAPI(a)
	eq(t, len(api.Reindex(ctx).Errors), 0)
	list := a.ListThreads()
	eq(t, len(list), 24)
	collection, err := api.SaveAnalysis(store.AnalysisCollection{Name: "keep comparison", Sessions: []string{list[0].ID, list[1].ID}})
	must(t, err)
	a.Store.SetSetting("prefs", map[string]any{"language": "ja", "theme": "dark"})
	trash := store.TrashEntry{TrashID: "retained-restore-record", Thread: "@missing:gone", Items: []store.TrashItem{}, TrashedAt: 1}
	must(t, a.Store.AddTrash(trash))
	beforeRows := a.Store.Sessions()
	beforeList := core.ToJSON(list)
	before, err := api.Thread(ctx, list[0].ID, 0, "")
	must(t, err)
	emitted := []Event{}
	api.Emit = func(e Event) { emitted = append(emitted, e) }
	start := time.Now()
	report, err := api.RebuildIndex(ctx)
	elapsed := time.Since(start)
	must(t, err)
	eq(t, report.Sessions, 24)
	assertSameIndexedContent(t, a.Store.Sessions(), beforeRows)
	eq(t, core.ToJSON(a.ListThreads()), beforeList)
	after, err := api.Thread(ctx, list[0].ID, 0, "")
	must(t, err)
	jsonEq(t, after, before)
	eq(t, len(emitted), 1)
	eq(t, emitted[0]["type"], any("cycle"))
	var prefs map[string]any
	ok(t, a.Store.Setting("prefs", &prefs))
	eq(t, prefs["language"], any("ja"))
	collections, err := api.Analyses()
	must(t, err)
	eq(t, collections[0].ID, collection.ID)
	eq(t, len(a.Store.Trash(false)), 1)
	// The returned view is still valid while cache ownership moves to fresh views.
	ok(t, len(before.Events) > 0)
	for file, hash := range files {
		b, e := os.ReadFile(file)
		must(t, e)
		eq(t, core.SHA256Hex(string(b)), hash)
	}
	db, err := sql.Open("sqlite", a.Store.File)
	must(t, err)
	defer db.Close()
	var pages, pageSize, maxTitle, maxPrompt, maxOffset, maxMeta int64
	must(t, db.QueryRow("PRAGMA page_count").Scan(&pages))
	must(t, db.QueryRow("PRAGMA page_size").Scan(&pageSize))
	must(t, db.QueryRow("SELECT max(length(title)),max(length(first_prompt)),max(length(offset)),max(length(meta)) FROM sessions").Scan(&maxTitle, &maxPrompt, &maxOffset, &maxMeta))
	ok(t, maxTitle <= 1024 && maxPrompt <= 200 && maxMeta < 16384)
	for _, r := range a.Store.Sessions() {
		ok(t, r.Meta["summary"] == nil)
	}
	ok(t, pages*pageSize < nativeBytes/4)
	t.Logf("native bytes=%d index bytes=%d rows=%d max(title,prompt,offset,meta)=%d,%d,%d,%d rebuild=%s source SHA unchanged", nativeBytes, pages*pageSize, len(list), maxTitle, maxPrompt, maxOffset, maxMeta, elapsed)
}

func TestStableProfileReferencesDoNotMoveAfterRootChange(t *testing.T) {
	s := newSandbox(t)
	writeCcSession(t, s.Claude, "", false)
	writeCcSession(t, s.Claude, "22222222-2222-4333-8444-555555555555", false)
	a := s.app(enable("claude-code"))
	api := newAPI(a)
	eq(t, len(api.Reindex(ctx).Errors), 0)
	list := a.ListThreads()
	saved, err := api.SaveAnalysis(store.AnalysisCollection{Name: "root A", Sessions: []string{list[0].ID, list[1].ID}})
	must(t, err)
	rootB := filepath.Join(s.Root, "another-root")
	writeCcSession(t, rootB, "", false)
	writeCcSession(t, rootB, "22222222-2222-4333-8444-555555555555", false)
	b := s.app(enable("claude-code"), func(c *Config) { c.ClaudeRoot = rootB })
	other := newAPI(b)
	eq(t, len(b.ListThreads()), 0, "rows from a different root are invisible before scan")
	eq(t, len(other.Reindex(ctx).Errors), 0)
	eq(t, len(b.ListThreads()), 2)
	_, err = other.Analyze(ctx, saved.Sessions)
	ok(t, err != nil, "old references must not select same IDs in root B")
	saved.Name = "renamed missing references"
	_, err = other.SaveAnalysis(saved)
	must(t, err)
	_, err = other.SaveAnalysis(store.AnalysisCollection{Name: "ambiguous future", Sessions: []string{"claude-code:missing-one", "claude-code:missing-two"}})
	ok(t, err != nil)
	// Another process rebuilding the DB also invalidates this instance's cached groups.
	_, err = a.OpenThread(ctx, saved.Sessions[0])
	ok(t, err != nil)
	eq(t, len(api.Reindex(ctx).Errors), 0)
	_, err = api.Analyze(ctx, saved.Sessions)
	must(t, err)
}

type gatedIndexTail struct {
	engine.Source
	once             atomic.Bool
	started, release chan struct{}
}

func (g *gatedIndexTail) Tail(ctx context.Context, r core.NativeRef, o *core.SourceOffset, y func(core.SourceBatch) error) error {
	if engine.IsIndexOnly(ctx) && g.once.Swap(false) {
		close(g.started)
		select {
		case <-g.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return g.Source.Tail(ctx, r, o, y)
}
func TestBackgroundSummaryDoesNotBlockPreviewOrCopiedResult(t *testing.T) {
	s := newSandbox(t)
	writeCcSession(t, s.Claude, "", true)
	a := s.app(enable("claude-code", "pi"))
	api := newAPI(a)
	eq(t, len(api.Reindex(ctx).Errors), 0)
	c := a.Registry.Get("claude-code")
	g := &gatedIndexTail{Source: c.Source, started: make(chan struct{}), release: make(chan struct{})}
	g.once.Store(true)
	c.Source = g
	c.Version += "-reparse"
	done := make(chan engine.ScanReport, 1)
	go func() { done <- api.Reindex(ctx) }()
	<-g.started
	foreground, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	preview, err := api.Carry(foreground, CarryRequest{Selector: "claude-code:" + CCSession, To: "pi", DryRun: true})
	must(t, err)
	ok(t, !preview.Applied)
	copied, err := api.Carry(foreground, CarryRequest{Selector: "claude-code:" + CCSession, To: "pi"})
	must(t, err)
	ok(t, copied.Applied)
	_, err = a.ResolveSession("pi:" + copied.Plan.Target.Session)
	must(t, err)
	close(g.release)
	eq(t, len((<-done).Errors), 0)
	_, err = a.ResolveSession("pi:" + copied.Plan.Target.Session)
	must(t, err) // fresh prune cannot drop concurrent result
}
