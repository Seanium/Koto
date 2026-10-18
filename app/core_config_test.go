package main

import (
	"path/filepath"
	"testing"
)

func TestIndependentAppDataKeepsNativeProfileDiscovery(t *testing.T) {
	data := t.TempDir()
	t.Setenv("KOTO_APP_SANDBOX", "")
	t.Setenv("KOTO_APP_DATA_DIR", data)
	cfg := configFromEnv(nil)
	if cfg.DataDir != data || cfg.NoDesktop || cfg.CursorRoot != "" || cfg.ClaudeRoot != "" {
		t.Fatalf("isolating koto data must retain native desktop and CLI discovery: %+v", cfg)
	}
	t.Setenv("KOTO_APP_SANDBOX", data)
	cfg = configFromEnv(nil)
	if !cfg.NoDesktop || cfg.CursorRoot != filepath.Join(data, "cursor") || cfg.DataDir != filepath.Join(data, "hub") {
		t.Fatalf("a synthetic sandbox must remain isolated from user sessions: %+v", cfg)
	}
}
