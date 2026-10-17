//go:build windows

package shared

import "testing"

func TestWindowsFileURLRoundTrip(t *testing.T) {
	for _, path := range []string{`C:\Users\中文 O'Brien\a b.txt`, `D:\data\a#b%.db`, `\\server\share\项目\file.txt`} {
		u := FileURL(path)
		got, ok := FileURLPath(u)
		if !ok || got != path {
			t.Fatalf("%q → %s → %q (%v)", path, u, got, ok)
		}
	}
}
