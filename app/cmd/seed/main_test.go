package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPiSeedWindowsProjectPath(t *testing.T) {
	previousRoot := root
	root = t.TempDir()
	t.Cleanup(func() { root = previousRoot })
	cwd := `C:\Users\测试 O'Brien\项目 & [work]`
	piSession("fixture-session", cwd, "Fixture", 1, []turn{{user: "hello", reply: "world"}})
	dir := j("pi", "sessions", "--C--Users-测试 O'Brien-项目 & [work]--")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one native pi session in %q: %v, %v", dir, entries, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var header struct {
		Cwd string `json:"cwd"`
	}
	if err := json.Unmarshal([]byte(strings.SplitN(string(data), "\n", 2)[0]), &header); err != nil {
		t.Fatal(err)
	}
	if header.Cwd != cwd {
		t.Fatalf("project path changed: %q", header.Cwd)
	}
}
