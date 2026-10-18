package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRevealSessionFileAndProjectFolder(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "会话 #1 %.jsonl")
	if err := os.WriteFile(file, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, file} {
		if !canRevealPath(path) {
			t.Errorf("existing folder or session file cannot be revealed: %s", path)
		}
	}
	for _, path := range []string{"", ".", "session.jsonl", filepath.Join(dir, "missing.jsonl")} {
		if canRevealPath(path) {
			t.Errorf("missing or relative path accepted: %s", path)
		}
	}
}
