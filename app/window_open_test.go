package main

import (
	"sync"
	"testing"
)

func TestWindowOpenRetainsColdStartTarget(t *testing.T) {
	var q windowOpenQueue
	tab := "general"
	if q.request(windowOpenRequest{settings: &tab}) || q.request(windowOpenRequest{}) {
		t.Fatal("request delivered before listeners are ready")
	}
	pending := q.activate()
	if pending == nil || pending.settings == nil || *pending.settings != tab {
		t.Fatalf("settings target lost: %+v", pending)
	}
	if q.activate() != nil || !q.request(windowOpenRequest{nav: "session"}) {
		t.Fatal("readiness did not drain exactly once")
	}
}

func TestWindowOpenLatestNavigation(t *testing.T) {
	var q windowOpenQueue
	tab := "general"
	q.request(windowOpenRequest{settings: &tab})
	q.request(windowOpenRequest{nav: "session"})
	got := q.activate()
	if got == nil || got.nav != "session" || got.settings != nil {
		t.Fatalf("latest explicit destination not retained: %+v", got)
	}
}

func TestWindowOpenReadinessRace(t *testing.T) {
	for range 1000 {
		var q windowOpenQueue
		var wg sync.WaitGroup
		var immediate bool
		var pending *windowOpenRequest
		wg.Add(2)
		go func() { defer wg.Done(); immediate = q.request(windowOpenRequest{nav: "session"}) }()
		go func() { defer wg.Done(); pending = q.activate() }()
		wg.Wait()
		if immediate == (pending != nil) {
			t.Fatalf("request lost or delivered twice: immediate=%v pending=%+v", immediate, pending)
		}
	}
}
