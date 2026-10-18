package cursor

import (
	"os"
	"strconv"
	"syscall"
)

func folderWorkspaceSalt(info os.FileInfo) (string, error) {
	inode := info.Sys().(*syscall.Stat_t).Ino
	if inode == 0 {
		return "", nil
	}
	// fs.Stats.ino is a JavaScript number in Cursor, including its precision.
	return strconv.FormatFloat(float64(inode), 'f', 0, 64), nil
}
