package update

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// replace atomically changes the directory entry, even while the previous EXE
// is mapped. Do not fall back to moving the destination away: a crash between
// two renames would leave the application missing.
func replace(from, to string) error {
	p, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(p, windows.DELETE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	name, err := windows.UTF16FromString(to)
	if err != nil {
		return err
	}
	type renameInfo struct {
		Flags  uint32
		Root   windows.Handle
		Length uint32
		Name   [1]uint16
	}
	var info renameInfo
	n := len(name) - 1
	buffer := make([]byte, int(unsafe.Offsetof(info.Name))+2*n)
	r := (*renameInfo)(unsafe.Pointer(&buffer[0]))
	r.Flags = windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS
	r.Length = uint32(2 * n)
	copy(unsafe.Slice(&r.Name[0], n), name[:n])
	if err = windows.SetFileInformationByHandle(h, windows.FileRenameInfoEx, &buffer[0], uint32(len(buffer))); err != nil {
		return &os.LinkError{Op: "atomic replace", Old: from, New: to, Err: err}
	}
	return nil
}

func exchangeDirectories(from, to string) error {
	return fmt.Errorf("atomic directory updates are not supported on Windows")
}
