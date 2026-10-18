package platform

import "testing"

func TestWindowsProjectPath(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`C:\工作 [copy] O'Brien & literal`, `C:\工作 [copy] O'Brien & literal`},
		{`\\?\C:\工作 [copy] O'Brien & literal`, `C:\工作 [copy] O'Brien & literal`},
		{`\\?\d:/project`, `d:/project`},
		{`\\server\share\项目`, `\\server\share\项目`},
		{`\\?\UNC\server\share\项目`, `\\server\share\项目`},
		{`\\?\unc\server\share`, `\\server\share`},
		{`\\?\UNC\server`, `\\?\UNC\server`},
		{`\\?\C:relative`, `\\?\C:relative`},
		{`\\?\Volume{123}\project`, `\\?\Volume{123}\project`},
		{`\\.\pipe\named`, `\\.\pipe\named`},
		{`/work/normal`, `/work/normal`},
	} {
		t.Run(tc.in, func(t *testing.T) {
			if got := windowsProjectPath(tc.in); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
