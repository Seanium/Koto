package koto

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"koto/internal/core"
	"koto/internal/store"
)

func TestSessionCacheLifecycle(t *testing.T) {
	s := newSandbox(t)
	var selectors []string
	for i := range 8 {
		id := fmt.Sprintf("ba112233-4455-4677-8899-%012d", i)
		records := ccLinear(id, 20, fmt.Sprintf("r%d", i))
		for n := range records {
			if n%2 == 0 {
				continue
			}
			o := records[n].(core.O)
			records[n] = setKey(o, "message", core.O{{K: "role", V: "assistant"}, {K: "content", V: []any{ccText(strings.Repeat(fmt.Sprintf("memory-%d-%d ", i, n), 4096))}}})
		}
		writeLines(t, filepath.Join(ccDir(s.Claude), id+".jsonl"), records)
		selectors = append(selectors, "claude-code:"+id)
	}
	a := s.app(enable("claude-code", "pi"))
	api := newAPI(a)
	api.Reindex(ctx)
	first, err := a.OpenThread(ctx, selectors[0])
	must(t, err)
	head := first.Hub.ThreadHead(first.Thread)
	for _, sel := range selectors {
		_, err := a.OpenThread(ctx, sel)
		must(t, err)
	}
	if len(a.Sessions.Views()) > 3 {
		t.Fatal("unbounded parsed session cache")
	}
	// Eviction removes ownership from the cache without destroying an active reader's data.
	if len(first.Hub.Path(head)) != 40 {
		t.Fatal("eviction invalidated an active request")
	}
	first = nil
	var wg sync.WaitGroup
	views := make([]*store.OpenThread, 8)
	for i := range views {
		wg.Add(1)
		go func(i int) { defer wg.Done(); views[i], _ = a.OpenThread(ctx, selectors[0]) }(i)
	}
	wg.Wait()
	for _, view := range views {
		if view == nil || view != views[0] {
			t.Fatal("concurrent requests parsed duplicate working copies")
		}
	}
	views = nil
	var samples []uint64
	for batch := range 4 {
		for _, sel := range selectors {
			d, err := api.Thread(ctx, sel, 30, "")
			must(t, err)
			req := CarryRequest{From: d.Events[3].ID, To: "pi", DryRun: true, RequestID: "memory-cycle", Cwd: s.Root}
			_, err = api.Carry(ctx, req)
			must(t, err)
			api.ForgetCopy(req.RequestID)
		}
		_, err := api.Analyze(ctx, selectors[:2])
		must(t, err)
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		samples = append(samples, m.HeapAlloc)
		t.Logf("batch %d: retained Go heap %.2f MiB, goroutines %d", batch+1, float64(m.HeapAlloc)/(1<<20), runtime.NumGoroutine())
	}
	if samples[3] > samples[1]+8<<20 {
		t.Fatal("retained heap grows across repeated identical work")
	}
	a.Sessions.Close()
	if len(a.Sessions.Views()) != 0 || len(a.Sessions.Recent()) != 0 {
		t.Fatal("closed session cache retains references")
	}
}
