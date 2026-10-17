package legal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedNoticesContainCompleteSources(t *testing.T) {
	text, err := Notices()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join("..", "..", "..")
	paths := []string{filepath.Join(root, "LICENSE"), filepath.Join(root, "THIRD_PARTY_NOTICES.md")}
	err = filepath.WalkDir(filepath.Join(root, "licenses"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() != "INDEX.json" {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, strings.TrimSpace(string(b))) {
			t.Errorf("incomplete notice: %s", path)
		}
	}
}
