package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"

	"koto/internal/core"
)

// DecodeContext is what a JSONL decoder gets with each record.
type DecodeContext struct {
	Ref       core.NativeRef
	Index     int
	State     core.JsonObject
	IndexOnly bool
	blobs     *[][]byte
}

// Blob adds bytes to the working copy's CAS; parts reference them by the same sha256.
func (c *DecodeContext) Blob(b []byte) core.JsonObject {
	if c.IndexOnly {
		return nil
	}
	*c.blobs = append(*c.blobs, append([]byte(nil), b...))
	return core.JsonObject{"sha256": core.SHA256Hex(b), "bytes": len(b)}
}

func (c *DecodeContext) BlobBase64(encoded string) core.JsonObject {
	if c.IndexOnly {
		return nil
	}
	return c.Blob(core.Base64(encoded))
}

// Side reads a side file (e.g. Claude Code tool-results/).
func (c *DecodeContext) Side(abs string) ([]byte, bool) {
	if c.IndexOnly {
		return nil, false
	}
	b, err := os.ReadFile(abs)
	return b, err == nil
}

// DecodeOut is what one record decodes to.
type DecodeOut struct {
	NativeID string
	Draft    *core.DraftEvent
	Alias    *core.ParentRef
	HasAlias bool
	Meta     core.JsonObject
	Lineage  *core.Lineage
}

// JSONLDecoder decodes one harness's records.
type JSONLDecoder interface {
	FormatVersion() string
	InitialState() core.JsonObject
	Decode(record any, raw []byte, ctx *DecodeContext) DecodeOut
}

// JSONLIndexParser can omit unused large fields before they are allocated as
// generic values. It is used only for a list-summary read, never full parsing.
type JSONLIndexParser interface {
	ParseIndex(raw []byte) (any, error)
}

// JSONLMultiDecoder decodes physical records containing multiple events and
// reports content errors. The source checkpoint advances once per record.
type JSONLMultiDecoder interface {
	JSONLDecoder
	DecodeMany(record any, raw []byte, ctx *DecodeContext) ([]DecodeOut, error)
}

var compressed = regexp.MustCompile(`\.zstd?$`)

// IsCompressed: zstd containers (Codex .jsonl.zst, dsh .jsonl.zstd).
func IsCompressed(file string) bool { return compressed.MatchString(file) }

// LoadDecoded reads a file, decompressed.
func LoadDecoded(file string) ([]byte, os.FileInfo, error) {
	st, err := os.Stat(file)
	if err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, nil, err
	}
	if IsCompressed(file) {
		data, err = core.ZstdDecompressAll(data)
	}
	return data, st, err
}

func readRange(file string, start, end int64) ([]byte, error) {
	if IsCompressed(file) {
		data, _, err := LoadDecoded(file)
		if err != nil {
			return nil, err
		}
		if start > int64(len(data)) {
			return nil, nil
		}
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		return data[start:end], nil
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if end < start {
		end = start
	}
	b := make([]byte, end-start)
	n, err := f.ReadAt(b, start)
	if n < len(b) {
		return b[:n], nil
	}
	return b, err
}

// ReadRecordAt reads one complete native record without retaining its container.
func ReadRecordAt(file string, offset int64) ([]byte, error) {
	return ReadRecordAtContext(context.Background(), file, offset)
}

type line struct {
	start int64
	raw   []byte
	end   int64 // position after the newline
}

// forLines calls fn for each complete line of data (base = position of data[0]).
func forLines(data []byte, base int64, fn func(l line) bool) {
	pos := 0
	for pos < len(data) {
		nl := bytes.IndexByte(data[pos:], '\n')
		if nl < 0 {
			return
		}
		nl += pos
		end := nl
		if end > pos && data[end-1] == '\r' {
			end--
		}
		if !fn(line{start: base + int64(pos), raw: data[pos:end], end: base + int64(nl) + 1}) {
			return
		}
		pos = nl + 1
	}
}

func emptyOffset(formatVersion, fileID string) core.SourceOffset {
	return core.SourceOffset{Position: core.BytePosition{Kind: "byte", FileID: fileID}, PrefixHash: core.ZeroPrefix, FormatVersion: formatVersion, State: core.JsonObject{}}
}

func forFileLines(ctx context.Context, file string, start, end int64, fn func(line) bool) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	return forReaderLines(ctx, io.NewSectionReader(f, start, max(0, end-start)), start, fn)
}

func forReaderLines(ctx context.Context, input io.Reader, start int64, fn func(line) bool) error {
	r := bufio.NewReaderSize(input, 64<<10)
	pos := start
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var b []byte
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			fragment, err := r.ReadSlice('\n')
			if err == io.EOF {
				return nil
			} // A partial final record is retried next time.
			if len(b) == 0 && err == nil {
				b = fragment
				break
			}
			b = append(b, fragment...)
			if err == nil {
				break
			}
			if err != bufio.ErrBufferFull {
				return err
			}
		}
		next := pos + int64(len(b))
		raw := bytes.TrimSuffix(bytes.TrimSuffix(b, []byte("\n")), []byte("\r"))
		if !fn(line{start: pos, raw: raw, end: next}) {
			return nil
		}
		pos = next
	}
}

// Unchanged requires a stable verified read, not just matching size/mtime.
func Unchanged(ref core.NativeRef, from *core.SourceOffset) bool {
	if from == nil || !from.WitnessStable || from.Witness == "" || ref.Size == 0 && ref.MtimeMs == 0 {
		return false
	}
	same := false
	if from.Position.Kind == "seq" {
		fs, _ := core.Num(from.State["fileSize"])
		mt, _ := core.Num(from.State["mtimeMs"])
		same = fs == float64(ref.Size) && mt == ref.MtimeMs
	} else {
		p := from.Position
		same = p.FileID == ref.Path && p.FileSize == ref.Size && p.MtimeMs == ref.MtimeMs
	}
	if !same {
		return false
	}
	witness, stable, err := SourceWitness(ref)
	return err == nil && stable && witness == from.Witness
}

// MtimeMs of a file as JavaScript reports it (ms with a fraction).
func MtimeMs(st os.FileInfo) float64 {
	return float64(st.ModTime().UnixNano()) / 1e6
}

// TailJSONL reads a JSONL session from `from`: complete lines only, a per-record chained hash, and
// a changed prefix (the history was rewritten) re-read from the start with Rewritten set.
func TailJSONL(ctx context.Context, ref core.NativeRef, from *core.SourceOffset, dec JSONLDecoder, yield func(core.SourceBatch) error, batchRecords int, fullVerify bool) error {
	file := ref.Path
	if err := ctx.Err(); err != nil {
		return err
	}
	if file == "" {
		return fmt.Errorf("tail: %s has no path", ref.Session)
	}
	if batchRecords <= 0 {
		batchRecords = 2000
	}
	st, err := os.Stat(file)
	if err != nil {
		return err
	}
	witness, stable, err := SourceWitness(ref)
	if err != nil {
		return err
	}
	isZst := IsCompressed(file)
	var off core.SourceOffset
	if from != nil && from.Position.Kind == "byte" {
		off = from.Clone()
	} else {
		off = emptyOffset(dec.FormatVersion(), file)
	}
	var rewritten *struct{ AtRecord int }
	pos := &off.Position
	if off.Records > 0 {
		suspect := fullVerify || pos.FileID != file || isZst || !off.WitnessStable || !stable ||
			(st.Size() == pos.FileSize && witness != off.Witness)
		if !suspect {
			if st.Size() < pos.ByteOff {
				suspect = true
			} else {
				last, err := readRange(file, pos.LastStart, pos.ByteOff)
				if err != nil {
					return err
				}
				body := bytes.TrimSuffix(last, []byte("\n"))
				body = bytes.TrimSuffix(body, []byte("\r"))
				if core.SHA256Hex(body) != pos.LastHash {
					suspect = true
				}
			}
		}
		if suspect {
			if err := ctx.Err(); err != nil {
				return err
			}
			prefix, n := core.ZeroPrefix, 0
			var endPos, lastStart int64
			lastHash := ""
			checkpoints := map[int]string{}
			for _, c := range off.Checkpoints {
				checkpoints[c.At] = c.Hash
			}
			matched := 0
			verify := func(l line) bool {
				if ctx.Err() != nil || n == off.Records {
					return false
				}
				if len(l.raw) == 0 {
					endPos = l.end
					return true
				}
				h := core.SHA256Hex(l.raw)
				prefix = core.ChainPrefix(prefix, h)
				n++
				if checkpoints[n] == prefix {
					matched = n
				}
				lastStart, lastHash, endPos = l.start, h, l.end
				return true
			}
			if isZst {
				if err := forDecodedLines(ctx, file, 0, verify); err != nil {
					return err
				}
			} else if err := forFileLines(ctx, file, 0, st.Size(), verify); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if n == off.Records && prefix == off.PrefixHash {
				pos.FileID, pos.ByteOff, pos.LastStart, pos.LastHash = file, endPos, lastStart, lastHash
			} else {
				rewritten = &struct{ AtRecord int }{matched}
				off = emptyOffset(dec.FormatVersion(), file)
				pos = &off.Position
			}
		}
	}
	state := off.State
	if len(state) == 0 {
		state = dec.InitialState()
	}
	var blobs [][]byte
	dctx := &DecodeContext{Ref: ref, State: state, blobs: &blobs, IndexOnly: IsIndexOnly(ctx)}
	var records []core.BatchRecord
	batchBytes := 0
	var drafts []core.DraftEvent
	meta := core.JsonObject{}
	var lineage *core.Lineage
	first := true
	flush := func() error {
		o := off.Clone()
		o.Witness, o.WitnessStable = witness, stable
		o.State = core.Clone(state).(map[string]any)
		b := core.SourceBatch{Ref: ref, Drafts: drafts, Records: records, Blobs: blobs, Offset: o, Lineage: lineage}
		if len(meta) > 0 {
			b.Meta = meta
		}
		if first && rewritten != nil {
			b.Rewritten = rewritten
		}
		first = false
		records, drafts, blobs, meta = nil, nil, nil, core.JsonObject{}
		batchBytes = 0
		return yield(b)
	}
	base := pos.ByteOff
	var ferr error
	visit := func(l line) bool {
		if ctx.Err() != nil {
			ferr = ctx.Err()
			return false
		}
		if len(l.raw) == 0 {
			pos.ByteOff = l.end
			return true
		}
		h := core.SHA256Hex(l.raw)
		batchBytes += len(l.raw)
		index := off.Records
		var outputs []DecodeOut
		var rec any
		multi, isMulti := dec.(JSONLMultiDecoder)
		var parseErr error
		if parser, ok := dec.(JSONLIndexParser); ok && dctx.IndexOnly {
			rec, parseErr = parser.ParseIndex(l.raw)
		} else {
			parseErr = json.Unmarshal(l.raw, &rec)
		}
		if err := parseErr; err != nil {
			if isMulti {
				ferr = fmt.Errorf("decode record %d: %w", index, err)
				return false
			}
			outputs = []DecodeOut{{NativeID: fmt.Sprintf("#bad%d", index)}}
		} else {
			dctx.Index = index
			if isMulti {
				outputs, ferr = safeDecodeMany(multi, rec, l.raw, dctx, index)
				if ferr != nil {
					return false
				}
			} else {
				outputs = []DecodeOut{safeDecode(dec, rec, l.raw, dctx, index)}
			}
		}
		if len(outputs) == 0 {
			outputs = []DecodeOut{{NativeID: fmt.Sprintf("#empty%d", index)}}
		}
		off.Records++
		off.PrefixHash = core.ChainPrefix(off.PrefixHash, h)
		if off.Records%core.CheckpointEvery == 0 {
			off.Checkpoints = append(off.Checkpoints, core.Checkpoint{At: off.Records, Hash: off.PrefixHash})
		}
		pos.ByteOff, pos.LastStart, pos.LastHash = l.end, l.start, h
		start := l.start
		var raw []byte
		if !dctx.IndexOnly {
			raw = append([]byte(nil), l.raw...)
		}
		for _, out := range outputs {
			r := core.BatchRecord{NativeID: out.NativeID, SHA256: h, Bytes: len(l.raw), Offset: &start, Index: index, Raw: raw}
			if out.HasAlias {
				r.Alias = out.Alias
				if r.Alias == nil {
					r.Alias = core.NullParent()
				}
			}
			records = append(records, r)
			if out.Draft != nil {
				drafts = append(drafts, *out.Draft)
			}
			for k, v := range out.Meta {
				meta[k] = v
			}
			if out.Lineage != nil {
				lineage = out.Lineage
			}
		}
		if len(records) >= batchRecords || batchBytes >= 4<<20 {
			if err := flush(); err != nil {
				ferr = err
				return false
			}
		}
		return true
	}
	if isZst {
		if err := forDecodedLines(ctx, file, base, visit); err != nil {
			return err
		}
	} else if err := forFileLines(ctx, file, base, st.Size(), visit); err != nil {
		return err
	}
	if ferr != nil {
		return ferr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	pos.FileSize, pos.MtimeMs, pos.FileID = st.Size(), MtimeMs(st), file
	// Always a final batch, so the offset (with size and mtime) is kept.
	return flush()
}

// safeDecode: a decoder that panics on a hostile record produces nothing for it.
func safeDecode(dec JSONLDecoder, rec any, raw []byte, ctx *DecodeContext, index int) (out DecodeOut) {
	defer func() {
		if recover() != nil {
			out = DecodeOut{NativeID: fmt.Sprintf("#bad%d", index)}
		}
	}()
	return dec.Decode(rec, raw, ctx)
}

func safeDecodeMany(dec JSONLMultiDecoder, rec any, raw []byte, ctx *DecodeContext, index int) (out []DecodeOut, err error) {
	defer func() {
		if v := recover(); v != nil {
			out = nil
			err = fmt.Errorf("decode record %d panicked: %v", index, v)
		}
	}()
	out, err = dec.DecodeMany(rec, raw, ctx)
	if err != nil {
		return nil, fmt.Errorf("decode record %d: %w", index, err)
	}
	seen := map[string]bool{}
	for _, v := range out {
		if v.NativeID == "" || seen[v.NativeID] || v.Draft != nil && v.Draft.NativeID != v.NativeID {
			return nil, fmt.Errorf("decode record %d returned invalid or duplicate event identity", index)
		}
		seen[v.NativeID] = true
	}
	return out, nil
}
