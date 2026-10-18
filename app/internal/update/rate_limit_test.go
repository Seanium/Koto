package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRateLimitHTTPHeaders(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		status  int
		headers map[string]string
		limited bool
		delay   time.Duration
	}{
		{"primary", 403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": fmt.Sprint(now.Add(2 * time.Minute).Unix())}, true, 2 * time.Minute},
		{"secondary-seconds", 403, map[string]string{"Retry-After": "90"}, true, 90 * time.Second},
		{"secondary-date", 403, map[string]string{"Retry-After": now.Add(3 * time.Minute).Format(http.TimeFormat)}, true, 3 * time.Minute},
		{"both-use-later", 403, map[string]string{"Retry-After": "90", "X-RateLimit-Remaining": "0", "X-RateLimit-Reset": fmt.Sprint(now.Add(2 * time.Minute).Unix())}, true, 2 * time.Minute},
		{"remaining-without-time", 403, map[string]string{"X-RateLimit-Remaining": "0"}, true, time.Minute},
		{"expired-reset", 403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1"}, true, time.Minute},
		{"malformed-reset", 403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "tomorrow"}, true, time.Minute},
		{"plain-forbidden", 403, nil, false, 0},
		{"unspent-limit", 403, map[string]string{"X-RateLimit-Remaining": "30", "X-RateLimit-Reset": fmt.Sprint(now.Add(time.Hour).Unix())}, false, 0},
		{"malformed-retry", 403, map[string]string{"Retry-After": "no"}, false, 0},
		{"negative-retry", 403, map[string]string{"Retry-After": "-9"}, false, 0},
		{"overflow-retry", 403, map[string]string{"Retry-After": "9223372036854775807"}, false, 0},
		{"too-many", 429, nil, true, time.Minute},
		{"zero-retry", 429, map[string]string{"Retry-After": "0"}, true, time.Minute},
		{"unavailable-retry", 503, map[string]string{"Retry-After": "90"}, false, 90 * time.Second},
		{"unavailable-no-retry", 503, nil, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("unexpected credentials")
				}
				for k, v := range tc.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			c := &Client{api: server.URL, http: server.Client(), now: func() time.Time { return now }}
			result, err := c.Check(context.Background(), "0.1.0", CLI, "windows", "arm64", "")
			var request *RequestError
			if result != nil || !errors.As(err, &request) || request.RateLimited != tc.limited || request.Status != tc.status {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if !strings.Contains(err.Error(), ReleasesPage) {
				t.Fatalf("manual download absent: %v", err)
			}
			if tc.delay > 0 && !request.RetryAt.Equal(now.Add(tc.delay)) {
				t.Fatalf("retry %v want %v", request.RetryAt, now.Add(tc.delay))
			}
			if tc.delay == 0 && !request.RetryAt.IsZero() {
				t.Fatal("failure without a retry hint imposed a cooldown")
			}
			_, _ = c.Check(context.Background(), "0.1.0", CLI, "windows", "arm64", "")
			want := int32(2)
			if tc.delay > 0 {
				want = 1
			}
			if calls.Load() != want {
				t.Fatalf("requests=%d want%d", calls.Load(), want)
			}
		})
	}
}

func TestRateLimitCooldownExpiresAndCancellationWins(t *testing.T) {
	initial := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	var clock atomic.Int64
	clock.Store(initial.UnixNano())
	var requests atomic.Int32
	released, _ := testRelease("v0.2.0", "koto-cli-linux-arm64", []byte("new"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(429)
			return
		}
		_ = json.NewEncoder(w).Encode([]release{released})
	}))
	defer server.Close()
	client := &Client{api: server.URL, http: server.Client(), now: func() time.Time { return time.Unix(0, clock.Load()) }}
	check := func() (*Result, error) { return client.Check(context.Background(), "0.1.0", CLI, "linux", "arm64", "") }
	if _, err := check(); err == nil {
		t.Fatal("initial refusal accepted")
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := check(); err == nil {
				t.Error("cooldown missing")
			}
		}()
	}
	wg.Wait()
	if requests.Load() != 1 {
		t.Fatalf("cooldown made %d requests", requests.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Check(ctx, "0.1.0", CLI, "linux", "arm64", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	clock.Add(int64(2 * time.Second))
	result, err := check()
	if err != nil || result == nil || !result.Available || requests.Load() != 2 {
		t.Fatalf("recovery: %+v %v requests=%d", result, err, requests.Load())
	}
}
