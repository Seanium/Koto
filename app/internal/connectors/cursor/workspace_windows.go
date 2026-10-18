package cursor

import (
	"os"
	"strconv"
	"syscall"
)

func folderWorkspaceSalt(info os.FileInfo) (string, error) {
	ms := info.Sys().(*syscall.Win32FileAttributeData).CreationTime.Nanoseconds() / 1000000
	if ms == 0 {
		return "", nil
	}
	return strconv.FormatInt(ms, 10), nil
}
