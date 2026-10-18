package update

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBootstrapFirstInstallAndValidation(t *testing.T) {
	for _, kind := range []string{"first", "checksum", "directory", "unknown", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "download.exe")
			target := filepath.Join(root, "koto.exe")
			os.WriteFile(source, []byte("verified new executable"), 0755)
			digest, _ := fileHash(source)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "checksum":
				digest = strings.Repeat("0", 64)
			case "directory":
				os.Mkdir(target, 0700)
			case "unknown":
				os.WriteFile(target, []byte("unrelated file"), 0600)
			case "cancelled":
				cancel()
			}
			err := InstallCLI(ctx, source, target, "1.1.0", digest)
			if kind == "first" {
				if err != nil {
					t.Fatal(err)
				}
				if hash, _ := fileHash(target); hash != digest {
					t.Fatal("wrong installation")
				}
			} else if err == nil {
				t.Fatal("invalid installation accepted")
			}
			if kind == "unknown" {
				b, _ := os.ReadFile(target)
				if string(b) != "unrelated file" {
					t.Fatal("unrelated file overwritten")
				}
			}
			stages, _ := filepath.Glob(filepath.Join(root, ".koto-update-*"))
			if len(stages) != 0 {
				t.Fatal("staging remains", stages)
			}
		})
	}
}

func TestBootstrapSharesUpdateLock(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "new.exe")
	target := filepath.Join(root, "koto.exe")
	os.WriteFile(source, []byte("payload"), 0755)
	digest, _ := fileHash(source)
	unlock, err := lock(context.Background(), filepath.Join(root, ".koto-update.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := InstallCLI(ctx, source, target, "1.0.0", digest); err == nil {
		t.Fatal("installer raced an update holding the lock")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("blocked installer changed target")
	}
}

func TestBootstrapOldBetaAndDowngrade(t *testing.T) {
	exe, _ := os.Executable()
	image, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	for _, current := range []string{"0.1.0-beta.2", "2.0.0"} {
		t.Run(current, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "download.exe")
			target := filepath.Join(root, "koto.exe")
			os.WriteFile(source, []byte("verified stable executable"), 0755)
			os.WriteFile(target, image, 0755)
			os.WriteFile(target+".version", []byte(current), 0600)
			t.Setenv("KOTO_VERSION_TEST_PROCESS", "1")
			digest, _ := fileHash(source)
			before, _ := fileHash(target)
			err := InstallCLI(context.Background(), source, target, "0.1.0", digest)
			after, _ := fileHash(target)
			if current == "2.0.0" {
				if err == nil || after != before {
					t.Fatal("installer downgraded newer installation", err)
				}
			} else if err != nil || after != digest {
				t.Fatal("old beta did not upgrade", err)
			}
		})
	}
}
