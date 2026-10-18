package update

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestMacImageFailureUnmounts(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "not-an-app"), []byte("invalid package"), 0600); err != nil {
		t.Fatal(err)
	}
	dmg := filepath.Join(root, "invalid.dmg")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "/usr/bin/hdiutil", "create", "-srcfolder", source, "-format", "UDZO", dmg).CombinedOutput(); err != nil {
		t.Fatalf("create fixture image: %v: %s", err, out)
	}
	stage := filepath.Join(root, "stage")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := extractMac(ctx, dmg, stage); err == nil {
		t.Fatal("accepted an image without koto.app")
	}
	mounts, err := filepath.Glob(filepath.Join(root, ".koto-mount-*"))
	if err != nil || len(mounts) != 0 {
		t.Fatalf("update mount remains: %v (%v)", mounts, err)
	}
}

func TestMacPublishedImageExtraction(t *testing.T) {
	dmg := os.Getenv("KOTO_UPDATE_TEST_DMG")
	if dmg == "" {
		t.Skip("set KOTO_UPDATE_TEST_DMG to a packaged koto disk image")
	}
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	app, err := extractMac(ctx, dmg, stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(app, "Contents", "MacOS", "koto")); err != nil {
		t.Fatal(err)
	}
	mounts, _ := filepath.Glob(filepath.Join(root, ".koto-mount-*"))
	if len(mounts) != 0 {
		t.Fatalf("update mount remains: %v", mounts)
	}
}
