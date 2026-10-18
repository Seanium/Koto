package main

import (
	"context"
	"errors"
	"koto/internal/update"
	"sync/atomic"
	"testing"
	"time"
)

type updateClientStub struct {
	check func() (*update.Result, error)
}

func (c updateClientStub) Check(context.Context, string, update.Product, string, string, string) (*update.Result, error) {
	return c.check()
}
func (c updateClientStub) Prepare(context.Context, *update.Result, update.Location, func(int64, int64)) (*update.Prepared, error) {
	panic("unexpected installation")
}

func TestUpdateCheckRateLimitPreservesErrorAndRespectsRetry(t *testing.T) {
	var calls atomic.Int32
	states := make(chan UpdateState, 4)
	u := newAppUpdater(func(s UpdateState) { states <- s }, func() { t.Error("unexpected restart") })
	defer u.cancel()
	u.client = updateClientStub{check: func() (*update.Result, error) {
		if calls.Add(1) == 1 {
			return nil, &update.RequestError{Status: 403, RateLimited: true, RetryAt: time.Now().Add(time.Minute)}
		}
		return &update.Result{Latest: "0.1.1", Available: true, URL: update.ReleasesPage}, nil
	}}
	u.state.Current = "0.1.0"
	if state := u.check(true); state.Phase != "checking" {
		t.Fatalf("%+v", state)
	}
	var failed UpdateState
	select {
	case failed = <-states:
	case <-time.After(time.Second):
		t.Fatal("no check result")
	}
	if failed.Phase != "error" || failed.ErrorCode != "RATE_LIMITED" || failed.ErrorStage != "check" || failed.URL != update.ReleasesPage || failed.Latest != "" {
		t.Fatalf("%+v", failed)
	}
	for i := 0; i < 10; i++ {
		if got := u.check(true); got.Revision != failed.Revision || got.Phase != "error" {
			t.Fatalf("cooldown changed state: %+v", got)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("forced checking bypassed cooldown")
	}
	u.mu.Lock()
	u.state.RetryAt = time.Now().Add(-time.Second).UnixMilli()
	u.mu.Unlock()
	u.check(false)
	select {
	case state := <-states:
		if state.Phase != "available" || state.RetryAt != 0 || state.ErrorCode != "" || state.Error != "" {
			t.Fatalf("%+v", state)
		}
	case <-time.After(time.Second):
		t.Fatal("retry did not complete")
	}
	if calls.Load() != 2 {
		t.Fatal("retry not performed")
	}
}
func TestUpdateFailureStages(t *testing.T) {
	var s UpdateState
	updateFailed(&s, errors.New("offline"), "check")
	if s.Phase != "error" || s.ErrorStage != "check" || s.ErrorCode != "" {
		t.Fatalf("%+v", s)
	}
	updateFailed(&s, &update.RequestError{Status: 403}, "install")
	if s.Phase != "error" || s.ErrorStage != "install" || s.ErrorCode != "HTTP_FORBIDDEN" || s.RetryAt != 0 {
		t.Fatalf("%+v", s)
	}
	updateFailed(&s, update.ErrStaleFeed, "check")
	if s.Phase != "error" || s.ErrorStage != "check" || s.ErrorCode != "STALE_FEED" {
		t.Fatalf("stale information reported as current: %+v", s)
	}
}
