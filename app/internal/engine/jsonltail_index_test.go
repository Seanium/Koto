package engine

import (
	"context"
	"errors"
	"koto/internal/core"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStreamingIndexRecordsMatchFullRead(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n"} {
		t.Run(strings.ReplaceAll(ending, "\n", "LF"), func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "events.jsonl")
			long := "[\"" + strings.Repeat("long record ", 100000) + "\"]"
			body := "[\"one\",\"two\"]" + ending + long + ending + "[\"unfinished"
			if err := os.WriteFile(file, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			type result struct {
				records []core.BatchRecord
				offset  core.SourceOffset
			}
			read := func(ctx context.Context, from *core.SourceOffset) result {
				var r result
				if err := TailJSONL(ctx, core.NativeRef{Path: file}, from, multiFixtureDecoder{}, func(b core.SourceBatch) error {
					r.records = append(r.records, b.Records...)
					r.offset = b.Offset
					return nil
				}, 1, false); err != nil {
					t.Fatal(err)
				}
				return r
			}
			full := read(context.Background(), nil)
			light := read(WithIndexOnly(context.Background()), nil)
			if len(full.records) != 3 || len(light.records) != 3 {
				t.Fatalf("lost long or multi record: %d/%d", len(full.records), len(light.records))
			}
			if core.ToJSON(full.offset) != core.ToJSON(light.offset) {
				t.Fatal("light checkpoint differs")
			}
			for i, f := range full.records {
				l := light.records[i]
				if len(l.Raw) != 0 || len(f.Raw) == 0 {
					t.Fatal("raw ownership mismatch")
				}
				f.Raw = nil
				if core.ToJSON(f) != core.ToJSON(l) {
					t.Fatalf("native record identity changed at %d", i)
				}
			}
			if full.offset.Position.ByteOff != int64(len(body)-len("[\"unfinished")) {
				t.Fatal("partial tail committed")
			}
			if err := os.WriteFile(file, []byte(body+"\"]"+ending), 0600); err != nil {
				t.Fatal(err)
			}
			next := read(WithIndexOnly(context.Background()), &light.offset)
			if len(next.records) != 1 || next.offset.Records != 3 {
				t.Fatal("incremental tail duplicated or omitted")
			}
		})
	}
}

func TestStreamingCancellationStopsBeforeNextRecord(t *testing.T) {
	file := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(file, []byte(strings.Repeat("[\"event\"]\n", 100)), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	batches := 0
	err := TailJSONL(ctx, core.NativeRef{Path: file}, nil, multiFixtureDecoder{}, func(core.SourceBatch) error { batches++; cancel(); return nil }, 1, false)
	if !errors.Is(err, context.Canceled) || batches != 1 {
		t.Fatalf("cancellation: %v batches=%d", err, batches)
	}
}

func TestRecentlyAppendedPrefixVerificationIsCancelable(t *testing.T) {
	file := filepath.Join(t.TempDir(), "active.jsonl")
	line := "[\"" + strings.Repeat("x", 1000) + "\"]\n"
	body := strings.Repeat(line, 16000)
	if err := os.WriteFile(file, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	var saved core.SourceOffset
	if err := TailJSONL(WithIndexOnly(context.Background()), core.NativeRef{Path: file}, nil, multiFixtureDecoder{}, func(b core.SourceBatch) error { saved = b.Offset; return nil }, 1000, false); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("[\"new\"]\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	// A recent append is suspect even when size grew: verify the whole stored
	// prefix, with cancellation between lines/chunks instead of one allocation.
	saved.WitnessStable = false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { time.Sleep(time.Millisecond); cancel() }()
	start := time.Now()
	yielded := false
	err = TailJSONL(WithIndexOnly(ctx), core.NativeRef{Path: file}, &saved, multiFixtureDecoder{}, func(core.SourceBatch) error { yielded = true; return nil }, 1000, false)
	if !errors.Is(err, context.Canceled) || yielded {
		t.Fatalf("canceled prefix verification committed: %v %v", err, yielded)
	}
	if time.Since(start) > time.Second {
		t.Fatal("prefix cancellation took over a second")
	}
	var records []core.BatchRecord
	if err = TailJSONL(WithIndexOnly(context.Background()), core.NativeRef{Path: file}, &saved, multiFixtureDecoder{}, func(b core.SourceBatch) error { records = append(records, b.Records...); return nil }, 1000, false); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Index != 16000 {
		t.Fatal("retry lost append or repeated history")
	}
}

func TestCompressedStreamingFramesOffsetsAndCorruption(t *testing.T) {
	file := filepath.Join(t.TempDir(), "events.jsonl.zst")
	first := []byte("[\"one\",\"two\"]\r\n[\"split")
	second := []byte(" across frames\"]\n[\"tail\"]\n")
	compressed := append(core.ZstdCompress(first), core.ZstdCompress(second)...)
	if err := os.WriteFile(file, compressed, 0600); err != nil {
		t.Fatal(err)
	}
	var saved core.SourceOffset
	var records []core.BatchRecord
	collect := func(b core.SourceBatch) error { records = append(records, b.Records...); saved = b.Offset; return nil }
	if err := TailJSONL(WithIndexOnly(context.Background()), core.NativeRef{Path: file}, nil, multiFixtureDecoder{}, collect, 1, false); err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 || saved.Records != 3 || saved.Position.ByteOff != int64(len(first)+len(second)) {
		t.Fatal("multi-frame decoded offsets changed")
	}
	if records[0].SHA256 != core.SHA256Hex("[\"one\",\"two\"]") || records[2].SHA256 != core.SHA256Hex("[\"split across frames\"]") {
		t.Fatal("compressed source hashes changed")
	}
	appended := append(compressed, core.ZstdCompress([]byte("[\"new\"]\n"))...)
	if err := os.WriteFile(file, appended, 0600); err != nil {
		t.Fatal(err)
	}
	records = nil
	if err := TailJSONL(context.Background(), core.NativeRef{Path: file}, &saved, multiFixtureDecoder{}, collect, 1, false); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Index != 3 {
		t.Fatal("compressed append duplicated history")
	}
	for _, bad := range [][]byte{{0x28, 0xb5}, {0x50, 0x2a, 0x4d, 0x18, 1}, {0x50, 0x2a, 0x4d, 0x18, 100, 0, 0, 0}, core.ZstdCompress([]byte("bad"))[:8]} {
		if err := os.WriteFile(file, append(append([]byte(nil), compressed...), bad...), 0600); err != nil {
			t.Fatal(err)
		}
		if err := TailJSONL(context.Background(), core.NativeRef{Path: file}, nil, multiFixtureDecoder{}, func(core.SourceBatch) error { return nil }, 0, false); err == nil {
			t.Fatal("truncated frame accepted")
		}
	}
}
