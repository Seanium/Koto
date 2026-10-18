package engine

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"koto/internal/core"
)

type recordReadsKey struct{}

// A plan normally asks for native records in forward order. Keep at most two
// open streams for that request, rather than decompressing a whole container
// for every record or retaining it globally after the plan has finished.
type recordReads struct {
	mu      sync.Mutex
	streams map[string]*recordStream
	order   []string
}

type recordStream struct {
	file     *os.File
	frames   *frameReader
	reader   *bufio.Reader
	key      string
	position int64
}

func (s *recordStream) close() {
	if s.frames != nil {
		s.frames.close()
	}
	s.file.Close()
}

func withRecordReads(ctx context.Context) (context.Context, func()) {
	r := &recordReads{streams: make(map[string]*recordStream)}
	return context.WithValue(ctx, recordReadsKey{}, r), func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		for file, s := range r.streams {
			s.close()
			delete(r.streams, file)
		}
		r.order = nil
	}
}

// ReadRecordAtContext returns one complete line at a decoded byte offset. It
// checks cancellation while skipping and reading, including a very long line.
func ReadRecordAtContext(ctx context.Context, file string, offset int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if offset < 0 {
		return nil, fmt.Errorf("negative native record offset")
	}
	r, ok := ctx.Value(recordReadsKey{}).(*recordReads)
	if !ok {
		var closeReads func()
		ctx, closeReads = withRecordReads(ctx)
		defer closeReads()
		r = ctx.Value(recordReadsKey{}).(*recordReads)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	st, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	witness, _, err := SourceWitness(core.NativeRef{Path: file})
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%d/%d/%s", st.Size(), st.ModTime().UnixNano(), witness)
	s := r.streams[file]
	if s != nil && (s.key != key || offset < s.position) {
		s.close()
		delete(r.streams, file)
		s = nil
	}
	if s == nil {
		for len(r.streams) >= 2 && len(r.order) > 0 {
			old := r.order[0]
			r.order = r.order[1:]
			if v := r.streams[old]; v != nil {
				v.close()
				delete(r.streams, old)
			}
		}
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		s = &recordStream{file: f, key: key}
		var input io.Reader = f
		if IsCompressed(file) {
			s.frames = &frameReader{ctx: ctx, file: f, size: st.Size()}
			input = s.frames
		} else {
			if _, err := f.Seek(offset, io.SeekStart); err != nil {
				f.Close()
				return nil, err
			}
			s.position = offset
		}
		s.reader = bufio.NewReaderSize(input, 64<<10)
		r.streams[file] = s
		// Remove a prior entry when a stream was restarted after a rewrite/seek.
		for i, old := range r.order {
			if old == file {
				r.order = append(r.order[:i], r.order[i+1:]...)
				break
			}
		}
		r.order = append(r.order, file)
	}
	for s.position < offset {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := s.reader.Discard(int(min(offset-s.position, 64<<10)))
		s.position += int64(n)
		if err == io.EOF {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
	}
	var raw []byte
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		part, err := s.reader.ReadSlice('\n')
		s.position += int64(len(part))
		raw = append(raw, part...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err == io.EOF {
			return nil, nil
		} // an unfinished native record
		if err != nil {
			return nil, err
		}
		return bytes.TrimSuffix(bytes.TrimSuffix(raw, []byte("\n")), []byte("\r")), nil
	}
}
