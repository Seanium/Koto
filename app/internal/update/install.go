package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Location is the actual executable/bundle. Resolving a CLI symlink preserves
// command shims instead of replacing them or installing a second PATH entry.
type Location struct {
	Executable, Target string
	Product            Product
}

func Locate(product Product) (Location, error) {
	p, err := os.Executable()
	if err != nil {
		return Location{}, err
	}
	return LocatePath(p, product, runtime.GOOS)
}

func LocatePath(executable string, product Product, goos string) (Location, error) {
	p, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return Location{}, err
	}
	p, err = filepath.Abs(p)
	if err != nil {
		return Location{}, err
	}
	l := Location{Executable: p, Target: p, Product: product}
	if product == App && goos == "darwin" {
		if filepath.Base(filepath.Dir(p)) != "MacOS" || filepath.Base(filepath.Dir(filepath.Dir(p))) != "Contents" {
			return Location{}, fmt.Errorf("update the installed koto.app, not a development executable")
		}
		l.Target = filepath.Dir(filepath.Dir(filepath.Dir(p)))
		if !strings.HasSuffix(l.Target, ".app") {
			return Location{}, fmt.Errorf("not an application bundle")
		}
		if strings.HasPrefix(l.Target, "/Volumes/") {
			return Location{}, fmt.Errorf("move koto to Applications before updating")
		}
	}
	return l, nil
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Prepared owns one verified download next to its destination. Close removes
// staging files; the previous version is kept only until replacement succeeds.
type Prepared struct {
	Location                    Location
	Version                     string
	dir, payload, before, after string
	beforeTarget, afterTarget   string
	unlock                      func()
	Backup                      string
	Package                     *PackagePrepared
	helper                      string
	helperDigest                string
}

func (p *Prepared) Close() {
	if p.unlock != nil {
		p.unlock()
		p.unlock = nil
	}
	if p.Backup != "" || (p.Package != nil && p.Package.NeedsRecovery) {
		return
	}
	if p.dir != "" {
		cleanupStage(p.dir, p.Location.Target)
		p.dir = ""
	}
}

func (c *Client) Prepare(ctx context.Context, result *Result, location Location, progress func(int64, int64)) (*Prepared, error) {
	if result == nil {
		return nil, fmt.Errorf("check for an update first")
	}
	if !result.Available || !Newer(result.Latest, result.Current) {
		return nil, fmt.Errorf("no newer version is available")
	}
	if result.Product != location.Product {
		return nil, fmt.Errorf("update product mismatch")
	}
	name, err := AssetName(location.Product, runtime.GOOS, runtime.GOARCH)
	if err != nil || result.Asset.Name != name {
		return nil, fmt.Errorf("update platform mismatch")
	}
	installed, err := DetectPackage(ctx, location)
	if err != nil {
		return nil, err
	}
	if installed != nil {
		return c.prepareInstalledPackage(ctx, result, location, installed, progress)
	}
	parent := filepath.Dir(location.Target)
	unlock, err := lock(ctx, filepath.Join(parent, ".koto-update.lock"))
	if err != nil {
		return nil, fmt.Errorf("cannot update in %s: %w", parent, err)
	}
	p := &Prepared{Location: location, Version: result.Latest, unlock: unlock}
	cleanPrevious(location)
	ok := false
	defer func() {
		if !ok {
			p.Close()
		}
	}()
	p.before, err = fileHash(location.Executable)
	if err != nil {
		return nil, err
	}
	// The checking process may still be an older version after another process
	// has updated this path. The file lock alone cannot detect that stale check.
	if err = verifyInstalledVersion(ctx, location, result.Current, result.Latest); err != nil {
		return nil, err
	}
	if err = checkPortableRecovery(location); err != nil {
		return nil, err
	}
	p.dir, err = os.MkdirTemp(parent, ".koto-update-")
	if err != nil {
		return nil, err
	}
	download, err := c.Download(ctx, result, p.dir, progress)
	if err != nil {
		return nil, err
	}
	p.payload = download
	if runtime.GOOS == "darwin" && location.Product == App {
		p.payload, err = extractMac(ctx, download, p.dir)
		if err != nil {
			return nil, err
		}
	} else if err = os.Chmod(p.payload, 0755); err != nil {
		return nil, err
	}
	trial := p.payload
	if runtime.GOOS == "darwin" && location.Product == App {
		trial = filepath.Join(p.payload, "Contents", "MacOS", "koto")
	}
	actual, err := executableVersion(ctx, trial, location.Product)
	if err != nil {
		return nil, fmt.Errorf("new version cannot run on this machine: %w", err)
	}
	if actual != strings.TrimPrefix(result.Latest, "v") {
		return nil, fmt.Errorf("downloaded executable reports an unexpected version")
	}
	ok = true
	return p, nil
}

// Install preserves a recovery copy and journal before one atomic replacement.
// Calls are serialized by the OS file lock, including independent app processes.
func (p *Prepared) Install() error { return p.InstallContext(context.Background()) }

func (p *Prepared) InstallContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.Package != nil {
		return p.installPackage(ctx)
	}
	current, err := fileHash(p.Location.Executable)
	if err != nil {
		return err
	}
	if current != p.before {
		return fmt.Errorf("koto changed during the update; check again")
	}
	backup, err := os.MkdirTemp(filepath.Dir(p.Location.Target), ".koto-previous-")
	if err != nil {
		return err
	}
	p.Backup = filepath.Join(backup, filepath.Base(p.Location.Target))
	info, err := os.Lstat(p.Location.Target)
	if err != nil {
		p.discardBackup()
		return err
	}
	trial := p.payload
	if info.IsDir() {
		rel, e := filepath.Rel(p.Location.Target, p.Location.Executable)
		if e != nil {
			p.discardBackup()
			return e
		}
		trial = filepath.Join(p.payload, rel)
	}
	p.after, err = fileHash(trial)
	if err != nil {
		p.discardBackup()
		return err
	}
	p.beforeTarget, err = targetHash(p.Location.Target)
	if err == nil {
		p.afterTarget, err = targetHash(p.payload)
	}
	if err != nil {
		p.discardBackup()
		return err
	}
	if info.IsDir() {
		// The new bundle is staged at the future backup path. Atomic exchange
		// leaves the old bundle there without ever removing the installed path.
		err = os.Rename(p.payload, p.Backup)
	} else {
		// Windows permits replacing a mapped image's name only while another
		// hard link retains that image. A byte copy does not provide this lease.
		if runtime.GOOS == "windows" {
			err = os.Link(p.Location.Target, p.Backup)
		} else {
			err = copyExecutable(p.Location.Target, p.Backup)
		}
	}
	if err != nil {
		p.discardBackup()
		return err
	}
	// Detect an external (non-koto) installer replacing the path while the
	// rollback copy was being made, before committing any filesystem change.
	if hash, e := fileHash(p.Location.Executable); e != nil || hash != p.before {
		p.discardBackup()
		return fmt.Errorf("koto changed while preparing recovery; check again")
	}
	// Keep a runnable old recovery helper independent of both names involved in
	// the swap. Recovery must not depend on a broken new executable starting.
	p.helper = filepath.Join(p.dir, "helper"+filepath.Ext(p.Location.Executable))
	helperSource := p.Location.Executable
	p.helperDigest = p.before
	if p.Location.Product == CLI {
		helperSource = p.payload
		p.helperDigest = p.after
	}
	if err = copyExecutable(helperSource, p.helper); err != nil {
		p.discardBackup()
		return err
	}
	if hash, e := fileHash(p.helper); e != nil || hash != p.helperDigest {
		p.discardBackup()
		return fmt.Errorf("installed executable changed while saving recovery helper")
	}
	job := restartJob{Parent: os.Getpid(), Location: p.Location, Backup: p.Backup, Version: p.Version, Digest: p.after, PreviousDigest: p.before, PreviousTargetDigest: p.beforeTarget, TargetDigest: p.afterTarget, Helper: p.helper, HelperDigest: p.helperDigest}
	if err = syncDirectory(backup); err == nil {
		err = writeRestartRecord(filepath.Join(p.dir, "restart.json"), job)
	}
	if err == nil {
		err = syncDirectory(filepath.Dir(p.Location.Target))
	}
	if err != nil {
		p.discardBackup()
		return err
	}
	if err = ctx.Err(); err != nil {
		return errors.Join(err, p.Rollback())
	}
	if info.IsDir() {
		err = exchangeDirectories(p.Backup, p.Location.Target)
	} else {
		err = replace(p.payload, p.Location.Target)
	}
	if err == nil {
		err = syncDirectory(filepath.Dir(p.Location.Target))
	}
	if err != nil {
		return errors.Join(err, p.Rollback())
	}
	return nil
}

func (p *Prepared) Rollback() error {
	// On Windows, an exited process or virus scanner may briefly retain an
	// executable mapping. Retry the entire validation, not just the rename:
	// an independent replacement during the wait must never be overwritten.
	return retryFileAccess(p.rollback)
}

func (p *Prepared) rollback() error {
	if p.Package != nil {
		return p.Package.Rollback(context.Background())
	}
	if p.Backup == "" {
		return nil
	}
	current, err := fileHash(p.Location.Executable)
	before, after := p.before, p.after
	if p.beforeTarget != "" {
		current, err = targetHash(p.Location.Target)
		before, after = p.beforeTarget, p.afterTarget
	}
	if err == nil && before != "" && before != after && current == before {
		p.discardBackup()
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && after != "" && current != after {
		return fmt.Errorf("installed executable changed independently; refusing rollback")
	}
	rel, err := filepath.Rel(p.Location.Target, p.Location.Executable)
	if err != nil {
		return err
	}
	old := p.Backup
	if rel != "." {
		old = filepath.Join(p.Backup, rel)
	}
	if hash, e := fileHash(old); e != nil || (p.before != "" && hash != p.before) {
		return fmt.Errorf("previous executable changed; recovery files retained at %s", p.Backup)
	}
	if p.beforeTarget != "" {
		if hash, e := targetHash(p.Backup); e != nil || hash != p.beforeTarget {
			return fmt.Errorf("previous application changed; recovery files retained at %s", p.Backup)
		}
	}
	info, err := os.Lstat(p.Location.Target)
	if err == nil && info.IsDir() {
		err = exchangeDirectories(p.Backup, p.Location.Target)
	} else if err == nil || os.IsNotExist(err) {
		err = replace(p.Backup, p.Location.Target)
	}
	if err != nil {
		return fmt.Errorf("cannot restore previous version from %s: %w", p.Backup, err)
	}
	if err = syncDirectory(filepath.Dir(p.Location.Target)); err != nil {
		return err
	}
	p.discardBackup()
	return nil
}

func (p *Prepared) discardBackup() {
	if p.Backup != "" {
		_ = os.RemoveAll(filepath.Dir(p.Backup))
	}
	_ = os.Remove(filepath.Join(p.dir, "restart.json"))
	p.Backup = ""
}

func (p *Prepared) Commit() error {
	if p.Package != nil {
		p.Package.NeedsRecovery = false
		return nil
	}
	if p.Backup != "" {
		dir := filepath.Dir(p.Backup)
		b, _ := json.Marshal(map[string]string{"target": p.Location.Target, "stage": p.dir})
		// Windows keeps the running old EXE mapped. Retain a small ownership
		// marker until a later invocation can remove it; no history is kept.
		if err := writeJournal(filepath.Join(dir, "committed.json"), b); err != nil {
			return err
		}
		if _, err := retireCommittedJournals(p.Location, dir, p.dir); err != nil {
			return err
		}
		cleanPrevious(p.Location)
		p.Backup = ""
	}
	return nil
}

// These checks run under the update lock, against the actual path on disk.
func verifyInstalledVersion(ctx context.Context, l Location, checked, next string) error {
	actual, err := executableVersion(ctx, l.Executable, l.Product)
	if err != nil {
		return fmt.Errorf("cannot verify installed version: %w", err)
	}
	if actual != strings.TrimPrefix(checked, "v") || !Newer(next, actual) {
		return fmt.Errorf("installed koto is %s; update check was for %s; check again", actual, checked)
	}
	return nil
}

type versionOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *versionOutput) Write(v []byte) (int, error) {
	n := len(v)
	room := 1024 - b.Len()
	if n > room {
		b.overflow = true
		v = v[:room]
	}
	_, _ = b.Buffer.Write(v)
	return n, nil
}
func executableVersion(ctx context.Context, path string, product Product) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	arg := "version"
	if product == App {
		arg = "--koto-update-version"
	}
	cmd := exec.CommandContext(ctx, path, arg)
	hideWindow(cmd)
	var out versionOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", err
	}
	v, ok := strings.CutPrefix(strings.TrimSpace(out.String()), "koto ")
	if out.overflow || !ok || !Released(v) {
		return "", fmt.Errorf("invalid executable version output")
	}
	return strings.TrimPrefix(v, "v"), nil
}

func copyExecutable(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("executable is not a regular file")
	}
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, out.Sync(), out.Close())
}

func cleanPrevious(location Location) {
	dirs, _ := updateDirectories(filepath.Dir(location.Target), ".koto-previous-")
	for _, dir := range dirs {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		marker := filepath.Join(dir, "committed.json")
		f, err := os.Open(marker)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(f, 4096))
		f.Close()
		var receipt map[string]string
		if json.Unmarshal(b, &receipt) != nil || receipt["target"] != location.Target {
			continue
		}
		// committed.json is the commit decision. After a crash immediately
		// following that write, retire its restart journal BEFORE the backup.
		// Otherwise recovery would be left pointing at an already deleted file.
		stages, err := retireCommittedJournals(location, dir, receipt["stage"])
		if err != nil {
			continue
		}
		if os.RemoveAll(filepath.Join(dir, filepath.Base(location.Target))) == nil {
			os.Remove(marker)
			os.Remove(dir)
			for _, stage := range stages {
				cleanupStage(stage, location.Target)
			}
		}
	}
	stages, _ := updateDirectories(filepath.Dir(location.Target), ".koto-update-")
	for _, dir := range stages {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		marker := filepath.Join(dir, "cleanup.json")
		info, err = os.Stat(marker)
		if err != nil || info.Size() > 4096 {
			continue
		}
		b, err := os.ReadFile(marker)
		var receipt map[string]string
		if err == nil && json.Unmarshal(b, &receipt) == nil && receipt["target"] == location.Target {
			cleanupStage(dir, location.Target)
		}
	}
}

// CleanPrevious removes committed backups on a later start, after Windows has
// released the old mapped executable. Uncommitted recovery copies are retained.
func CleanPrevious(product Product) {
	l, err := Locate(product)
	if err == nil {
		// Startup/version probes must not interfere with an active updater.
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		unlock, e := lock(ctx, filepath.Join(filepath.Dir(l.Target), ".koto-update.lock"))
		if e != nil {
			return
		}
		defer unlock()
		cleanPrevious(l)
	}
}

func cleanupStage(dir, target string) {
	if err := os.RemoveAll(dir); err != nil {
		// Windows may retain a mapped helper briefly after process exit.
		b, _ := json.Marshal(map[string]string{"target": target})
		_ = os.WriteFile(filepath.Join(dir, "cleanup.json"), b, 0600)
	}
}

func retireCommittedJournals(l Location, backupDir, recordedStage string) ([]string, error) {
	parent := filepath.Dir(l.Target)
	dirs, err := updateDirectories(parent, ".koto-update-")
	if err != nil {
		return nil, err
	}
	stages := []string{}
	for _, dir := range dirs {
		path := filepath.Join(dir, "restart.json")
		stageInfo, e := os.Lstat(filepath.Dir(path))
		if e != nil || !stageInfo.IsDir() || stageInfo.Mode()&os.ModeSymlink != 0 {
			continue
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<10 {
			continue
		}
		b, err := os.ReadFile(path)
		var job restartJob
		if err != nil || json.Unmarshal(b, &job) != nil || job.Location != l || job.Backup != filepath.Join(backupDir, filepath.Base(l.Target)) {
			continue
		}
		if err = os.Remove(path); err != nil {
			return nil, err
		}
		stage := filepath.Dir(path)
		if err = syncDirectory(stage); err != nil {
			return nil, err
		}
		stages = append(stages, stage)
	}
	if recordedStage != "" && filepath.Dir(recordedStage) == parent && strings.HasPrefix(filepath.Base(recordedStage), ".koto-update-") {
		if info, err := os.Lstat(recordedStage); err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			if _, err := os.Lstat(filepath.Join(recordedStage, "restart.json")); os.IsNotExist(err) {
				stages = append(stages, recordedStage)
			}
		}
	}
	return stages, nil
}

// Only the generated child name is matched. The installation directory may
// contain literal glob characters, such as brackets in a Windows user path.
func updateDirectories(parent, prefix string) ([]string, error) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) {
			dirs = append(dirs, filepath.Join(parent, entry.Name()))
		}
	}
	return dirs, nil
}
