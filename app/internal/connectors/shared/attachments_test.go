package shared

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/core"
)

func TestAttachmentReadBoundaries(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "assets")
	data := []byte("private fixture")
	sha := core.SHA256Hex(data)
	file := filepath.Join(dir, sha, "name (1).txt")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	link := func(p string) string {
		return "[附件：name](" + strings.NewReplacer("(", "%28", ")", "%29").Replace((&url.URL{Scheme: "file", Path: p}).String()) + ")"
	}
	reads := 0
	put := func(b []byte) core.JsonObject { reads++; return core.JsonObject{"sha256": core.SHA256Hex(b)} }
	p := DecodeAttachmentText(link(file), dir, put)
	if reads != 1 || len(p) != 1 || p[0]["blob"] == nil {
		t.Fatal("valid file was not decoded")
	}
	if err := os.WriteFile(file, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	p = DecodeAttachmentText(link(file), dir, put)
	if reads != 1 || p[0]["blob"] != nil {
		t.Fatal("changed bytes were accepted")
	}
	outside := filepath.Join(root, "private.txt")
	if err := os.WriteFile(outside, data, 0600); err != nil {
		t.Fatal(err)
	}
	p = DecodeAttachmentText(link(outside), dir, put)
	if reads != 1 || p[0]["type"] != "text" {
		t.Fatal("arbitrary file link was followed")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, file); err != nil {
		t.Skip(err)
	}
	p = DecodeAttachmentText(link(file), dir, put)
	if reads != 1 || p[0]["blob"] != nil {
		t.Fatal("symlink escaped session assets")
	}
}
