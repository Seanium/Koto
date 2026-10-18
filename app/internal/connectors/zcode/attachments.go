package zcode

import (
	"encoding/base64"
	"koto/internal/connectors/shared"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const maxAttachmentBytes = 256 * 1024 * 1024

var pathSegment = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

func sanitizeSegment(s string) string {
	s = pathSegment.ReplaceAllString(s, "_")
	if len(s) > 120 {
		s = s[:120]
	}
	if s == "" {
		return "unknown"
	}
	return s
}
func (c *connector) attachmentBytes(uri string) ([]byte, bool) {
	if strings.HasPrefix(uri, "data:") {
		if len(uri) > maxAttachmentBytes*4/3+1024 {
			return nil, false
		}
		comma := strings.IndexByte(uri, ',')
		if comma < 0 {
			return nil, false
		}
		if strings.HasSuffix(uri[:comma], ";base64") {
			b, e := base64.StdEncoding.DecodeString(uri[comma+1:])
			return b, e == nil && len(b) <= maxAttachmentBytes
		}
		s, e := url.PathUnescape(uri[comma+1:])
		return []byte(s), e == nil && len(s) <= maxAttachmentBytes
	}
	u, err := url.Parse(uri)
	if err != nil {
		return nil, false
	}
	var file string
	if u.Scheme == "zcode-artifact" {
		sid, e := url.PathUnescape(u.Host)
		if e != nil {
			return nil, false
		}
		id, e := url.PathUnescape(strings.TrimPrefix(u.Path, "/"))
		if e != nil || id == "" || strings.ContainsAny(id, "/\\") {
			return nil, false
		}
		dir := filepath.Join(c.root, "cli", "artifacts", sanitizeSegment(sid))
		files, e := os.ReadDir(dir)
		if e != nil {
			return nil, false
		}
		for _, f := range files {
			if !f.IsDir() && strings.Contains(f.Name(), id) {
				if file != "" {
					return nil, false
				}
				file = filepath.Join(dir, f.Name())
			}
		}
		// Artifact references cannot escape their native directory through symlinks.
		real, e := filepath.EvalSymlinks(file)
		root, re := filepath.EvalSymlinks(dir)
		if e != nil || re != nil || !strings.HasPrefix(real, root+string(filepath.Separator)) {
			return nil, false
		}
		file = real
	} else if u.Scheme == "file" {
		var ok bool
		file, ok = shared.FileURLPath(u)
		if !ok {
			return nil, false
		}
	} else if filepath.IsAbs(uri) {
		file = uri
	} else {
		return nil, false
	}
	st, e := os.Stat(file)
	if e != nil || !st.Mode().IsRegular() || st.Size() > maxAttachmentBytes {
		return nil, false
	}
	b, e := os.ReadFile(file)
	if e != nil {
		return nil, false
	}
	// Older native sessions store media data URLs in text artifacts.
	if u.Scheme == "zcode-artifact" && (strings.HasPrefix(string(b[:min(len(b), 32)]), "data:image/") || strings.HasPrefix(string(b[:min(len(b), 32)]), "data:video/") || strings.HasPrefix(string(b[:min(len(b), 32)]), "data:application/pdf")) {
		return c.attachmentBytes(string(b))
	}
	return b, true
}
