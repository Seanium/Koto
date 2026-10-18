package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelectedDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "验收说明.txt")
	if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", dir} {
		if got, err := selectedDirectory(path); err != nil || got != path {
			t.Fatalf("directory/cancel %q: got %q, %v", path, got, err)
		}
	}
	for _, path := range []string{file, filepath.Join(dir, "missing")} {
		if got, err := selectedDirectory(path); err == nil || got != "" {
			t.Fatalf("invalid directory %q accepted: %q, %v", path, got, err)
		}
	}
	link := filepath.Join(dir, "folder-link")
	if err := os.Symlink(dir, link); err == nil {
		if got, err := selectedDirectory(link); err != nil || got != link {
			t.Fatalf("directory symlink: %q, %v", got, err)
		}
	}
}
