package engine

import (
	"fmt"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func fileChangeMeta(f *os.File, _ os.FileInfo) (string, time.Time, bool) {
	h := windows.Handle(f.Fd())
	var id windows.ByHandleFileInformation
	var basic struct {
		Creation, Access, Write, Change int64
		Attributes                      uint32
	}
	if windows.GetFileInformationByHandle(h, &id) != nil || windows.GetFileInformationByHandleEx(h, windows.FileBasicInfo, (*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic))) != nil {
		return "", time.Time{}, false
	}
	changed := time.Unix(0, (basic.Change-116444736000000000)*100)
	return fmt.Sprintf("%d:%d:%d:%d", id.VolumeSerialNumber, id.FileIndexHigh, id.FileIndexLow, basic.Change), changed, true
}
