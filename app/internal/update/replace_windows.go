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
	buffer, err := renameBuffer(to)
	if err != nil {
		return err
	}
	if err = windows.SetFileInformationByHandle(h, windows.FileRenameInfoEx, &buffer[0], uint32(len(buffer))); err != nil {
		return &os.LinkError{Op: "atomic replace", Old: from, New: to, Err: err}
	}
	return nil
}

type renameInfo struct {
	Flags  uint32
	Root   windows.Handle
	Length uint32
	Name   [1]uint16
}

func renameBuffer(to string) ([]byte, error) {
	name, err := windows.UTF16FromString(to)
	if err != nil {
		return nil, err
	}
	var info renameInfo
	// FILE_RENAME_INFO.FileName is NUL-terminated for the Win32 API, while
	// FileNameLength counts only the preceding UTF-16 bytes. Reserve both the
	// complete structure and the terminator so path conversion never reads
	// beyond our buffer. https://learn.microsoft.com/windows/win32/api/winbase/ns-winbase-file_rename_info
	buffer := make([]byte, max(int(unsafe.Sizeof(info)), int(unsafe.Offsetof(info.Name))+2*len(name)))
	r := (*renameInfo)(unsafe.Pointer(&buffer[0]))
	r.Flags = windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS
	r.Length = uint32(2 * (len(name) - 1))
	copy(unsafe.Slice(&r.Name[0], len(name)), name)
	return buffer, nil
}

func exchangeDirectories(from, to string) error {
	return fmt.Errorf("atomic directory updates are not supported on Windows")
}
