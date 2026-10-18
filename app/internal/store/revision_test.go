package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"koto/internal/core"
	"koto/internal/engine"
)

func TestNativeRevisionTracksWitnessAndDecoder(t *testing.T) {
	file := filepath.Join(t.TempDir(), "history.jsonl")
	if err := os.WriteFile(file, []byte("first\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-time.Hour)
	if err := os.Chtimes(file, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	c := &engine.Connector{Version: "v1"}
	ref := core.NativeRef{Path: file, Size: 6, MtimeMs: float64(stamp.UnixMilli()), Meta: core.JsonObject{"title": "title"}}
	before := nativeRevision(c, ref)
	if nativeRevision(c, ref) != before {
		t.Fatal("unchanged source revision unstable")
	}
	if err := os.WriteFile(file, []byte("other\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	after := nativeRevision(c, ref)
	if after == before {
		t.Fatal("same size/mtime rewrite did not invalidate")
	}
	c.Version = "v2"
	if nativeRevision(c, ref) == after {
		t.Fatal("decoder change did not invalidate")
	}
}
