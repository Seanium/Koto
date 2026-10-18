//go:build linux

package update

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// PackageInstallation describes the package owning the actual running file.
// Version is the Debian version (for example 0.1.0~beta.2), not a release tag.
type PackageInstallation struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
	Executable   string `json:"executable"`
}

// PackagePrepared contains only temporary verified archives and a trial binary.
// The application is always installed/rolled back with dpkg, never by moving its
// files. Keep these files while NeedsRecovery is true, including after an error.
type PackagePrepared struct {
	Location        Location            `json:"location"`
	Installed       PackageInstallation `json:"installed"`
	NewVersion      string              `json:"newVersion"`
	NewDEB          string              `json:"newDEB"`
	OldDEB          string              `json:"oldDEB"`
	NewSHA256       string              `json:"newSHA256"`
	OldSHA256       string              `json:"oldSHA256"`
	BeforeSHA256    string              `json:"beforeSHA256"`
	TrialExecutable string              `json:"trialExecutable"`
	NeedsRecovery   bool                `json:"needsRecovery"`
	run             packageRunner
}

type packageRunner func(context.Context, string, ...string) ([]byte, error)

// DetectPackage returns nil for an ordinary executable. An executable owned by
// another package, an ambiguous owner, or a damaged dpkg state is not portable.
func DetectPackage(ctx context.Context, l Location) (*PackageInstallation, error) {
	return detectPackage(ctx, l, runPackageCommand)
}
func detectPackage(ctx context.Context, l Location, run packageRunner) (*PackageInstallation, error) {
	if l.Product != App {
		return nil, nil
	}
	if !filepath.IsAbs(l.Executable) || strings.ContainsAny(l.Executable, "\n\r\x00") {
		return nil, fmt.Errorf("invalid executable path")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := run(ctx, "/usr/bin/dpkg-query", "--admindir=/var/lib/dpkg", "--search", "--", escapeDpkgPattern(l.Executable))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || commandExit(err) == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot determine package ownership: %w", err)
	}
	owners := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		at := strings.LastIndex(line, ": ")
		if at < 0 || line[at+2:] != l.Executable {
			continue
		}
		for _, name := range strings.Split(line[:at], ", ") {
			owners[name] = true
		}
	}
	if len(owners) == 0 {
		return nil, nil
	}
	if len(owners) != 1 {
		return nil, fmt.Errorf("executable has ambiguous package ownership")
	}
	for owner := range owners {
		if owner != "koto" && owner != "koto:"+runtime.GOARCH {
			return nil, fmt.Errorf("executable belongs to %s, not the koto package", owner)
		}
	}
	installed, status, err := queryPackage(ctx, run)
	if err != nil {
		return nil, err
	}
	if status != "install ok installed" {
		return nil, fmt.Errorf("koto package is not in a clean installed state: %s", status)
	}
	if installed.Name != "koto" || installed.Architecture != runtime.GOARCH {
		return nil, fmt.Errorf("installed koto package architecture mismatch")
	}
	installed.Executable = l.Executable
	return installed, nil
}
func escapeDpkgPattern(path string) string {
	return strings.NewReplacer(`\`, `\\`, "*", `\*`, "?", `\?`, "[", `\[`).Replace(path)
}
func queryPackage(ctx context.Context, run packageRunner) (*PackageInstallation, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := run(ctx, "/usr/bin/dpkg-query", "--admindir=/var/lib/dpkg", "--show", "--showformat=${Package}\t${Version}\t${Architecture}\t${Status}\n", "koto")
	if err != nil {
		return nil, "", fmt.Errorf("cannot read installed koto package: %w", err)
	}
	fields := strings.Split(strings.TrimSuffix(string(out), "\n"), "\t")
	if len(fields) != 4 || strings.ContainsAny(strings.Join(fields, ""), "\n\r") || fields[0] != "koto" || fields[1] == "" {
		return nil, "", fmt.Errorf("invalid koto package status")
	}
	return &PackageInstallation{Name: fields[0], Version: fields[1], Architecture: fields[2]}, fields[3], nil
}
func debVersion(version string) (string, error) {
	if !Released(version) {
		return "", fmt.Errorf("invalid release version")
	}
	return strings.Replace(strings.TrimPrefix(version, "v"), "-", "~", 1), nil
}

// PreparePackage downloads both releases before requesting privileges. The old
// release must exactly match the installed Debian version; missing rollback
// material is a hard failure. stageDir must be private and user-owned.
func (c *Client) PreparePackage(ctx context.Context, next, previous *Result, l Location, stageDir string, progress func(int64, int64)) (*PackagePrepared, error) {
	return c.preparePackage(ctx, next, previous, l, stageDir, progress, runPackageCommand)
}
func (c *Client) preparePackage(ctx context.Context, next, previous *Result, l Location, stageDir string, progress func(int64, int64), run packageRunner) (p *PackagePrepared, resultErr error) {
	installed, err := detectPackage(ctx, l, run)
	if err != nil {
		return nil, err
	}
	if installed == nil {
		return nil, fmt.Errorf("the executable is not installed by the koto package")
	}
	if next == nil || previous == nil || next.Product != App || previous.Product != App || !next.Available || !Newer(next.Latest, next.Current) {
		return nil, fmt.Errorf("invalid package update releases")
	}
	current, err := debVersion(next.Current)
	if err != nil {
		return nil, err
	}
	old, err := debVersion(previous.Latest)
	if err != nil {
		return nil, err
	}
	newVersion, err := debVersion(next.Latest)
	if err != nil {
		return nil, err
	}
	if current != installed.Version || old != installed.Version {
		return nil, fmt.Errorf("rollback release does not match installed package version")
	}
	name := "koto-linux-" + installed.Architecture + ".deb"
	if next.Asset.Name != name || previous.Asset.Name != name {
		return nil, fmt.Errorf("package update asset/architecture mismatch")
	}
	if err = privateStage(stageDir); err != nil {
		return nil, err
	}
	p = &PackagePrepared{Location: l, Installed: *installed, NewVersion: newVersion, run: run}
	p.BeforeSHA256, err = fileHash(l.Executable)
	if err != nil {
		return nil, err
	}
	prepared := p
	defer func() {
		if resultErr != nil {
			p := prepared
			if p.NewDEB != "" {
				os.Remove(p.NewDEB)
			}
			if p.OldDEB != "" {
				os.Remove(p.OldDEB)
			}
			if p.TrialExecutable != "" {
				os.RemoveAll(filepath.Join(stageDir, "trial"))
			}
		}
	}()
	p.NewDEB, err = c.Download(ctx, next, stageDir, progress)
	if err != nil {
		return nil, err
	}
	p.OldDEB, err = c.Download(ctx, previous, stageDir, nil)
	if err != nil {
		return nil, err
	}
	p.NewSHA256, err = fileHash(p.NewDEB)
	if err != nil {
		return nil, err
	}
	p.OldSHA256, err = fileHash(p.OldDEB)
	if err != nil {
		return nil, err
	}
	if err = p.Validate(ctx); err != nil {
		return nil, err
	}
	// Ensure rollback restores these exact bytes, including detecting a locally
	// modified installation before it is replaced. Never execute the old trial.
	oldTrial := filepath.Join(stageDir, "previous")
	defer os.RemoveAll(oldTrial)
	oldExecutable, err := extractPackageExecutable(ctx, run, p.OldDEB, oldTrial, l.Executable)
	if err != nil {
		return nil, err
	}
	oldHash, err := fileHash(oldExecutable)
	if err != nil || oldHash != p.BeforeSHA256 {
		return nil, fmt.Errorf("rollback package does not contain the currently installed executable")
	}
	p.TrialExecutable = filepath.Join(stageDir, "trial", strings.TrimPrefix(l.Executable, string(filepath.Separator)))
	if _, err = extractPackageExecutable(ctx, run, p.NewDEB, filepath.Join(stageDir, "trial"), l.Executable); err != nil {
		return nil, err
	}
	trialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	output, err := run(trialCtx, p.TrialExecutable, "--koto-update-version")
	if err != nil {
		return nil, fmt.Errorf("new package cannot run on this machine: %w", err)
	}
	if strings.TrimSpace(string(output)) != "koto "+strings.TrimPrefix(next.Latest, "v") {
		return nil, fmt.Errorf("new package executable version mismatch")
	}
	return p, nil
}
func extractPackageExecutable(ctx context.Context, run packageRunner, archive, dir, executable string) (string, error) {
	if err := os.Mkdir(dir, 0700); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if _, err := run(ctx, "/usr/bin/dpkg-deb", "--extract", archive, dir); err != nil {
		return "", fmt.Errorf("cannot extract package for compatibility check: %w", err)
	}
	path := filepath.Join(dir, strings.TrimPrefix(executable, string(filepath.Separator)))
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("package does not contain the installed executable")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return "", fmt.Errorf("package trial executable crosses a symbolic link")
	}
	return path, nil
}
func privateStage(dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("package update staging path must be absolute")
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("package update staging directory must be private")
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("package update staging directory belongs to another user")
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil || real != dir {
		return fmt.Errorf("package update staging directory crosses a symbolic link")
	}
	return nil
}
func (p *PackagePrepared) runner() packageRunner {
	if p.run != nil {
		return p.run
	}
	return runPackageCommand
}

// Validate is also required when restoring a restart record. It validates both
// archived packages again immediately before any privileged package operation.
func (p *PackagePrepared) Validate(ctx context.Context) error {
	if p == nil || p.Location.Product != App || p.Installed.Name != "koto" || p.Installed.Architecture != runtime.GOARCH || p.Installed.Executable != p.Location.Executable || !filepath.IsAbs(p.Location.Executable) {
		return fmt.Errorf("invalid package update record")
	}
	before, err := hex.DecodeString(p.BeforeSHA256)
	oldVersion := strings.Replace(p.Installed.Version, "~", "-", 1)
	newVersion := strings.Replace(p.NewVersion, "~", "-", 1)
	if err != nil || len(before) != 32 || !Released(oldVersion) || !Newer(newVersion, oldVersion) || p.Location.Target != p.Location.Executable {
		return fmt.Errorf("invalid package update versions or executable")
	}
	dir := filepath.Dir(p.NewDEB)
	if !filepath.IsAbs(p.NewDEB) || !filepath.IsAbs(p.OldDEB) || p.NewDEB == p.OldDEB || filepath.Dir(p.OldDEB) != dir {
		return fmt.Errorf("package archives must share one staging directory")
	}
	if err := privateStage(dir); err != nil {
		return err
	}
	for _, entry := range []struct{ file, hash, version string }{{p.NewDEB, p.NewSHA256, p.NewVersion}, {p.OldDEB, p.OldSHA256, p.Installed.Version}} {
		digest, e := hex.DecodeString(entry.hash)
		if e != nil || len(digest) != 32 || entry.version == "" {
			return fmt.Errorf("invalid package archive digest/version")
		}
		info, e := os.Lstat(entry.file)
		if e != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("package archive is missing or not a regular file")
		}
		hash, e := fileHash(entry.file)
		if e != nil || hash != entry.hash {
			return fmt.Errorf("package archive changed after download")
		}
		inspectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		output, e := p.runner()(inspectCtx, "/usr/bin/dpkg-deb", "--field", entry.file, "Package", "Version", "Architecture")
		cancel()
		if e != nil {
			return fmt.Errorf("cannot inspect package archive: %w", e)
		}
		fields := map[string]string{}
		for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			key, value, ok := strings.Cut(line, ": ")
			if !ok || fields[key] != "" {
				return fmt.Errorf("invalid package archive metadata")
			}
			fields[key] = value
		}
		if len(fields) != 3 || fields["Package"] != "koto" || fields["Version"] != entry.version || fields["Architecture"] != p.Installed.Architecture {
			return fmt.Errorf("package archive name, version or architecture mismatch")
		}
	}
	return nil
}

// Install does not launch the app. Cancellation before pkexec or cancellation in
// the system authorization dialog leaves the installed package unchanged. Once
// dpkg starts, it is allowed to finish even if the app's context is cancelled.
func (p *PackagePrepared) Install(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.Validate(ctx); err != nil {
		return err
	}
	current, err := detectPackage(ctx, p.Location, p.runner())
	if err != nil {
		return err
	}
	if current == nil || current.Version != p.Installed.Version || current.Architecture != p.Installed.Architecture {
		return fmt.Errorf("installed package changed during the update")
	}
	hash, err := fileHash(p.Location.Executable)
	if err != nil || hash != p.BeforeSHA256 {
		return fmt.Errorf("installed executable changed during the update")
	}
	p.NeedsRecovery = true
	out, err := p.runner()(ctx, "/usr/bin/pkexec", "--disable-internal-agent", "/usr/bin/dpkg", "--admindir=/var/lib/dpkg", "--root=/", "--refuse-downgrade", "--install", "--", p.NewDEB)
	after, status, queryErr := queryPackage(context.Background(), p.runner())
	if err != nil {
		if queryErr == nil && after.Version == p.Installed.Version && status == "install ok installed" {
			if h, e := fileHash(p.Location.Executable); e == nil && h == p.BeforeSHA256 {
				p.NeedsRecovery = false
			}
		}
		if commandExit(err) == 126 {
			return fmt.Errorf("package update authorization was cancelled: %w", err)
		}
		if commandExit(err) == 127 {
			return fmt.Errorf("package update authorization was not granted: %w", err)
		}
		return fmt.Errorf("package installation failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if queryErr != nil || after.Version != p.NewVersion || after.Architecture != p.Installed.Architecture || status != "install ok installed" {
		return fmt.Errorf("package installation did not reach the expected installed state")
	}
	installedHash, err := fileHash(p.Location.Executable)
	trialHash, trialErr := fileHash(p.TrialExecutable)
	if err != nil || trialErr != nil || installedHash != trialHash {
		return fmt.Errorf("installed package executable differs from the verified download")
	}
	return nil
}

// Rollback uses dpkg too, so its package database and application files agree.
// pkexec may ask for administrator authorization again. Failure keeps recovery
// material owned by the caller; callers must not discard it before success.
func (p *PackagePrepared) Rollback(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.Validate(ctx); err != nil {
		return err
	}
	current, status, err := queryPackage(ctx, p.runner())
	if err != nil {
		return err
	}
	if current.Architecture != p.Installed.Architecture || (current.Version != p.NewVersion && current.Version != p.Installed.Version) {
		return fmt.Errorf("package changed independently; refusing to replace it during rollback")
	}
	if current.Version == p.Installed.Version && status == "install ok installed" {
		if h, e := fileHash(p.Location.Executable); e == nil && h == p.BeforeSHA256 {
			p.NeedsRecovery = false
			return nil
		}
	}
	p.NeedsRecovery = true
	out, err := p.runner()(ctx, "/usr/bin/pkexec", "--disable-internal-agent", "/usr/bin/dpkg", "--admindir=/var/lib/dpkg", "--root=/", "--install", "--", p.OldDEB)
	if err != nil {
		return fmt.Errorf("package rollback needs administrator authorization and did not complete: %w: %s", err, strings.TrimSpace(string(out)))
	}
	after, state, err := queryPackage(context.Background(), p.runner())
	if err != nil || after.Version != p.Installed.Version || state != "install ok installed" {
		return fmt.Errorf("package rollback did not restore the installed package state")
	}
	hash, err := fileHash(p.Location.Executable)
	if err != nil || hash != p.BeforeSHA256 {
		return fmt.Errorf("restored package executable differs from the previous installation")
	}
	p.NeedsRecovery = false
	return nil
}
func commandExit(err error) int {
	var exit interface{ ExitCode() int }
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

type cappedPackageOutput struct{ bytes.Buffer }

func (b *cappedPackageOutput) Write(p []byte) (int, error) {
	n := len(p)
	if room := 128*1024 - b.Len(); room > 0 {
		if len(p) > room {
			p = p[:room]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}
func runPackageCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var cmd *exec.Cmd
	if name == "/usr/bin/pkexec" {
		cmd = exec.Command(name, args...)
	} else {
		cmd = exec.CommandContext(ctx, name, args...)
	}
	for _, v := range os.Environ() {
		if strings.HasPrefix(v, "DPKG_") || strings.HasPrefix(v, "LC_ALL=") {
			continue
		}
		cmd.Env = append(cmd.Env, v)
	}
	cmd.Env = append(cmd.Env, "LC_ALL=C")
	var output cappedPackageOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return output.Bytes(), err
}
