package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestWorkerFeedFreshnessNeverConfirmsStaleCurrentVersion(t *testing.T) {
	if NewClient().api != "https://updates.kotoai.site/api/releases" {
		t.Fatal("default update feed is not the metadata service")
	}
	r, _ := testRelease("v0.1.1", "koto-cli-linux-amd64", []byte("executable"))
	r.Stale = true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
			t.Error("client sent credentials")
		}
		_ = json.NewEncoder(w).Encode([]release{r})
	}))
	defer server.Close()
	c := &Client{api: server.URL, http: server.Client()}
	for _, current := range []string{"0.1.1", "0.1.2"} {
		got, err := c.Check(context.Background(), current, CLI, "linux", "amd64", "auto")
		if got != nil || !errors.Is(err, ErrStaleFeed) {
			t.Fatalf("stale feed claimed current: %s %+v %v", current, got, err)
		}
	}
	got, err := c.Check(context.Background(), "0.1.0", CLI, "linux", "amd64", "auto")
	if err != nil || !got.Available || got.Latest != "0.1.1" {
		t.Fatalf("known newer release unavailable: %+v %v", got, err)
	}
}

func TestWorkerFallbackUnknownSizeRequiresVerifiedDigest(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*release)
		valid  bool
	}{
		{"verified-unknown-size", func(r *release) { r.Assets[0].Size = 0 }, true},
		{"missing-digest", func(r *release) { r.Assets[0].Size = 0; r.Assets[0].Digest = "" }, false},
		{"invalid-digest", func(r *release) { r.Assets[0].Size = 0; r.Assets[0].Digest = "sha256:" + strings.Repeat("z", 64) }, false},
		{"wrong-algorithm", func(r *release) { r.Assets[0].Size = 0; r.Assets[0].Digest = "sha512:" + strings.Repeat("a", 64) }, false},
		{"negative-size", func(r *release) { r.Assets[0].Size = -1 }, false},
		{"oversized", func(r *release) { r.Assets[0].Size = maxAssetSize + 1 }, false},
		{"unknown-checksums-size", func(r *release) { r.Assets[1].Size = 0 }, false},
		{"missing-checksums-digest", func(r *release) { r.Assets[1].Digest = "" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, bodies := testRelease("v0.1.1", "koto-cli-linux-amd64", []byte("verified executable"))
			r.Stale = true // A known newer release is still usable after verification.
			tc.mutate(&r)
			c := testClient([]release{r}, bodies)
			result, err := c.Check(context.Background(), "0.1.0", CLI, "linux", "amd64", "")
			if !tc.valid {
				if err == nil {
					t.Fatal("unverifiable metadata accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var progress int64
			path, err := c.Download(context.Background(), result, t.TempDir(), func(n, total int64) {
				progress = n
				if total != 0 {
					t.Errorf("invented total %d", total)
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != "verified executable" || progress != int64(len(got)) {
				t.Fatalf("wrong payload/progress: %q %d %v", got, progress, err)
			}
		})
	}
}

func TestWorkerUnknownSizeDownloadStillRejectsCorruptionAndBounds(t *testing.T) {
	for _, kind := range []string{"corrupt", "truncated", "digest-disagreement", "oversized-content-length", "read-error"} {
		t.Run(kind, func(t *testing.T) {
			r, bodies := testRelease("v0.1.1", "koto-cli-linux-amd64", []byte("verified executable"))
			r.Assets[0].Size = 0
			c := testClient([]release{r}, bodies)
			result, err := c.Check(context.Background(), "0.1.0", CLI, "linux", "amd64", "")
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "corrupt":
				bodies[r.Assets[0].URL] = []byte("corrupt")
			case "truncated":
				bodies[r.Assets[0].URL] = []byte("verified")
			case "digest-disagreement":
				result.Asset.Digest = "sha256:" + strings.Repeat("a", 64)
			case "oversized-content-length", "read-error":
				previous := c.http.Transport
				c.http.Transport = roundTrip(func(req *http.Request) (*http.Response, error) {
					resp, err := previous.RoundTrip(req)
					if err == nil && req.URL.String() == r.Assets[0].URL {
						if kind == "oversized-content-length" {
							resp.ContentLength = maxAssetSize + 1
						} else {
							resp.Body.Close()
							resp.Body = io.NopCloser(failedDownloadReader{})
						}
					}
					return resp, err
				})
			}
			dir := t.TempDir()
			if _, err = c.Download(context.Background(), result, dir, nil); err == nil {
				t.Fatal("unverified payload accepted")
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Fatalf("left partial payload: %v", entries)
			}
		})
	}
}

type failedDownloadReader struct{}

func (failedDownloadReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestWorkerExactTagRecoveryMayUseVerifiedStaleMetadata(t *testing.T) {
	r, bodies := testRelease("v0.1.0", "koto-linux-arm64.deb", []byte("old package"))
	r.Stale = true
	r.Assets[0].Size = 0
	data, _ := json.Marshal(r)
	bodies[releasesAPI+"/tags/"+r.Tag] = data
	c := testClient(nil, bodies)
	result, err := c.releaseAsset(context.Background(), r.Tag, "0.1.1", App, r.Assets[0].Name)
	if err != nil || result.Tag != r.Tag {
		t.Fatalf("exact recovery changed: %+v %v", result, err)
	}
	path, err := c.Download(context.Background(), result, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, []byte("old package")) {
		t.Fatal("wrong recovery bytes")
	}
}
