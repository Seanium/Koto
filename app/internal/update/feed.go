package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const Repository = "https://github.com/Seanium/koto"
const releasesAPI = "https://api.github.com/repos/Seanium/koto/releases?per_page=100"

type Product string

const (
	App Product = "app"
	CLI Product = "cli"
)

type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type release struct {
	Tag        string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

type Result struct {
	Tag       string  `json:"tag"`
	Current   string  `json:"current"`
	Latest    string  `json:"latest"`
	Available bool    `json:"available"`
	URL       string  `json:"url"`
	Product   Product `json:"product"`
	Asset     Asset   `json:"asset"`
	Checksums Asset   `json:"checksums"`
}

type Client struct {
	http *http.Client
	api  string
}

func NewClient() *Client {
	return &Client{api: releasesAPI, http: &http.Client{
		Timeout: 5 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many update redirects")
			}
			if req.URL.Scheme != "https" {
				return fmt.Errorf("update redirect must use HTTPS")
			}
			return nil
		},
	}}
}

func AssetName(product Product, goos, arch string) (string, error) {
	if product != App && product != CLI {
		return "", fmt.Errorf("unknown update product %q", product)
	}
	if arch != "arm64" && arch != "amd64" {
		return "", fmt.Errorf("updates do not support %s", arch)
	}
	if goos != "darwin" && goos != "linux" && goos != "windows" {
		return "", fmt.Errorf("updates do not support %s", goos)
	}
	name := "koto-"
	if product == CLI {
		name += "cli-"
	}
	name += goos + "-" + arch
	if goos == "windows" {
		name += ".exe"
	}
	if goos == "darwin" && product == App {
		name += ".dmg"
	}
	return name, nil
}

// Check selects by semantic version instead of /latest, which excludes GitHub
// prereleases. Auto keeps stable users on stable and beta users on the beta track.
func (c *Client) Check(ctx context.Context, current string, product Product, goos, arch, channel string) (*Result, error) {
	installed, valid := parseVersion(current)
	if !valid {
		return nil, fmt.Errorf("development builds cannot update themselves")
	}
	if channel == "" || channel == "auto" {
		channel = "stable"
		if len(installed.pre) > 0 {
			channel = "beta"
		}
	}
	if channel != "stable" && channel != "beta" {
		return nil, fmt.Errorf("update channel must be stable or beta")
	}
	name, err := AssetName(product, goos, arch)
	if err != nil {
		return nil, err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	b, err := c.read(checkCtx, c.api, 8<<20)
	if err != nil {
		return nil, err
	}
	var releases []release
	if err := json.Unmarshal(b, &releases); err != nil {
		return nil, fmt.Errorf("invalid GitHub release response: %w", err)
	}
	var newest *release
	var newestVersion version
	for i := range releases {
		r := &releases[i]
		v, ok := parseVersion(r.Tag)
		if !ok || r.Draft || (channel == "stable" && (r.Prerelease || len(v.pre) > 0)) {
			continue
		}
		if newest == nil || compare(v, newestVersion) > 0 {
			newest, newestVersion = r, v
		}
	}
	if newest == nil {
		return nil, fmt.Errorf("no published %s release is available", channel)
	}
	return resultForRelease(newest, current, product, name)
}

func resultForRelease(r *release, current string, product Product, name string) (*Result, error) {
	result := &Result{Tag: r.Tag, Current: current, Latest: strings.TrimPrefix(r.Tag, "v"), Available: Newer(r.Tag, current), URL: Repository + "/releases/tag/" + r.Tag, Product: product}
	for _, asset := range r.Assets {
		if asset.Name != name && asset.Name != "SHA256SUMS" {
			continue
		}
		if err := validateAsset(asset, r.Tag); err != nil {
			return nil, err
		}
		if asset.Name == name {
			if result.Asset.Name != "" {
				return nil, fmt.Errorf("duplicate release asset %s", name)
			}
			result.Asset = asset
		} else {
			if result.Checksums.Name != "" {
				return nil, fmt.Errorf("duplicate release checksums")
			}
			result.Checksums = asset
		}
	}
	if result.Asset.Name == "" || result.Checksums.Name == "" {
		return nil, fmt.Errorf("release %s is missing %s or SHA256SUMS", r.Tag, name)
	}
	return result, nil
}

func validateAsset(a Asset, tag string) error {
	expected := Repository + "/releases/download/" + tag + "/" + a.Name
	u, err := url.Parse(a.URL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || a.URL != expected || a.Size <= 0 || a.Size > 1<<30 {
		return fmt.Errorf("invalid release asset %q", a.Name)
	}
	return nil
}

func (c *Client) get(ctx context.Context, address string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "koto-updater")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		if resp.StatusCode == 403 || resp.StatusCode == 429 {
			return nil, fmt.Errorf("GitHub refused the update request (HTTP %d); retry later", resp.StatusCode)
		}
		return nil, fmt.Errorf("update request failed: HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

func (c *Client) read(ctx context.Context, address string, limit int64) ([]byte, error) {
	resp, err := c.get(ctx, address)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("update response is too large")
	}
	return b, nil
}

// Fetch an exact tag for package archives, including the installed release's
// rollback DEB. Never substitute /latest for recovery material.
func (c *Client) releaseAsset(ctx context.Context, tag, current string, product Product, name string) (*Result, error) {
	if !Released(tag) {
		return nil, fmt.Errorf("invalid release tag")
	}
	endpoint, err := url.Parse(c.api)
	if err != nil {
		return nil, err
	}
	endpoint.RawQuery = ""
	endpoint.Path = strings.TrimSuffix(endpoint.Path, "/") + "/tags/" + tag
	requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	b, err := c.read(requestCtx, endpoint.String(), 8<<20)
	if err != nil {
		return nil, err
	}
	var r release
	if err = json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("invalid GitHub release response: %w", err)
	}
	if r.Tag != tag || r.Draft {
		return nil, fmt.Errorf("release tag is not the requested published version")
	}
	return resultForRelease(&r, current, product, name)
}
