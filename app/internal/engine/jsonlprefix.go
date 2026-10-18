package engine

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"

	"koto/internal/core"
)

type verifiedPrefix struct {
	prefix, lastHash string
	records, matched int
	end, lastStart   int64
}

func verifyFilePrefix(ctx context.Context, file string, size int64, off core.SourceOffset) (verifiedPrefix, error) {
	f, err := os.Open(file)
	if err != nil {
		return verifiedPrefix{}, err
	}
	defer f.Close()
	var input io.Reader = io.NewSectionReader(f, 0, size)
	if IsCompressed(file) {
		frames := &frameReader{ctx: ctx, file: f, size: size}
		defer frames.close()
		input = frames
	}
	return verifyReaderPrefix(ctx, bufio.NewReaderSize(input, 64<<10), off)
}

// verifyReaderPrefix hashes every complete record in the committed prefix.
// It never assembles a long line: at most the reader buffer and SHA state are
// retained. The chain is the same H(previous || H(record)) stored by TailJSONL.
func verifyReaderPrefix(ctx context.Context, reader *bufio.Reader, off core.SourceOffset) (verifiedPrefix, error) {
	var out verifiedPrefix
	var chain [64]byte
	var digest, prefix, last [sha256.Size]byte
	checkpoints := make(map[int]string, len(off.Checkpoints))
	for _, c := range off.Checkpoints {
		checkpoints[c.At] = c.Hash
	}
	finish := func() (verifiedPrefix, error) {
		if err := ctx.Err(); err != nil {
			return verifiedPrefix{}, err
		}
		out.prefix = hex.EncodeToString(prefix[:])
		if out.records > 0 {
			out.lastHash = hex.EncodeToString(last[:])
		}
		return out, nil
	}
	hash := sha256.New()
	var pos, start, bodyBytes int64
	continued, pendingCR := false, false
	cr := []byte{'\r'}
	for out.records < off.Records {
		if err := ctx.Err(); err != nil {
			return verifiedPrefix{}, err
		}
		fragment, err := reader.ReadSlice('\n')
		if err == io.EOF {
			return finish() // A partial final record never advances the checkpoint.
		}
		if err != nil && err != bufio.ErrBufferFull {
			return verifiedPrefix{}, err
		}
		if err := ctx.Err(); err != nil {
			return verifiedPrefix{}, err
		}
		pos += int64(len(fragment))
		complete := err == nil
		payload := fragment
		if complete {
			payload = payload[:len(payload)-1]
		}
		if !continued && complete {
			payload = bytes.TrimSuffix(payload, cr)
			bodyBytes = int64(len(payload))
			digest = sha256.Sum256(payload)
		} else {
			// Hold a trailing CR until the next fragment tells whether it is
			// the CR in CRLF or an ordinary byte inside the record.
			if pendingCR && len(payload) > 0 {
				hash.Write(cr)
				bodyBytes++
			}
			pendingCR = false
			if len(payload) > 0 && payload[len(payload)-1] == '\r' {
				payload = payload[:len(payload)-1]
				pendingCR = !complete
			}
			hash.Write(payload)
			bodyBytes += int64(len(payload))
			if !complete {
				continued = true
				continue
			}
			hash.Sum(digest[:0])
			hash.Reset()
		}
		if bodyBytes > 0 {
			copy(chain[sha256.Size:], digest[:])
			prefix = sha256.Sum256(chain[:])
			copy(chain[:sha256.Size], prefix[:])
			out.records++
			if expected, ok := checkpoints[out.records]; ok && expected == hex.EncodeToString(prefix[:]) {
				out.matched = out.records
			}
			last, out.lastStart = digest, start
		}
		out.end, start = pos, pos
		bodyBytes, continued, pendingCR = 0, false, false
	}
	return finish()
}
