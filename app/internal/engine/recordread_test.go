package engine

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"koto/internal/core"
)

func TestRecordReadsStreamAndRelease(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "compressed"}[compressed], func(t *testing.T) {
			body := []byte("one\r\n" + strings.Repeat("x", 130000) + "\ntail\nunfinished")
			file := filepath.Join(t.TempDir(), "events.jsonl")
			data := body
			if compressed {
				file += ".zst"
				data = append(core.ZstdCompress(body[:64000]), core.ZstdCompress(body[64000:])...)
			}
			if err := os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, closeReads := withRecordReads(context.Background())
			scope := ctx.Value(recordReadsKey{}).(*recordReads)
			for _, point := range []struct {
				off  int64
				want string
			}{{0, "one"}, {5, strings.Repeat("x", 130000)}, {130006, "tail"}, {130011, ""}, {0, "one"}} {
				got, err := ReadRecordAtContext(ctx, file, point.off)
				if err != nil || string(got) != point.want {
					t.Fatalf("record at %d: len=%d err=%v", point.off, len(got), err)
				}
			}
			if len(scope.streams) != 1 {
				t.Fatal("request did not reuse stream")
			}
			open := scope.streams[file].file
			closeReads()
			if len(scope.streams) != 0 {
				t.Fatal("request retained streams")
			}
			// Read checks Go's closed descriptor state consistently on every OS;
			// Windows Stat delegates GetFileType and reports ERROR_INVALID_HANDLE.
			if _, err := open.Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("descriptor retained: %v", err)
			}
		})
	}
}

func TestRecordReadsBoundOpenFilesAndDetectRewrite(t *testing.T) {
	ctx, done := withRecordReads(context.Background())
	defer done()
	r := ctx.Value(recordReadsKey{}).(*recordReads)
	var last string
	for i := 0; i < 5; i++ {
		last = filepath.Join(t.TempDir(), "event.jsonl.zst")
		if err := os.WriteFile(last, core.ZstdCompress([]byte("old\nsecond\n")), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadRecordAtContext(ctx, last, 0); err != nil {
			t.Fatal(err)
		}
		if len(r.streams) > 2 {
			t.Fatal("unbounded readers")
		}
	}
	if err := os.WriteFile(last, core.ZstdCompress([]byte("new\nchanged\n")), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRecordAtContext(ctx, last, 4)
	if err != nil || string(got) != "changed" {
		t.Fatalf("stale rewrite: %q %v", got, err)
	}
}

func TestCompressedRecordReadCancellation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "large.jsonl.zst")
	if err := os.WriteFile(file, core.ZstdCompress([]byte("first\n"+strings.Repeat("x", 32<<20)+"\nlast\n")), 0600); err != nil {
		t.Fatal(err)
	}
	for name, offset := range map[string]int64{"read": 6, "skip": 32<<20 + 7} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx, done := withRecordReads(ctx)
			defer done() // A failed assertion must also release the request's files.
			if got, err := ReadRecordAtContext(ctx, file, 0); err != nil || string(got) != "first" {
				t.Fatalf("prime stream: %q %v", got, err)
			}
			scope := ctx.Value(recordReadsKey{}).(*recordReads)
			stream := scope.streams[file]
			// Cancel after the next real read, rather than racing a 1 ms timer
			// against an operation that can finish sooner on a warm machine.
			trigger := &cancelRecordReader{Reader: stream.reader, cancel: cancel}
			stream.reader = bufio.NewReaderSize(trigger, 64<<10)
			start := time.Now()
			_, err := ReadRecordAtContext(ctx, file, offset)
			if !trigger.read || !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel offset %d: read=%v err=%v", offset, trigger.read, err)
			}
			if time.Since(start) > time.Second {
				t.Fatal("cancellation took over a second")
			}
			done()
			if len(scope.streams) != 0 || len(scope.order) != 0 || stream.frames.decoder != nil {
				t.Fatal("cancelled request retained stream or decoder")
			}
			if _, err := stream.file.Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("cancelled request retained file descriptor: %v", err)
			}
		})
	}
}

type cancelRecordReader struct {
	io.Reader
	cancel context.CancelFunc
	read   bool
}

func (r *cancelRecordReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.read = true
		r.cancel()
	}
	return n, err
}
