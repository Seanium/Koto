package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVersionOrdering(t *testing.T) {
	for _, c := range []struct {
		next, current string
		newer         bool
	}{
		{"0.1.0-beta.10", "0.1.0-beta.9", true},
		{"0.1.0", "0.1.0-beta.10", true},
		{"0.1.0-beta.1", "0.1.0", false},
		{"v0.2.0+build.9", "0.1.9", true},
		{"0.1.0+other", "0.1.0+first", false},
		{"0.1.0-beta.999999999999999999999", "0.1.0-beta.10", true},
		{"0.1.0-beta.01", "0.1.0-beta.1", false},
		{"0.1.00", "0.1.0", false},
		{"0.1.0", "dev", false},
		{"0.1.0-", "0.0.9", false},
		{"0.1.0-beta", "0.1.0-9", true},
		{"0.1.0-beta.1", "0.1.0-beta", true},
	} {
		if got := Newer(c.next, c.current); got != c.newer {
			t.Errorf("Newer(%q,%q)=%v", c.next, c.current, got)
		}
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testClient(releases []release, bodies map[string][]byte) *Client {
	feed, _ := json.Marshal(releases)
	return &Client{api: releasesAPI, http: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		b := feed
		status := 200
		if r.URL.String() != releasesAPI {
			var ok bool
			b, ok = bodies[r.URL.String()]
			if !ok {
				status = 404
			}
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(b)), Header: make(http.Header)}, nil
	})}}
}

func testRelease(tag, name string, payload []byte) (release, map[string][]byte) {
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])
	checksums := []byte(hash + "  " + name + "\n")
	checksumsHash := sha256.Sum256(checksums)
	base := Repository + "/releases/download/" + tag + "/"
	return release{Tag: tag, Assets: []Asset{
		{Name: name, URL: base + name, Size: int64(len(payload)), Digest: "sha256:" + hash},
		{Name: "SHA256SUMS", URL: base + "SHA256SUMS", Size: int64(len(checksums)), Digest: "sha256:" + hex.EncodeToString(checksumsHash[:])},
	}}, map[string][]byte{base + name: payload, base + "SHA256SUMS": checksums}
}

func TestCheckChoosesChannelAndSemanticVersion(t *testing.T) {
	a, _ := testRelease("v0.1.0-beta.9", "koto-cli-linux-amd64", []byte("nine"))
	b, _ := testRelease("v0.1.0-beta.10", "koto-cli-linux-amd64", []byte("ten"))
	b.Prerelease = true
	c, _ := testRelease("v0.0.8", "koto-cli-linux-amd64", []byte("stable"))
	d, _ := testRelease("v9.0.0", "koto-cli-linux-amd64", []byte("draft"))
	d.Draft = true
	client := testClient([]release{a, c, d, b}, nil)
	for _, test := range []struct{ current, channel, want string }{{"0.1.0-beta.1", "auto", "0.1.0-beta.10"}, {"0.0.7", "auto", "0.0.8"}, {"0.0.7", "beta", "0.1.0-beta.10"}} {
		r, err := client.Check(context.Background(), test.current, CLI, "linux", "amd64", test.channel)
		if err != nil || r.Latest != test.want || !r.Available {
			t.Fatalf("%+v: %+v %v", test, r, err)
		}
	}
	if _, err := client.Check(context.Background(), "dev", CLI, "linux", "amd64", ""); err == nil {
		t.Fatal("dev build must not update")
	}
	if _, err := client.Check(context.Background(), "0.0.7", CLI, "linux", "amd64", "nightly"); err == nil {
		t.Fatal("unknown channel")
	}
}

func TestCheckRejectsMissingOrUntrustedAsset(t *testing.T) {
	for _, mutate := range []func(*release){
		func(r *release) { r.Assets = r.Assets[:1] },
		func(r *release) { r.Assets[0].URL = "https://example.org/koto" },
		func(r *release) { r.Assets[0].URL += "?redirect=elsewhere" },
		func(r *release) { r.Assets = append(r.Assets, r.Assets[0]) },
	} {
		r, _ := testRelease("v0.1.0", "koto-cli-linux-amd64", []byte("payload"))
		mutate(&r)
		_, err := testClient([]release{r}, nil).Check(context.Background(), "0.0.1", CLI, "linux", "amd64", "")
		if err == nil {
			t.Fatal("accepted incomplete or untrusted release")
		}
	}
}

func TestDownloadIntegrityAndCleanup(t *testing.T) {
	for _, kind := range []string{"valid", "corrupt", "truncated", "oversized", "wrong-checksum", "cancelled", "missing"} {
		t.Run(kind, func(t *testing.T) {
			r, bodies := testRelease("v0.1.0", "koto-cli-linux-amd64", []byte("verified native executable"))
			client := testClient([]release{r}, bodies)
			result, err := client.Check(context.Background(), "0.0.1", CLI, "linux", "amd64", "")
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "corrupt":
				bodies[r.Assets[0].URL][0] = 'X'
			case "truncated":
				bodies[r.Assets[0].URL] = []byte("short")
			case "oversized":
				bodies[r.Assets[0].URL] = bytes.Repeat([]byte("X"), 128)
			case "wrong-checksum":
				bodies[r.Assets[1].URL][0] = 'X'
			case "missing":
				delete(bodies, r.Assets[0].URL)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "cancelled" {
				cancel()
			}
			dir := t.TempDir()
			path, err := client.Download(ctx, result, dir, nil)
			if kind == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				got, _ := os.ReadFile(path)
				if string(got) != "verified native executable" {
					t.Fatal("wrong bytes")
				}
			} else {
				if err == nil {
					t.Fatal("bad download accepted")
				}
				entries, _ := os.ReadDir(dir)
				if len(entries) != 0 {
					t.Fatalf("partial download remains: %v", entries)
				}
			}
		})
	}
}

func TestChecksumRequiresSingleExactFilename(t *testing.T) {
	h := strings.Repeat("a", 64)
	for _, input := range []string{h + "  other", h + "  ../koto", h + "  koto\n" + h + "  koto", "invalid  koto"} {
		if _, err := checksum([]byte(input), "koto"); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}

func TestReplacementRollbackAndConcurrentChange(t *testing.T) {
	for _, kind := range []string{"success", "missing-payload", "changed", "rollback"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "koto")
			os.WriteFile(target, []byte("old"), 0755)
			stage := filepath.Join(dir, "stage")
			os.Mkdir(stage, 0700)
			payload := filepath.Join(stage, "new")
			os.WriteFile(payload, []byte("new"), 0755)
			before, _ := fileHash(target)
			p := &Prepared{Location: Location{Target: target, Executable: target, Product: CLI}, dir: stage, payload: payload, before: before}
			defer p.Close()
			if kind == "missing-payload" {
				os.Remove(payload)
			}
			if kind == "changed" {
				os.WriteFile(target, []byte("another update"), 0755)
			}
			err := p.Install()
			want := "old"
			if kind == "success" || kind == "rollback" {
				if err != nil {
					t.Fatal(err)
				}
				if kind == "rollback" {
					if err := p.Rollback(); err != nil {
						t.Fatal(err)
					}
				} else {
					p.Commit()
					want = "new"
				}
			} else if err == nil {
				t.Fatal("failure was ignored")
			}
			if kind == "changed" {
				want = "another update"
			}
			got, _ := os.ReadFile(target)
			if string(got) != want {
				t.Fatalf("installed %q, want %q", got, want)
			}
		})
	}
}

func TestUpdateLockIsCancellable(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".koto-update.lock")
	unlock, err := lock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := lock(ctx, path); err == nil {
		t.Fatal("concurrent update acquired the lock")
	}
}

func TestReleaseAssetUsesExactInstalledVersion(t *testing.T) {
	name := "koto-linux-arm64.deb"
	old, _ := testRelease("v0.1.0-beta.2", name, []byte("old package"))
	newer, _ := testRelease("v0.1.1", name, []byte("new package"))
	for _, kind := range []string{"valid", "different-tag", "draft", "missing-checksums"} {
		t.Run(kind, func(t *testing.T) {
			reply := old
			reply.Assets = append([]Asset{}, old.Assets...)
			switch kind {
			case "different-tag":
				reply = newer
			case "draft":
				reply.Draft = true
			case "missing-checksums":
				reply.Assets = reply.Assets[:1]
			}
			body, _ := json.Marshal(reply)
			api := strings.Split(releasesAPI, "?")[0] + "/tags/v0.1.0-beta.2"
			c := testClient([]release{newer}, map[string][]byte{api: body})
			r, err := c.releaseAsset(context.Background(), old.Tag, "0.1.0-beta.2", App, name)
			if kind != "valid" {
				if err == nil {
					t.Fatal("accepted wrong recovery release")
				}
				return
			}
			if err != nil || r.Tag != old.Tag || r.Asset.URL != old.Assets[0].URL || r.Available {
				t.Fatal(r, err)
			}
		})
	}
}

func TestBetaUpgradesToStableThenPatch(t *testing.T) {
	name := "koto-cli-linux-arm64"
	beta, _ := testRelease("v0.1.0-beta.2", name, []byte("beta"))
	stable, _ := testRelease("v0.1.0", name, []byte("stable"))
	future, _ := testRelease("v0.2.0-beta.1", name, []byte("future beta"))
	future.Prerelease = true
	patch, _ := testRelease("v0.1.1", name, []byte("patch"))
	for _, test := range []struct {
		current, want string
		releases      []release
	}{
		{"0.1.0-beta.2", "0.1.0", []release{beta, stable}},
		{"0.1.0", "0.1.1", []release{future, patch, stable}},
	} {
		r, err := testClient(test.releases, nil).Check(context.Background(), test.current, CLI, "linux", "arm64", "auto")
		if err != nil || r.Latest != test.want || !r.Available {
			t.Fatal(r, err)
		}
	}
}
