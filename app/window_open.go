package main

import "sync"

type windowOpenRequest struct {
	nav      string
	settings *string
}

// Native menu events can precede the frontend subscriptions. Coalesce requests
// until those subscriptions exist without losing a target or a readiness wakeup.
type windowOpenQueue struct {
	mu      sync.Mutex
	ready   bool
	pending *windowOpenRequest
}

func (q *windowOpenQueue) request(request windowOpenRequest) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.ready {
		return true
	}
	// A plain tray click should not erase an earlier request to open settings.
	if q.pending == nil || request.nav != "" || request.settings != nil {
		q.pending = &request
	}
	return false
}

func (q *windowOpenQueue) activate() *windowOpenRequest {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ready = true
	pending := q.pending
	q.pending = nil
	return pending
}
