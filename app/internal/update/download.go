package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Download streams into a private staging directory and returns only after the
// known byte count, published SHA-256 and feed digest agree. An unknown size
// still requires a digest and cannot exceed the same 1 GiB download limit.
// Failure removes the partial file; an installed executable is never touched.
func (c *Client) Download(ctx context.Context, r *Result, dir string, progress func(int64, int64)) (string, error) {
	tag := r.Tag
	if err := validateAsset(r.Asset, tag); err != nil {
		return "", err
	}
	if err := validateAsset(r.Checksums, tag); err != nil {
		return "", err
	}
	b, err := c.read(ctx, r.Checksums.URL, 128<<10)
	if err != nil {
		return "", err
	}
	if int64(len(b)) != r.Checksums.Size {
		return "", fmt.Errorf("release checksums have an unexpected size")
	}
	if err := verifyDigest(b, r.Checksums.Digest); err != nil {
		return "", err
	}
	want, err := checksum(b, r.Asset.Name)
	if err != nil {
		return "", err
	}
	if r.Asset.Digest != "" && r.Asset.Digest != "sha256:"+want {
		return "", fmt.Errorf("release checksum disagrees with update metadata")
	}
	resp, err := c.get(ctx, r.Asset.URL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	limit := r.Asset.Size
	if limit == 0 {
		limit = maxAssetSize
	}
	if resp.ContentLength > limit {
		return "", fmt.Errorf("download exceeds the allowed size of %d bytes", limit)
	}
	f, err := os.CreateTemp(dir, ".koto-download-*")
	if err != nil {
		return "", err
	}
	path := f.Name()
	complete := false
	defer func() {
		f.Close()
		if !complete {
			os.Remove(path)
		}
	}()
	hash := sha256.New()
	var input io.Reader = io.LimitReader(resp.Body, limit+1)
	if progress != nil {
		input = &progressReader{Reader: input, total: r.Asset.Size, notify: progress}
	}
	n, err := io.Copy(io.MultiWriter(f, hash), input)
	if err != nil {
		return "", err
	}
	if n > limit {
		return "", fmt.Errorf("download exceeds the allowed size of %d bytes", limit)
	}
	if r.Asset.Size > 0 && n != r.Asset.Size {
		return "", fmt.Errorf("download size mismatch: expected %d, received %d", r.Asset.Size, n)
	}
	if hex.EncodeToString(hash.Sum(nil)) != want {
		return "", fmt.Errorf("download SHA-256 mismatch")
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	// Keep the extension for native executable checks and hdiutil on macOS.
	final := path + filepath.Ext(r.Asset.Name)
	if final != path {
		if err := os.Rename(path, final); err != nil {
			return "", err
		}
	}
	complete = true
	return final, nil
}

func validSHA256Digest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func checksum(b []byte, name string) (string, error) {
	found := ""
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		digest, err := hex.DecodeString(fields[0])
		if err != nil || len(digest) != sha256.Size || found != "" {
			return "", fmt.Errorf("invalid or duplicate checksum for %s", name)
		}
		found = strings.ToLower(fields[0])
	}
	if found == "" {
		return "", fmt.Errorf("no checksum for %s", name)
	}
	return found, nil
}

func verifyDigest(b []byte, digest string) error {
	if digest == "" {
		return nil
	}
	sum := sha256.Sum256(b)
	if digest != "sha256:"+hex.EncodeToString(sum[:]) {
		return fmt.Errorf("release checksums SHA-256 mismatch")
	}
	return nil
}

type progressReader struct {
	io.Reader
	done, total int64
	notify      func(int64, int64)
}

func (r *progressReader) Read(b []byte) (int, error) {
	n, err := r.Reader.Read(b)
	r.done += int64(n)
	r.notify(r.done, r.total)
	return n, err
}
