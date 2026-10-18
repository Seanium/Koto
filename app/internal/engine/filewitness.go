package engine

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"koto/internal/core"
)

// Filesystems may give several writes the same mtime/ctime tick. A read made
// within this window must be verified again after the file becomes stable.
const fileStabilityWindow = time.Second
const witnessWindow = int64(32 * 1024)

var witnessBuffers = sync.Pool{New: func() any { b := make([]byte, 32*1024); return &b }}

// SourceWitness bounds ordinary cache checks to the first/last 32 KiB and
// native file identity/change metadata. Small files are covered in full. A
// recently changed file is never declared stable based on those samples.
// SQLite's WAL is included without opening either database through SQLite.
func SourceWitness(ref core.NativeRef) (string, bool, error) {
	return sourceWitnessAt(ref, time.Now())
}

func sourceWitnessAt(ref core.NativeRef, now time.Time) (string, bool, error) {
	h := sha256.New()
	stable := true
	paths := []string{ref.Path}
	if ref.Harness == "antigravity" || ref.Harness == "opencode" || ref.Harness == "zcode" || ref.Harness == "cursor" {
		paths = append(paths, ref.Path+"-wal")
	}
	for i, path := range paths {
		f, err := os.Open(path)
		if i > 0 && os.IsNotExist(err) {
			fmt.Fprintln(h, path, "absent")
			continue
		}
		if err != nil {
			return "", false, err
		}
		stamp, quiet, err := fileWitness(f, h, now)
		_ = f.Close()
		if err != nil {
			return "", false, err
		}
		fmt.Fprintln(h, path, stamp)
		stable = stable && quiet
	}
	return fmt.Sprintf("%x", h.Sum(nil)), stable, nil
}

func fileWitness(f *os.File, out io.Writer, now time.Time) (string, bool, error) {
	st, err := f.Stat()
	if err != nil {
		return "", false, err
	}
	identity, changed, known := fileChangeMeta(f, st)
	stamp := fmt.Sprintf("%d:%d:%s", st.Size(), st.ModTime().UnixNano(), identity)
	quiet := known && now.Sub(st.ModTime()) >= fileStabilityWindow && now.Sub(changed) >= fileStabilityWindow
	// Overlapping windows are read once, so files up to 64 KiB are fully hashed.
	first := min(st.Size(), witnessWindow)
	if st.Size() <= 2*witnessWindow {
		first = st.Size()
	}
	buffer := witnessBuffers.Get().(*[]byte)
	defer witnessBuffers.Put(buffer)
	if _, err := io.CopyBuffer(out, io.NewSectionReader(f, 0, first), *buffer); err != nil {
		return "", false, err
	}
	if first < st.Size() {
		if _, err := io.CopyBuffer(out, io.NewSectionReader(f, st.Size()-witnessWindow, witnessWindow), *buffer); err != nil {
			return "", false, err
		}
	}
	return stamp, quiet, nil
}
