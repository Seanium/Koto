package engine

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/klauspost/compress/zstd"
	"koto/internal/core"
)

// frameReader decodes one frame at a time, retaining the existing per-frame
// output limit without retaining the decompressed container. DSH can append
// multiple independent frames; skippable frames carry no JSONL bytes.
type frameReader struct {
	ctx                context.Context
	file               *os.File
	size, next, output int64
	decoder            *zstd.Decoder
}

func (r *frameReader) close() {
	if r.decoder != nil {
		r.decoder.Close()
		r.decoder = nil
	}
}
func (r *frameReader) Read(p []byte) (int, error) {
	for {
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
		if r.decoder == nil {
			start, end, err := r.nextFrame()
			if err != nil {
				return 0, err
			}
			r.next = end
			r.output = 0
			r.decoder, err = zstd.NewReader(io.NewSectionReader(r.file, start, end-start), zstd.WithDecoderMaxMemory(core.MaxFrameOutput), zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true))
			if err != nil {
				return 0, err
			}
		}
		n, err := r.decoder.Read(p)
		r.output += int64(n)
		if r.output > core.MaxFrameOutput {
			return 0, fmt.Errorf("zstd: frame output exceeds %d bytes", core.MaxFrameOutput)
		}
		if err == io.EOF {
			r.close()
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}
func (r *frameReader) nextFrame() (int64, int64, error) {
	var h [8]byte
	for {
		if err := r.ctx.Err(); err != nil {
			return 0, 0, err
		}
		start := r.next
		if r.size == start {
			return 0, 0, io.EOF
		}
		if r.size-start < 4 {
			return 0, 0, fmt.Errorf("zstd: truncated frame magic")
		}
		if _, err := r.file.ReadAt(h[:4], start); err != nil {
			return 0, 0, err
		}
		magic := binary.LittleEndian.Uint32(h[:4])
		if magic&0xfffffff0 == 0x184d2a50 {
			if r.size-start < 8 {
				return 0, 0, fmt.Errorf("zstd: truncated skippable header")
			}
			if _, err := r.file.ReadAt(h[:4], start+4); err != nil {
				return 0, 0, err
			}
			r.next = start + 8 + int64(binary.LittleEndian.Uint32(h[:4]))
			if r.next > r.size {
				return 0, 0, fmt.Errorf("zstd: truncated skippable payload")
			}
			continue
		}
		if magic != 0xfd2fb528 || r.size-start < 5 {
			return 0, 0, fmt.Errorf("zstd: bad frame magic at %d", start)
		}
		if _, err := r.file.ReadAt(h[:1], start+4); err != nil {
			return 0, 0, err
		}
		fhd := h[0]
		fcs, single, checksum, did := int(fhd>>6), int(fhd>>5)&1, int(fhd>>2)&1, int(fhd&3)
		pos := start + 5 + int64(1-single+[]int{0, 1, 2, 4}[did]+[]int{single, 2, 4, 8}[fcs])
		for {
			if err := r.ctx.Err(); err != nil {
				return 0, 0, err
			}
			if pos+3 > r.size {
				return 0, 0, fmt.Errorf("zstd: truncated block header")
			}
			if _, err := r.file.ReadAt(h[:3], pos); err != nil {
				return 0, 0, err
			}
			block := uint32(h[0]) | uint32(h[1])<<8 | uint32(h[2])<<16
			typ := (block >> 1) & 3
			if typ == 3 {
				return 0, 0, fmt.Errorf("zstd: reserved block type")
			}
			if typ == 1 {
				pos += 4
			} else {
				pos += 3 + int64(block>>3)
			}
			if block&1 != 0 {
				break
			}
		}
		pos += int64(checksum * 4)
		if pos > r.size {
			return 0, 0, fmt.Errorf("zstd: truncated frame")
		}
		return start, pos, nil
	}
}

func forDecodedLines(ctx context.Context, file string, start int64, fn func(line) bool) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	r := &frameReader{ctx: ctx, file: f, size: st.Size()}
	defer r.close()
	if start > 0 {
		if _, err = io.CopyN(io.Discard, r, start); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
	return forReaderLines(ctx, r, start, fn)
}
