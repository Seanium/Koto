package update

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// InstallCLI is the verified installer's private entry point. Shell and
// PowerShell pass the checksum they checked, and installation shares the same
// lock and transaction as `koto update`. Beta CLIs need only `version`.
func InstallCLI(ctx context.Context, source, target, release, digest string) error {
	if !Released(release) || !filepath.IsAbs(target) {
		return fmt.Errorf("invalid CLI installation request")
	}
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != 32 {
		return fmt.Errorf("invalid installer checksum")
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("installer executable is not a regular file")
	}
	actual, err := fileHash(source)
	if err != nil || actual != hex.EncodeToString(decoded) {
		return fmt.Errorf("installer executable changed after verification")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(target))
	if err != nil {
		return err
	}
	target = filepath.Join(parent, filepath.Base(target))
	unlock, err := lock(ctx, filepath.Join(parent, ".koto-update.lock"))
	if err != nil {
		return err
	}
	p := &Prepared{Location: Location{target, target, CLI}, Version: release, unlock: unlock}
	defer p.Close()
	cleanPrevious(p.Location)
	if err = checkPortableRecovery(p.Location); err != nil {
		return err
	}
	info, err = os.Lstat(target)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if exists {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("installation target must be a regular executable")
		}
		p.before, err = fileHash(target)
		if err != nil {
			return err
		}
		current, e := executableVersion(ctx, target, CLI)
		if e != nil {
			return fmt.Errorf("existing target is not a runnable koto CLI: %w", e)
		}
		if Newer(current, release) {
			return fmt.Errorf("installed koto %s is newer than requested %s", current, release)
		}
		if hash, e := fileHash(target); e != nil || hash != p.before {
			return fmt.Errorf("installed CLI changed during version verification; retry installation")
		}
		if p.before == actual {
			return nil
		}
	}
	p.dir, err = os.MkdirTemp(parent, ".koto-update-")
	if err != nil {
		return err
	}
	p.payload = filepath.Join(p.dir, "koto"+filepath.Ext(target))
	if err = copyExecutable(source, p.payload); err != nil {
		return err
	}
	if hash, e := fileHash(p.payload); e != nil || hash != actual {
		return fmt.Errorf("installer executable changed while staging")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if exists {
		if err = p.InstallContext(ctx); err != nil {
			return err
		}
		return p.Commit()
	}
	// A hard link publishes the verified file without overwriting a file that
	// appeared after the absence check. Both paths are on this filesystem.
	if err = os.Link(p.payload, target); err != nil {
		return err
	}
	return syncDirectory(parent)
}
