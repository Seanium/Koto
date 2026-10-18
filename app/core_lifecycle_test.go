package main

import (
	"sync"
	"testing"
	"time"
)

func TestConcurrentCoreRestartsClosePreviousWatchers(t *testing.T) {
	t.Setenv("KOTO_APP_SANDBOX", t.TempDir())
	c := &Core{}
	var mu sync.Mutex
	var watches []chan struct{}
	c.OnStatus = func(s CoreStatus) {
		if s.State == "ready" {
			c.mu.Lock()
			d := c.done
			c.mu.Unlock()
			mu.Lock()
			watches = append(watches, d)
			mu.Unlock()
		}
	}
	c.Start()
	defer c.Stop()
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); c.Restart() }()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(watches) != 7 {
		t.Fatalf("ready count %d", len(watches))
	}
	for i, ch := range watches[:len(watches)-1] {
		select {
		case <-ch:
		default:
			t.Fatalf("watch %d still live", i)
		}
	}
	select {
	case <-watches[len(watches)-1]:
		t.Fatal("current watch closed")
	default:
	}
}
func TestCoreStopWaitsForPinnedServiceRequest(t *testing.T) {
	t.Setenv("KOTO_APP_SANDBOX", t.TempDir())
	c := &Core{}
	c.Start()
	api, release := c.acquire()
	if api == nil {
		t.Fatal("no api")
	}
	stopped := make(chan struct{})
	go func() { c.Stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("closed store while service request pinned")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop did not finish")
	}
	api, release = c.acquire()
	defer release()
	if api != nil {
		t.Fatal("accepted request after stop")
	}
}
