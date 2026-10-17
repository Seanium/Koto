package engine

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

func fileChangeMeta(_ *os.File, st os.FileInfo) (string, time.Time, bool) {
	s, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return "", time.Time{}, false
	}
	return fmt.Sprintf("%d:%d:%d:%d", s.Dev, s.Ino, s.Ctimespec.Sec, s.Ctimespec.Nsec), time.Unix(s.Ctimespec.Sec, s.Ctimespec.Nsec), true
}
