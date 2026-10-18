package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"koto/internal/platform"
)

func packageUpdateParent(l Location) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	key := sha256.Sum256([]byte(l.Target))
	parent := filepath.Join(cache, "koto", "updates", hex.EncodeToString(key[:16]))
	if err = os.MkdirAll(parent, 0700); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(parent)
}

func (c *Client) prepareInstalledPackage(ctx context.Context, result *Result, l Location, installed *PackageInstallation, progress func(int64, int64)) (_ *Prepared, err error) {
	parent, err := packageUpdateParent(l)
	if err != nil {
		return nil, err
	}
	unlock, err := lock(ctx, filepath.Join(parent, ".koto-update.lock"))
	if err != nil {
		return nil, err
	}
	p := &Prepared{Location: l, Version: result.Latest, unlock: unlock}
	ready := false
	defer func() {
		if !ready {
			p.Close()
		}
	}()
	if err = checkPackageRecovery(parent); err != nil {
		return nil, err
	}
	// DetectPackage ran before waiting for the lock. Re-read the package and
	// executable now so another updater cannot turn a stale check into a downgrade.
	installed, err = DetectPackage(ctx, l)
	if err != nil {
		return nil, err
	}
	if installed == nil || strings.Replace(installed.Version, "~", "-", 1) != strings.TrimPrefix(result.Current, "v") {
		return nil, fmt.Errorf("installed package changed; check again")
	}
	if err = verifyInstalledVersion(ctx, l, result.Current, result.Latest); err != nil {
		return nil, err
	}
	p.dir, err = os.MkdirTemp(parent, ".koto-update-")
	if err != nil {
		return nil, err
	}
	name := "koto-linux-" + installed.Architecture + ".deb"
	next, err := c.releaseAsset(ctx, result.Tag, result.Current, App, name)
	if err != nil {
		return nil, err
	}
	oldVersion := strings.Replace(installed.Version, "~", "-", 1)
	previous, err := c.releaseAsset(ctx, "v"+oldVersion, result.Current, App, name)
	if err != nil {
		return nil, fmt.Errorf("cannot obtain the installed release for rollback: %w", err)
	}
	p.Package, err = c.PreparePackage(ctx, next, previous, l, p.dir, progress)
	if err != nil {
		return nil, err
	}
	// The old runtime can recover a broken new build without running its GUI.
	p.helper = filepath.Join(p.dir, "helper")
	in, err := os.Open(l.Executable)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	out, err := os.OpenFile(p.helper, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return nil, err
	}
	_, copyErr := io.Copy(out, in)
	syncErr := out.Sync()
	closeErr := out.Close()
	if err = errors.Join(copyErr, syncErr, closeErr); err != nil {
		return nil, err
	}
	if hash, e := fileHash(p.helper); e != nil || hash != p.Package.BeforeSHA256 {
		return nil, fmt.Errorf("installed app changed while preparing recovery")
	}
	ready = true
	return p, nil
}

func writeRestartRecord(path string, job restartJob) error {
	b, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return writeJournal(path, b)
}

func writeJournal(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".journal-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if err = replace(f.Name(), path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

// Never overwrite an interrupted transaction's rollback material with a new
// update. Only the recovery helper may remove its journal after restoration.
func checkPackageRecovery(parent string) error {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), ".koto-update-") {
			continue
		}
		journal := filepath.Join(parent, entry.Name(), "restart.json")
		if _, err := os.Lstat(journal); err == nil {
			return fmt.Errorf("an interrupted package update needs recovery first; recovery record: %s", journal)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// Journal first: a crash during dpkg must not erase the only verified rollback
// package. The saved old helper accepts --koto-update-recover with this record.
func (p *Prepared) installPackage(ctx context.Context) error {
	digest, err := fileHash(p.Package.TrialExecutable)
	if err != nil {
		return err
	}
	saved := *p.Package
	saved.NeedsRecovery = true
	job := restartJob{Parent: os.Getpid(), Location: p.Location, Version: p.Version, Digest: digest, Package: &saved, Helper: p.helper}
	journal := filepath.Join(p.dir, "restart.json")
	if err = writeRestartRecord(journal, job); err != nil {
		return err
	}
	if err = p.Package.Install(ctx); err != nil && p.Package.NeedsRecovery {
		if rollback := p.Package.Rollback(context.Background()); rollback != nil {
			return errors.Join(err, rollback, fmt.Errorf("recovery files retained at %s", p.dir))
		}
	}
	return err
}

func validatePackageRestart(job restartJob, path, executable string) error {
	p := job.Package
	if runtime.GOOS != "linux" || p == nil || p.Location != job.Location || p.Location.Product != App || job.Helper != filepath.Join(filepath.Dir(path), "helper") || executable != job.Helper || filepath.Dir(p.NewDEB) != filepath.Dir(path) || filepath.Dir(p.OldDEB) != filepath.Dir(path) {
		return fmt.Errorf("invalid package recovery record")
	}
	parent, err := packageUpdateParent(job.Location)
	if err != nil || filepath.Dir(filepath.Dir(path)) != parent {
		return fmt.Errorf("package recovery directory mismatch")
	}
	hash, err := fileHash(job.Helper)
	if err != nil || hash != p.BeforeSHA256 {
		return fmt.Errorf("package recovery helper changed")
	}
	if strings.Replace(p.NewVersion, "~", "-", 1) != job.Version {
		return fmt.Errorf("package recovery version mismatch")
	}
	return p.Validate(context.Background())
}

// RunRecovery is only used with the private journal retained after an interrupted
// package update. Rollback still requests authorization through the system UI.
func RunRecovery(path string) error {
	job, err := readRestart(path)
	if err != nil {
		return err
	}
	if job.Package == nil {
		return recoverPortable(path, job)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err = waitProcess(ctx, job.Parent); err != nil {
		return fmt.Errorf("close the previous app before recovery: %w", err)
	}
	parent, err := packageUpdateParent(job.Location)
	if err != nil {
		return err
	}
	unlock, err := lock(ctx, filepath.Join(parent, ".koto-update.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	// A restart helper may have finished while we waited for the lock.
	job, err = readRestart(path)
	if err != nil {
		return err
	}
	if data, e := os.ReadFile(filepath.Join(filepath.Dir(path), "ready")); e == nil {
		var pid int
		if _, e = fmt.Sscan(string(data), &pid); e == nil && pid > 0 && platform.ProcessAlive(pid) {
			return fmt.Errorf("close the updated app before recovery")
		}
	}
	if err = job.Package.Rollback(ctx); err != nil {
		return err
	}
	p := &Prepared{Location: job.Location, Package: job.Package, dir: filepath.Dir(path)}
	p.Close()
	return nil
}
