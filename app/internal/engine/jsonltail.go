package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sync"

	"koto/internal/core"
)

// DecodeContext is what a JSONL decoder gets with each record.
type DecodeContext struct {
	Ref   core.NativeRef
	Index int
	State core.JsonObject
	blobs *[][]byte
}

// Blob adds bytes to the working copy's CAS; parts reference them by the same sha256.
func (c *DecodeContext) Blob(b []byte) core.JsonObject {
	*c.blobs = append(*c.blobs, append([]byte(nil), b...))
	return core.JsonObject{"sha256": core.SHA256Hex(b), "bytes": len(b)}
}

// Side reads a side file (e.g. Claude Code tool-results/).
func (c *DecodeContext) Side(abs string) ([]byte, bool) {
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

var zstMu sync.Mutex
var zstCache = map[string][]byte{}
var zstOrder []string

func decodedZst(file string, st os.FileInfo) ([]byte, error) {
	witness, stable, err := SourceWitness(core.NativeRef{Path: file})
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%s\x00%d\x00%d\x00%s", file, st.Size(), st.ModTime().UnixNano(), witness)
	zstMu.Lock()
	defer zstMu.Unlock()
	if b, ok := zstCache[key]; ok && stable {
		return b, nil
	}
	b, _, err := LoadDecoded(file)
	if err != nil {
		return nil, err
	}
	if !stable {
		return b, nil
	}
	zstCache[key] = b
	zstOrder = append(zstOrder, key)
	for len(zstOrder) > 2 {
		delete(zstCache, zstOrder[0])
		zstOrder = zstOrder[1:]
	}
	return b, nil
}

// ReadRecordAt reads the line starting at offset of the decoded stream.
func ReadRecordAt(file string, offset int64) ([]byte, error) {
	st, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	var chunk []byte
	if IsCompressed(file) {
		b, err := decodedZst(file, st)
		if err != nil {
			return nil, err
		}
		if offset > int64(len(b)) {
			return nil, nil
		}
		chunk = b[offset:]
	} else {
		end := offset + 64*1024*1024
		if end > st.Size() {
			end = st.Size()
		}
		chunk, err = readRange(file, offset, end)
		if err != nil {
			return nil, err
		}
	}
	nl := bytes.IndexByte(chunk, '\n')
	if nl < 0 {
		return nil, nil
	}
	return chunk[:nl], nil
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
	var full []byte
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
			full, _, err = LoadDecoded(file)
			if err != nil {
				return err
			}
			prefix, n := core.ZeroPrefix, 0
			var endPos, lastStart int64
			lastHash := ""
			forLines(full, 0, func(l line) bool {
				if n == off.Records {
					return false
				}
				if len(l.raw) == 0 {
					endPos = l.end
					return true
				}
				h := core.SHA256Hex(l.raw)
				prefix = core.ChainPrefix(prefix, h)
				n++
				lastStart, lastHash, endPos = l.start, h, l.end
				return true
			})
			if n == off.Records && prefix == off.PrefixHash {
				// Same content in a new container (compressed) or benign: continue from the same place.
				pos.FileID, pos.ByteOff, pos.LastStart, pos.LastHash = file, endPos, lastStart, lastHash
			} else {
				at := 0
				for _, c := range off.Checkpoints {
					if c.At <= n && checkpointMatches(full, c.At, c.Hash) {
						at = c.At
					}
				}
				rewritten = &struct{ AtRecord int }{at}
				off = emptyOffset(dec.FormatVersion(), file)
				pos = &off.Position
			}
		}
	}
	var data []byte
	if full != nil {
		data = full[pos.ByteOff:]
	} else if isZst {
		all, _, err := LoadDecoded(file)
		if err != nil {
			return err
		}
		if pos.ByteOff <= int64(len(all)) {
			data = all[pos.ByteOff:]
		}
	} else {
		data, err = readRange(file, pos.ByteOff, st.Size())
		if err != nil {
			return err
		}
	}
	state := off.State
	if len(state) == 0 {
		state = dec.InitialState()
	}
	var blobs [][]byte
	dctx := &DecodeContext{Ref: ref, State: state, blobs: &blobs}
	var records []core.BatchRecord
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
		return yield(b)
	}
	base := pos.ByteOff
	var ferr error
	forLines(data, base, func(l line) bool {
		if ctx.Err() != nil {
			ferr = ctx.Err()
			return false
		}
		if len(l.raw) == 0 {
			pos.ByteOff = l.end
			return true
		}
		h := core.SHA256Hex(l.raw)
		index := off.Records
		var out DecodeOut
		var rec any
		if err := json.Unmarshal(l.raw, &rec); err != nil {
			out = DecodeOut{NativeID: fmt.Sprintf("#bad%d", index)} // malformed: counted in the chain, produces nothing
		} else {
			dctx.Index = index
			out = safeDecode(dec, rec, l.raw, dctx, index)
		}
		off.Records++
		off.PrefixHash = core.ChainPrefix(off.PrefixHash, h)
		if off.Records%core.CheckpointEvery == 0 {
			off.Checkpoints = append(off.Checkpoints, core.Checkpoint{At: off.Records, Hash: off.PrefixHash})
		}
		pos.ByteOff, pos.LastStart, pos.LastHash = l.end, l.start, h
		start := l.start
		r := core.BatchRecord{NativeID: out.NativeID, SHA256: h, Bytes: len(l.raw), Offset: &start, Index: index, Raw: append([]byte(nil), l.raw...)}
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
		if len(records) >= batchRecords {
			if err := flush(); err != nil {
				ferr = err
				return false
			}
		}
		return true
	})
	if ferr != nil {
		return ferr
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

func checkpointMatches(data []byte, upTo int, expect string) bool {
	prefix, n := core.ZeroPrefix, 0
	forLines(data, 0, func(l line) bool {
		if n == upTo {
			return false
		}
		if len(l.raw) == 0 {
			return true
		}
		prefix = core.ChainPrefix(prefix, core.SHA256Hex(l.raw))
		n++
		return true
	})
	return n == upTo && prefix == expect
}
