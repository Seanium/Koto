package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func extractMac(ctx context.Context, dmg, dir string) (target string, resultErr error) {
	// Keep the mount outside the payload directory. Even when macOS refuses
	// to detach, staging cleanup must never walk a still-mounted filesystem.
	mount, err := os.MkdirTemp(filepath.Dir(dir), ".koto-mount-")
	if err != nil {
		return "", err
	}
	defer func() {
		// A cancelled attach can still have mounted the image. A directory
		// removal succeeds only when nothing is mounted here.
		if err := os.Remove(mount); err == nil || os.IsNotExist(err) {
			return
		}
		detachCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := exec.CommandContext(detachCtx, "/usr/bin/hdiutil", "detach", "-force", mount).Run(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("cannot unmount update disk image at %s: %w", mount, err))
			return
		}
		if err := os.Remove(mount); err != nil && !os.IsNotExist(err) {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	attachCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := exec.CommandContext(attachCtx, "/usr/bin/hdiutil", "attach", "-readonly", "-nobrowse", "-mountpoint", mount, dmg).Run(); err != nil {
		return "", fmt.Errorf("cannot open update disk image: %w", err)
	}
	source := filepath.Join(mount, "koto.app")
	id, err := exec.CommandContext(ctx, "/usr/bin/plutil", "-extract", "CFBundleIdentifier", "raw", "-o", "-", filepath.Join(source, "Contents", "Info.plist")).Output()
	if err != nil || strings.TrimSpace(string(id)) != "app.koto.desktop" {
		return "", fmt.Errorf("update disk image does not contain koto.app")
	}
	if err := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", source).Run(); err != nil {
		return "", fmt.Errorf("updated app signature is invalid: %w", err)
	}
	target = filepath.Join(dir, "koto.app")
	if err := exec.CommandContext(ctx, "/usr/bin/ditto", source, target).Run(); err != nil {
		return "", err
	}
	return target, nil
}
