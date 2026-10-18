package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
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
		{"no-published-release-json", "NO_PUBLISHED_RELEASE", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			http.DefaultTransport = updateRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "updates.kotoai.site" || r.Header.Get("Authorization") != "" {
					t.Fatalf("unexpected request %s", r.URL.Host)
				}
				if tc.code == "NO_PUBLISHED_RELEASE" {
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`[{"tag_name":"v0.1.0-beta.2","prerelease":true}]`))}, nil
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
			if tc.code == "NO_PUBLISHED_RELEASE" {
				args = append(args, "--channel", "stable")
			}
			if tc.json {
				args = append(args, "--json")
			}
			var out, errOut bytes.Buffer
			if code := runUpdate(args, &out, &errOut); code != 1 || calls != 1 {
				t.Fatalf("exit=%d calls=%d out=%s err=%s", code, calls, out.String(), errOut.String())
			}
			if tc.json {
				var result struct {
					OK                        bool `json:"ok"`
					Code, Stage, URL, Channel string
					RetryAt                   int64 `json:"retryAt"`
				}
				if err := json.Unmarshal(out.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.OK || result.Code != tc.code || result.Stage != "check" || result.URL != update.ReleasesPage || errOut.Len() != 0 {
					t.Fatalf("result=%+v stderr=%s", result, errOut.String())
				}
				if tc.code == "NO_PUBLISHED_RELEASE" && result.Channel != "stable" {
					t.Fatalf("wrong channel: %+v", result)
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

func TestUpdateDefaultBetaJSONAndAheadText(t *testing.T) {
	oldVersion, oldTransport := version, http.DefaultTransport
	t.Cleanup(func() { version, http.DefaultTransport = oldVersion, oldTransport })
	version = "0.1.0"
	calls := 0
	http.DefaultTransport = updateRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://updates.kotoai.site/api/releases" {
			t.Fatalf("unexpected download or request: %s", r.URL)
		}
		name, err := update.AssetName(update.CLI, runtime.GOOS, runtime.GOARCH)
		if err != nil {
			t.Fatal(err)
		}
		base := update.Repository + "/releases/download/v0.1.0-beta.2/"
		body, _ := json.Marshal([]map[string]any{{"tag_name": "v0.1.0-beta.2", "assets": []map[string]any{{"name": name, "browser_download_url": base + name, "size": 0, "digest": "sha256:" + strings.Repeat("a", 64)}, {"name": "SHA256SUMS", "browser_download_url": base + "SHA256SUMS", "size": 100, "digest": "sha256:" + strings.Repeat("b", 64)}}}})
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
	for _, asJSON := range []bool{false, true} {
		args := []string{}
		if asJSON {
			args = append(args, "--json")
		}
		var out, errOut bytes.Buffer
		if code := runUpdate(args, &out, &errOut); code != 0 || errOut.Len() != 0 {
			t.Fatalf("exit %d: %s", code, errOut.String())
		}
		if asJSON {
			var result struct {
				OK, Available, Updated, Prerelease, Ahead bool
				Latest                                    string
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if !result.OK || result.Available || result.Updated || !result.Prerelease || !result.Ahead || result.Latest != "0.1.0-beta.2" {
				t.Fatalf("%+v", result)
			}
		} else if !strings.Contains(out.String(), "Latest published release: 0.1.0-beta.2 (beta)") || !strings.Contains(out.String(), "No release is newer than installed koto 0.1.0.") {
			t.Fatal(out.String())
		}
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}
