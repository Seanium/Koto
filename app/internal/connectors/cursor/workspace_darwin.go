package cursor

import (
	"os"
	"strconv"
	"syscall"
)

func folderWorkspaceSalt(info os.FileInfo) (string, error) {
	stamp := info.Sys().(*syscall.Stat_t).Birthtimespec
	ms := stamp.Sec*1000 + stamp.Nsec/1000000
	if ms == 0 {
		return "", nil
	}
	return strconv.FormatInt(ms, 10), nil
}
