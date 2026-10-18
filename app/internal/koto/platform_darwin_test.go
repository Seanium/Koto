package koto

import (
	"path/filepath"
	"testing"
)

func TestPlatformDefaultsMacDataRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KOTO_HOME", "")
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "xdg"))
	eq(t, PlatformDefaults().DataDir, filepath.Join(home, "Library", "Application Support", "koto"))
	t.Setenv("KOTO_HOME", filepath.Join(home, "isolated"))
	eq(t, PlatformDefaults().DataDir, filepath.Join(home, "isolated"))
	a, err := New(Config{DataDir: filepath.Join(home, "explicit"), Enable: []string{"no-connectors"}, NoDesktop: true})
	must(t, err)
	defer a.Close()
	eq(t, a.DataDir, filepath.Join(home, "explicit"))
}
