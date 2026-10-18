package update

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func lock(ctx context.Context, path string) (func(), error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	for {
		h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err == nil {
			return func() { windows.CloseHandle(h) }, nil
		}
		if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
func hideWindow(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200, HideWindow: true}
}

// Windows does not support fsync on directory handles. The journal file itself
// is flushed before rename; package recovery journals are Linux-only.
func syncDirectory(string) error { return nil }
