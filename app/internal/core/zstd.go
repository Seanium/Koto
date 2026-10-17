package core

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/klauspost/compress/zstd"
)

const zstdMagic = 0xfd2fb528

// ZstdFrameEnd is the end offset of the (non-skippable) frame starting at pos; an error if it is not whole.
func ZstdFrameEnd(buf []byte, pos int) (int, error) {
	if pos+5 > len(buf) || binary.LittleEndian.Uint32(buf[pos:]) != zstdMagic {
		return 0, fmt.Errorf("zstd: bad frame magic at %d", pos)
	}
	fhd := buf[pos+4]
	fcs, single, checksum, did := int(fhd>>6), int(fhd>>5)&1, int(fhd>>2)&1, int(fhd&3)
	singleSize := 0
	if single == 1 {
		singleSize = 1
	}
	p := pos + 5 + (1 - single) + []int{0, 1, 2, 4}[did] + []int{singleSize, 2, 4, 8}[fcs]
	for {
		if p+3 > len(buf) {
			return 0, errors.New("zstd: truncated block header")
		}
		h := int(buf[p]) | int(buf[p+1])<<8 | int(buf[p+2])<<16
		last, typ, size := h&1, (h>>1)&3, h>>3
		if typ == 3 {
			return 0, errors.New("zstd: reserved block type")
		}
		if typ == 1 {
			p += 4
		} else {
			p += 3 + size
		}
		if last == 1 {
			break
		}
	}
	if checksum == 1 {
		p += 4
	}
	if p > len(buf) {
		return 0, errors.New("zstd: truncated frame")
	}
	return p, nil
}

// MaxFrameOutput refuses frames that would decode larger (decompression bombs).
const MaxFrameOutput = 1 << 30

// ZstdDecompressAll decodes every frame of a container (dsh appends one frame per write batch).
func ZstdDecompressAll(buf []byte) ([]byte, error) {
	dec, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(MaxFrameOutput), zstd.WithDecoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	var out bytes.Buffer
	pos := 0
	for pos+4 <= len(buf) {
		magic := binary.LittleEndian.Uint32(buf[pos:])
		if magic&0xfffffff0 == 0x184d2a50 {
			if pos+8 > len(buf) {
				break
			}
			pos += 8 + int(binary.LittleEndian.Uint32(buf[pos+4:]))
			continue
		}
		end, err := ZstdFrameEnd(buf, pos)
		if err != nil {
			return nil, err
		}
		b, err := dec.DecodeAll(buf[pos:end], nil)
		if err != nil {
			return nil, err
		}
		out.Write(b)
		pos = end
	}
	return out.Bytes(), nil
}

// ZstdCompress writes one frame (tests and fixtures).
func ZstdCompress(b []byte) []byte {
	enc, _ := zstd.NewWriter(nil)
	defer enc.Close()
	return enc.EncodeAll(b, nil)
}
