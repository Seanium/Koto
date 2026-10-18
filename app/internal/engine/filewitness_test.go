package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"koto/internal/core"
)

type witnessDecoder struct{}

func (witnessDecoder) FormatVersion() string         { return "witness-test" }
func (witnessDecoder) InitialState() core.JsonObject { return core.JsonObject{} }
func (witnessDecoder) Decode(record any, _ []byte, _ *DecodeContext) DecodeOut {
	return DecodeOut{NativeID: core.S(core.Obj(record)["id"])}
}

func TestSameMetadataMiddleRewriteWithinTick(t *testing.T) {
	file := filepath.Join(t.TempDir(), "session.jsonl")
	line := func(id, body string) string { return fmt.Sprintf("{\"id\":%q,\"text\":%q}\n", id, body) }
	original := []byte(line("first", strings.Repeat("a", 128<<10)) + line("middle", strings.Repeat("b", 128<<10)) + line("last", strings.Repeat("c", 128<<10)))
	if err := os.WriteFile(file, original, 0600); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(file)
	ref := core.NativeRef{Path: file, Size: st.Size(), MtimeMs: MtimeMs(st)}
	var before core.SourceOffset
	if err := TailJSONL(context.Background(), ref, nil, witnessDecoder{}, func(b core.SourceBatch) error { before = b.Offset; return nil }, 0, false); err != nil {
		t.Fatal(err)
	}
	if before.WitnessStable {
		t.Fatal("a just-written file cannot be trusted as stable")
	}
	updated := append([]byte(nil), original...)
	updated[len(updated)/2] = 'z' // Outside both sampled windows, preserving the final record.
	if err := os.WriteFile(file, updated, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(file)
	if after.Size() != ref.Size || MtimeMs(after) != ref.MtimeMs {
		t.Fatal("regression must preserve size and mtime")
	}
	if Unchanged(ref, &before) {
		t.Fatal("same-tick middle rewrite was hidden by the metadata cache")
	}
	rewritten := false
	var final core.SourceOffset
	if err := TailJSONL(context.Background(), ref, &before, witnessDecoder{}, func(b core.SourceBatch) error {
		rewritten = rewritten || b.Rewritten != nil
		final = b.Offset
		return nil
	}, 0, false); err != nil {
		t.Fatal(err)
	}
	if !rewritten || final.PrefixHash == before.PrefixHash || final.Records != before.Records {
		t.Fatal("middle rewrite must rebuild the prefix while preserving the record count")
	}
}

type byteCounter int64

func (n *byteCounter) Write(b []byte) (int, error) { *n += byteCounter(len(b)); return len(b), nil }

func TestOldLargeFileWitnessHasBoundedRead(t *testing.T) {
	file := filepath.Join(t.TempDir(), "large.jsonl")
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(64 << 20); err != nil {
		t.Fatal(err)
	}
	var count byteCounter
	_, stable, err := fileWitness(f, &count, time.Now().Add(time.Hour))
	if err != nil || !stable {
		t.Fatalf("stable file: %v, %v", stable, err)
	}
	if int64(count) != 2*witnessWindow {
		t.Fatalf("cache check read %d bytes of a 64 MiB file", count)
	}
}

func TestWitnessIncludesSQLiteWAL(t *testing.T) {
	for _, harness := range []string{"antigravity", "opencode", "zcode", "cursor"} {
		t.Run(harness, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "session.db")
			if err := os.WriteFile(file, bytes.Repeat([]byte("d"), 4096), 0600); err != nil {
				t.Fatal(err)
			}
			wal := file + "-wal"
			if err := os.WriteFile(wal, bytes.Repeat([]byte("a"), 4096), 0600); err != nil {
				t.Fatal(err)
			}
			ref := core.NativeRef{Harness: harness, Path: file}
			one, _, err := SourceWitness(ref)
			if err != nil {
				t.Fatal(err)
			}
			st, _ := os.Stat(wal)
			if err := os.WriteFile(wal, bytes.Repeat([]byte("b"), 4096), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(wal, st.ModTime(), st.ModTime()); err != nil {
				t.Fatal(err)
			}
			two, _, err := SourceWitness(ref)
			if err != nil || one == two {
				t.Fatalf("WAL overwrite not detected: %v", err)
			}
		})
	}
}
