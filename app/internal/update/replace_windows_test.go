package update

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestRenameBufferTerminatorAndLayout(t *testing.T) {
	for _, path := range []string{"a", `C:\install [test] 中文\koto.exe`, `C:\🐙\koto.exe`, `\\server\share\koto.exe`} {
		buf, err := renameBuffer(path)
		if err != nil {
			t.Fatal(err)
		}
		info := (*renameInfo)(unsafe.Pointer(&buf[0]))
		name, _ := windows.UTF16FromString(path)
		if uintptr(unsafe.Pointer(info))%unsafe.Alignof(*info) != 0 || len(buf) < int(unsafe.Sizeof(*info)) {
			t.Fatal("unaligned or incomplete FILE_RENAME_INFO")
		}
		if info.Length != uint32(2*(len(name)-1)) || info.Root != 0 || info.Flags != windows.FILE_RENAME_REPLACE_IF_EXISTS|windows.FILE_RENAME_POSIX_SEMANTICS {
			t.Fatalf("incorrect header: %+v", info)
		}
		end := int(unsafe.Offsetof(info.Name)) + int(info.Length)
		if len(buf) < end+2 || !bytes.Equal(buf[end:end+2], []byte{0, 0}) {
			t.Fatal("UTF-16 terminator absent from allocated buffer")
		}
		if got := windows.UTF16ToString(unsafe.Slice(&info.Name[0], len(name))); got != path {
			t.Fatalf("path changed: %q != %q", got, path)
		}
	}
	if _, err := renameBuffer("bad\x00path"); err == nil {
		t.Fatal("embedded NUL accepted")
	}
}

func TestReplaceWindowsUnicodePathLengths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "install [test] 中文 🐙")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 96; n++ {
		from := filepath.Join(root, "payload")
		to := filepath.Join(root, strings.Repeat("a", n)+"中文🐙.exe")
		body := []byte(fmt.Sprintf("replacement %d", n))
		if err := os.WriteFile(from, body, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(to, []byte("previous"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := replace(from, to); err != nil {
			t.Fatalf("path length %d: %v", n, err)
		}
		if got, err := os.ReadFile(to); err != nil || !bytes.Equal(got, body) {
			t.Fatalf("replacement content: %q %v", got, err)
		}
		if _, err := os.Stat(from); !os.IsNotExist(err) {
			t.Fatalf("source remains: %v", err)
		}
		if err := os.Remove(to); err != nil {
			t.Fatal(err)
		}
	}
}
