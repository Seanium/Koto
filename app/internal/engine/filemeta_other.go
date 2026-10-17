//go:build !linux && !darwin && !windows

package engine

import (
	"os"
	"time"
)

func fileChangeMeta(*os.File, os.FileInfo) (string, time.Time, bool) {
	return "", time.Time{}, false
}
