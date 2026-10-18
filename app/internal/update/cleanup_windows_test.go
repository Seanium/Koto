package update

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestCleanupRetainsMarkerUntilHelperIsReleased(t *testing.T) {
	root := t.TempDir()
	stage, err := os.MkdirTemp(root, ".koto-update-")
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(stage, "helper.exe")
	if err := os.WriteFile(helper, []byte("mapped helper"), 0600); err != nil {
		t.Fatal(err)
	}
	path, _ := windows.UTF16PtrFromString(helper)
	h, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if h != 0 {
			windows.CloseHandle(h)
		}
	}()
	l := Location{Target: filepath.Join(root, "koto.exe"), Executable: filepath.Join(root, "koto.exe"), Product: CLI}
	cleanupStage(stage, l.Target)
	for range 3 {
		cleanPrevious(l)
		if _, err := os.Stat(filepath.Join(stage, "cleanup.json")); err != nil {
			t.Fatal("failed cleanup discarded its retry marker", err)
		}
	}
	windows.CloseHandle(h)
	h = 0
	cleanPrevious(l)
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatal("released helper was not cleaned", err)
	}
}
