// Package update checks GitHub Releases and installs verified, platform-specific
// koto builds. It is shared by the independent desktop and CLI applications.
package update

import (
	"cmp"
	"regexp"
	"strconv"
	"strings"
)

var versionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

type version struct {
	n   [3]uint64
	pre []string
}

func parseVersion(s string) (version, bool) {
	var v version
	m := versionPattern.FindStringSubmatch(s)
	if m == nil {
		return v, false
	}
	for i := range v.n {
		n, err := strconv.ParseUint(m[i+1], 10, 64)
		if err != nil {
			return v, false
		}
		v.n[i] = n
	}
	if m[4] != "" {
		v.pre = strings.Split(m[4], ".")
		for _, p := range v.pre {
			if numeric(p) && len(p) > 1 && p[0] == '0' {
				return v, false
			}
		}
	}
	return v, true
}

func numeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

func compare(a, b version) int {
	for i := range a.n {
		if c := cmp.Compare(a.n[i], b.n[i]); c != 0 {
			return c
		}
	}
	if len(a.pre) == 0 || len(b.pre) == 0 {
		return cmp.Compare(len(b.pre), len(a.pre))
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		x, y := a.pre[i], b.pre[i]
		nx, ny := numeric(x), numeric(y)
		if nx != ny {
			if nx {
				return -1
			}
			return 1
		}
		if nx && len(x) != len(y) {
			return cmp.Compare(len(x), len(y))
		}
		if c := strings.Compare(x, y); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a.pre), len(b.pre))
}

// Newer compares semantic versions, including numeric prerelease identifiers.
// Invalid or development versions never authorize an automatic replacement.
func Newer(candidate, current string) bool {
	a, okA := parseVersion(candidate)
	b, okB := parseVersion(current)
	return okA && okB && compare(a, b) > 0
}

func Released(s string) bool { _, ok := parseVersion(s); return ok }
