package shared

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

// FileURL preserves Windows drive letters and UNC shares in standard file URIs.
func FileURL(path string) *url.URL {
	p := filepath.ToSlash(path)
	u := &url.URL{Scheme: "file", Path: p}
	if runtime.GOOS == "windows" {
		if strings.HasPrefix(p, "//") {
			parts := strings.SplitN(p[2:], "/", 2)
			u.Host = parts[0]
			u.Path = "/"
			if len(parts) == 2 {
				u.Path += parts[1]
			}
		} else if len(p) >= 2 && p[1] == ':' {
			u.Path = "/" + p
		}
	}
	return u
}

func FileURLPath(u *url.URL) (string, bool) {
	if u.Scheme != "file" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	p := u.Path
	if runtime.GOOS == "windows" {
		if u.Host != "" {
			if strings.ContainsAny(u.Host, `:\/`) {
				return "", false
			}
			p = "//" + u.Host + p
		} else if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
			p = p[1:]
		}
	} else if u.Host != "" {
		return "", false
	}
	p = filepath.FromSlash(p)
	return p, filepath.IsAbs(p)
}
