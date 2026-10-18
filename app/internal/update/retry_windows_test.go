package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestRollbackRetriesLockedBackupAndRevalidatesDestination(t *testing.T) {
	for _, external := range []bool{false, true} {
		name := "released"
		if external {
			name = "independent-change"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "koto.exe")
			backupDir, err := os.MkdirTemp(root, ".koto-previous-")
			if err != nil {
				t.Fatal(err)
			}
			stage, err := os.MkdirTemp(root, ".koto-update-")
			if err != nil {
				t.Fatal(err)
			}
			backup := filepath.Join(backupDir, "koto.exe")
			for path, data := range map[string]string{target: "new", backup: "previous"} {
				if err = os.WriteFile(path, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := fileHash(backup)
			after, _ := fileHash(target)
			p := &Prepared{Location: Location{Executable: target, Target: target, Product: App}, Backup: backup, dir: stage, before: before, after: after}
			path, _ := windows.UTF16PtrFromString(backup)
			// Permit integrity reads while denying the DELETE access used by
			// the atomic replacement; this deterministically models the lock.
			handle, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := p.rollback(); err == nil {
				windows.CloseHandle(handle)
				t.Fatal("lock did not prevent the first atomic rollback")
			}
			done := make(chan error, 1)
			go func() { done <- p.Rollback() }()
			select {
			case err := <-done:
				windows.CloseHandle(handle)
				t.Fatalf("rollback did not wait for the lock: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			if external {
				if err := os.WriteFile(target, []byte("independent installer"), 0600); err != nil {
					windows.CloseHandle(handle)
					t.Fatal(err)
				}
			}
			windows.CloseHandle(handle)
			select {
			case err := <-done:
				actual, _ := os.ReadFile(target)
				if external {
					if err == nil || !strings.Contains(err.Error(), "independently") || string(actual) != "independent installer" {
						t.Fatalf("overwrote independent update: %q %v", actual, err)
					}
					if _, err := os.Stat(backup); err != nil {
						t.Fatal("recovery backup lost", err)
					}
				} else if err != nil || string(actual) != "previous" {
					t.Fatalf("rollback failed after releasing lock: %q %v", actual, err)
				}
			case <-time.After(7 * time.Second):
				t.Fatal("rollback retry did not finish")
			}
		})
	}
}
