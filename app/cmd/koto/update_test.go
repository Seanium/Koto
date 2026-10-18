package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"koto/internal/update"
)

type updateRoundTrip func(*http.Request) (*http.Response, error)

func (f updateRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// HTTP header semantics are covered by update's real HTTP tests. This checks
// the command's exit status and public text/JSON contract without the network.
func TestUpdateCheckFailureOutput(t *testing.T) {
	oldVersion, oldTransport := version, http.DefaultTransport
	t.Cleanup(func() { version, http.DefaultTransport = oldVersion, oldTransport })
	version = "0.1.0"
	retry := time.Now().Add(time.Hour).Truncate(time.Second)
	for _, tc := range []struct {
		name, code  string
		json, limit bool
	}{
		{"rate-limit-text", "RATE_LIMITED", false, true},
		{"rate-limit-json", "RATE_LIMITED", true, true},
		{"forbidden-json", "HTTP_FORBIDDEN", true, false},
		{"stale-feed-json", "STALE_FEED", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			http.DefaultTransport = updateRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "updates.kotoai.site" || r.Header.Get("Authorization") != "" {
					t.Fatalf("unexpected request %s", r.URL.Host)
				}
				if tc.code == "STALE_FEED" {
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`[{"tag_name":"v0.1.0","stale":true}]`))}, nil
				}
				header := make(http.Header)
				if tc.limit {
					header.Set("X-RateLimit-Remaining", "0")
					header.Set("X-RateLimit-Reset", fmt.Sprint(retry.Unix()))
				}
				return &http.Response{StatusCode: 403, Header: header, Body: io.NopCloser(strings.NewReader("forbidden"))}, nil
			})
			args := []string{"--check"}
			if tc.json {
				args = append(args, "--json")
			}
			var out, errOut bytes.Buffer
			if code := runUpdate(args, &out, &errOut); code != 1 || calls != 1 {
				t.Fatalf("exit=%d calls=%d out=%s err=%s", code, calls, out.String(), errOut.String())
			}
			if tc.json {
				var result struct {
					OK               bool `json:"ok"`
					Code, Stage, URL string
					RetryAt          int64 `json:"retryAt"`
				}
				if err := json.Unmarshal(out.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.OK || result.Code != tc.code || result.Stage != "check" || result.URL != update.ReleasesPage || errOut.Len() != 0 {
					t.Fatalf("result=%+v stderr=%s", result, errOut.String())
				}
				if tc.limit && result.RetryAt != retry.UnixMilli() || !tc.limit && result.RetryAt != 0 {
					t.Fatalf("retry=%d", result.RetryAt)
				}
			} else if out.Len() != 0 || !strings.Contains(errOut.String(), "Unable to check for updates:") || !strings.Contains(errOut.String(), update.ReleasesPage) || !strings.Contains(errOut.String(), retry.Format(time.RFC3339)) {
				t.Fatalf("out=%s err=%s", out.String(), errOut.String())
			}
		})
	}
}
