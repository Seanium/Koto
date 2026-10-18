package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenDataDirectorySpecialCharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "索引 [copy] O'Brien #100%")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.SetSetting("marker", "路径 中文 🐙")
	s.Close()
	if _, err := os.Stat(filepath.Join(dir, "koto.sqlite")); err != nil {
		t.Fatal("SQLite opened a different URI path:", err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var value string
	if !s.Setting("marker", &value) || value != "路径 中文 🐙" {
		t.Fatalf("reopened wrong database: %q", value)
	}
}
