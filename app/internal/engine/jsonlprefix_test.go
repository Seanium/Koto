package engine

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/core"
)

// The pre-optimization verifier, retained as an independent byte/offset oracle.
type referencePrefix struct {
	prefix, lastHash string
	records, matched int
	end, lastStart   int64
}

func referenceVerifyPrefix(ctx context.Context, input io.Reader, off core.SourceOffset, bufferSize int) (referencePrefix, error) {
	out := referencePrefix{prefix: core.ZeroPrefix}
	cp := map[int]string{}
	for _, checkpoint := range off.Checkpoints {
		cp[checkpoint.At] = checkpoint.Hash
	}
	r := bufio.NewReaderSize(input, bufferSize)
	var pos int64
	for {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		var b []byte
		for {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			fragment, err := r.ReadSlice('\n')
			if err == io.EOF {
				return out, nil
			}
			if len(b) == 0 && err == nil {
				b = fragment
				break
			}
			b = append(b, fragment...)
			if err == nil {
				break
			}
			if err != bufio.ErrBufferFull {
				return out, err
			}
		}
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if out.records == off.Records {
			return out, nil
		}
		end := pos + int64(len(b))
		raw := bytes.TrimSuffix(bytes.TrimSuffix(b, []byte("\n")), []byte("\r"))
		if len(raw) == 0 {
			out.end, pos = end, end
			continue
		}
		hash := core.SHA256Hex(raw)
		out.prefix = core.ChainPrefix(out.prefix, hash)
		out.records++
		if cp[out.records] == out.prefix {
			out.matched = out.records
		}
		out.lastStart, out.lastHash, out.end, pos = pos, hash, end, end
	}
}

func prefixBenchmarkInputs() []struct {
	name string
	data []byte
} {
	return []struct {
		name string
		data []byte
	}{
		{"giant-line", []byte("{\"data\":\"" + strings.Repeat("x", 64<<20) + "\"}\r\n")},
		{"many-small-lines", bytes.Repeat([]byte("{\"type\":\"event\",\"text\":\"short message in a long conversation 中文\"}\n"), 65536)},
		{"mixed-lines", bytes.Repeat([]byte(strings.Repeat("{\"text\":\"small\"}\n", 255)+"{\"image\":\""+strings.Repeat("x", 2<<20)+"\"}\r\n"), 16)},
	}
}

func BenchmarkPrefixVerification(b *testing.B) {
	samples := prefixBenchmarkInputs()
	if path := os.Getenv("KOTO_PREFIX_BENCH_FILE"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			b.Fatal(err)
		}
		samples = append(samples, struct {
			name string
			data []byte
		}{"native-snapshot", data})
	}
	for _, sample := range samples {
		file := filepath.Join(b.TempDir(), "prefix.jsonl")
		if err := os.WriteFile(file, sample.data, 0600); err != nil {
			b.Fatal(err)
		}
		off := prefixOffset(sample.data)
		for _, size := range []int{64 << 10, 256 << 10} {
			for _, streaming := range []bool{false, true} {
				name := map[bool]string{false: "reference", true: "streaming"}[streaming]
				b.Run(fmt.Sprintf("%s/%s/%dKiB", sample.name, name, size>>10), func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(sample.data)))
					for range b.N {
						f, err := os.Open(file)
						if err != nil {
							b.Fatal(err)
						}
						if streaming {
							_, err = verifyReaderPrefix(context.Background(), bufio.NewReaderSize(f, size), off)
						} else {
							_, err = referenceVerifyPrefix(context.Background(), f, off, size)
						}
						f.Close()
						if err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}

func prefixOffset(data []byte) core.SourceOffset {
	off := core.SourceOffset{PrefixHash: core.ZeroPrefix}
	forLines(data, 0, func(l line) bool {
		if len(l.raw) == 0 {
			return true
		}
		off.Records++
		off.PrefixHash = core.ChainPrefix(off.PrefixHash, core.SHA256Hex(l.raw))
		if off.Records%core.CheckpointEvery == 0 {
			off.Checkpoints = append(off.Checkpoints, core.Checkpoint{At: off.Records, Hash: off.PrefixHash})
		}
		return true
	})
	return off
}

func TestPrefixVerifierMatchesReference(t *testing.T) {
	for _, size := range []int{16, 64 << 10, 256 << 10} {
		for _, ending := range []string{"\n", "\r\n"} {
			for _, extra := range []int{-2, -1, 0, 1, 2} {
				data := []byte("\n\r\n" + strings.Repeat("x", size+extra) + ending + "\r\r\n" + strings.Repeat("short 中文"+ending, 520) + "\npartial")
				off := prefixOffset(data)
				// Changed history keeps only the last matching prior checkpoint.
				changed := bytes.Replace(data, []byte("short 中文"), []byte("other 中文"), 300)
				lateChange := append([]byte(nil), data...)
				lateChange[bytes.Index(data, []byte("short 中文"))+300*len("short 中文"+ending)] = 'S'
				counts := []int{0, 1, 2, 256, off.Records, off.Records + 1}
				for _, body := range [][]byte{data, changed, lateChange} {
					for _, count := range counts {
						off.Records = count
						want, err := referenceVerifyPrefix(context.Background(), bytes.NewReader(body), off, size)
						if err != nil {
							t.Fatal(err)
						}
						got, err := verifyReaderPrefix(context.Background(), bufio.NewReaderSize(bytes.NewReader(body), size), off)
						if err != nil {
							t.Fatal(err)
						}
						if got != (verifiedPrefix(want)) {
							t.Fatalf("size=%d extra=%d ending=%q records=%d: got %+v want %+v", size, extra, ending, count, got, want)
						}
					}
				}
			}
		}
	}
}

func TestPrefixVerifierCRAtBufferBoundary(t *testing.T) {
	for _, size := range []int{16, 64 << 10, 256 << 10} {
		for _, tail := range []string{"\r\n", "\r\r\n", "\rX\n", "\n", "\rpartial"} {
			data := []byte(strings.Repeat("x", size-1) + tail)
			off := core.SourceOffset{Records: 2}
			want, err := referenceVerifyPrefix(context.Background(), bytes.NewReader(data), off, size)
			if err != nil {
				t.Fatal(err)
			}
			got, err := verifyReaderPrefix(context.Background(), bufio.NewReaderSize(bytes.NewReader(data), size), off)
			if err != nil || got != verifiedPrefix(want) {
				t.Fatalf("size=%d tail=%q: got %+v want %+v err=%v", size, tail, got, want, err)
			}
		}
	}
}

type cancelPrefixReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r *cancelPrefixReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.cancel()
	return n, err
}

func TestPrefixVerifierCancellationAndReadError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := &cancelPrefixReader{Reader: strings.NewReader(strings.Repeat("x", 1<<20) + "\n"), cancel: cancel}
	if _, err := verifyReaderPrefix(ctx, bufio.NewReaderSize(input, 64<<10), core.SourceOffset{Records: 1}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	bad := errors.New("reader failure")
	if _, err := verifyReaderPrefix(context.Background(), bufio.NewReader(io.MultiReader(strings.NewReader("first\n"), errorPrefixReader{bad})), core.SourceOffset{Records: 2}); !errors.Is(err, bad) {
		t.Fatal(err)
	}
}

type errorPrefixReader struct{ err error }

func (r errorPrefixReader) Read([]byte) (int, error) { return 0, r.err }

func TestPrefixVerifierCompressedFramesAndClosedFile(t *testing.T) {
	first := []byte("first\r\n" + strings.Repeat("x", 64<<10) + "\r")
	second := []byte("\nsecond\npartial")
	data := append(append([]byte(nil), first...), second...)
	off := prefixOffset(data)
	file := filepath.Join(t.TempDir(), "events.jsonl.zst")
	compressed := append(core.ZstdCompress(first), core.ZstdCompress(second)...)
	if err := os.WriteFile(file, compressed, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := verifyFilePrefix(context.Background(), file, int64(len(compressed)), off)
	want, _ := referenceVerifyPrefix(context.Background(), bytes.NewReader(data), off, 64<<10)
	if err != nil || got != verifiedPrefix(want) {
		t.Fatalf("%+v %+v %v", got, want, err)
	}
	badFrames := [][]byte{{0x28, 0xb5}, {0x50, 0x2a, 0x4d, 0x18, 1}, {0x50, 0x2a, 0x4d, 0x18, 100, 0, 0, 0}}
	for _, bad := range badFrames {
		broken := append(append([]byte(nil), compressed...), bad...)
		if err := os.WriteFile(file, broken, 0600); err != nil {
			t.Fatal(err)
		}
		incomplete := off
		incomplete.Records++
		if _, err := verifyFilePrefix(context.Background(), file, int64(len(broken)), incomplete); err == nil {
			t.Fatal("corrupt frame accepted as a complete prefix")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = verifyFilePrefix(ctx, file, int64(len(compressed)), off); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// In particular Windows must not retain the native source handle on return.
	if err = os.Rename(file, file+".closed"); err != nil {
		t.Fatal(err)
	}
}

func TestPrefixVerifierTailAppendAndRewrite(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		t.Run(map[bool]string{false: "jsonl", true: "multiple-zstd-frames"}[compressed], func(t *testing.T) {
			var initial bytes.Buffer
			for i := 0; i < 600; i++ {
				fmt.Fprintf(&initial, "[\"record-%03d\"]\r\n", i)
				if i == 257 {
					fmt.Fprintf(&initial, "[\"%s\"]\r\n", strings.Repeat("long", 256<<10))
				}
			}
			initial.WriteString("\n[\"pending")
			file := filepath.Join(t.TempDir(), "native.jsonl")
			if compressed {
				file += ".zst"
			}
			write := func(data []byte) {
				t.Helper()
				if compressed {
					middle := len(data) / 2
					data = append(core.ZstdCompress(data[:middle]), core.ZstdCompress(data[middle:])...)
				}
				if err := os.WriteFile(file, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			read := func(from *core.SourceOffset) (core.SourceOffset, []core.BatchRecord, int) {
				t.Helper()
				var offset core.SourceOffset
				var records []core.BatchRecord
				rewritten := -1
				err := TailJSONL(WithIndexOnly(context.Background()), core.NativeRef{Path: file}, from, multiFixtureDecoder{}, func(b core.SourceBatch) error {
					offset = b.Offset
					records = append(records, b.Records...)
					if b.Rewritten != nil {
						rewritten = b.Rewritten.AtRecord
					}
					return nil
				}, 128, true)
				if err != nil {
					t.Fatal(err)
				}
				return offset, records, rewritten
			}
			write(initial.Bytes())
			first, _, rewritten := read(nil)
			if first.Records != 601 || rewritten != -1 {
				t.Fatalf("initial: %d %d", first.Records, rewritten)
			}
			before := core.ToJSON(first)
			appended := append(append([]byte(nil), initial.Bytes()...), []byte("\"]\r\n")...)
			write(appended)
			next, records, rewritten := read(&first)
			if len(records) != 1 || records[0].Index != 601 || next.Records != 602 || rewritten != -1 || core.ToJSON(first) != before {
				t.Fatal("append lost/duplicated history or changed caller offset")
			}
			st, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			changed := bytes.Replace(appended, []byte("record-400"), []byte("Record-400"), 1)
			write(changed)
			if err := os.Chtimes(file, st.ModTime(), st.ModTime()); err != nil {
				t.Fatal(err)
			}
			final, records, rewritten := read(&next)
			want := prefixOffset(changed)
			if rewritten != 256 || final.Records != want.Records || final.PrefixHash != want.PrefixHash || len(records) != want.Records {
				t.Fatalf("rewrite checkpoint/whole history changed: at=%d records=%d/%d", rewritten, final.Records, want.Records)
			}
			if final.Position.ByteOff != int64(len(changed)) {
				t.Fatal("decoded byte position changed")
			}
		})
	}
}
