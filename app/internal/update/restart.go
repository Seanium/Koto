package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"koto/internal/platform"
)

const readyEnv = "KOTO_UPDATE_READY"

type restartJob struct {
	Parent               int              `json:"parent"`
	Location             Location         `json:"location"`
	Backup               string           `json:"backup"`
	Version              string           `json:"version"`
	Digest               string           `json:"digest"`
	PreviousDigest       string           `json:"previousDigest,omitempty"`
	PreviousTargetDigest string           `json:"previousTargetDigest,omitempty"`
	TargetDigest         string           `json:"targetDigest,omitempty"`
	Package              *PackagePrepared `json:"package,omitempty"`
	Helper               string           `json:"helper,omitempty"`
	HelperDigest         string           `json:"helperDigest,omitempty"`
}

// Restart transfers cleanup to a short-lived helper. The new window starts only
// after the old process has closed its databases and exited. The helper retains
// the old build until the new webview reports readiness.
func (p *Prepared) Restart() error {
	if (p.Backup == "" && p.Package == nil) || p.Location.Product != App {
		return fmt.Errorf("no installed app update to restart")
	}
	digest, err := fileHash(p.Location.Executable)
	if err != nil {
		return err
	}
	if p.Package == nil {
		if digest != p.after {
			return fmt.Errorf("installed app changed before restart")
		}
		if hash, e := targetHash(p.Location.Target); e != nil || hash != p.afterTarget {
			return fmt.Errorf("installed application bundle changed before restart")
		}
	}
	job := restartJob{Parent: os.Getpid(), Location: p.Location, Backup: p.Backup, Version: p.Version, Digest: digest, PreviousDigest: p.before, PreviousTargetDigest: p.beforeTarget, TargetDigest: p.afterTarget, Package: p.Package, Helper: p.helper, HelperDigest: p.helperDigest}
	path := filepath.Join(p.dir, "restart.json")
	if err = writeRestartRecord(path, job); err != nil {
		return err
	}
	helper := p.Location.Executable
	if p.helper != "" {
		helper = p.helper
	}
	cmd := exec.Command(helper, "--koto-update-restart", path)
	cmd.Env = withoutReady(os.Environ())
	detach(cmd)
	if err = cmd.Start(); err != nil {
		return err
	}
	// Reap it if the old app stays alive briefly; process exit releases ownership.
	go func() { _ = cmd.Wait() }()
	if p.unlock != nil {
		p.unlock()
		p.unlock = nil
	}
	p.dir = ""
	p.Backup = ""
	return nil
}

func withoutReady(env []string) []string {
	out := make([]string, 0, len(env))
	for _, v := range env {
		if !strings.HasPrefix(v, readyEnv+"=") {
			out = append(out, v)
		}
	}
	return out
}

// MarkReady is called when the actual frontend has installed its event handlers.
func MarkReady() {
	path := os.Getenv(readyEnv)
	if path == "" {
		return
	}
	_ = os.Unsetenv(readyEnv)
	if filepath.IsAbs(path) && filepath.Base(path) == "ready" && strings.HasPrefix(filepath.Base(filepath.Dir(path)), ".koto-update-") {
		_ = os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0600)
	}
}

// RunRestart is an internal mode of the app executable, before GUI setup. A
// timeout before parent exit leaves the installed update and its backup intact.
func RunRestart(path string) error {
	job, err := readRestart(path)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err = waitProcess(ctx, job.Parent); err != nil {
		return err
	}
	// Give the new window its own startup deadline after the old process exits.
	startup, finish := context.WithTimeout(context.Background(), 90*time.Second)
	defer finish()
	lockParent := filepath.Dir(job.Location.Target)
	if job.Package != nil {
		lockParent = filepath.Dir(filepath.Dir(path))
	}
	unlock, err := lock(ctx, filepath.Join(lockParent, ".koto-update.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	hash, err := fileHash(job.Location.Executable)
	if err != nil || hash != job.Digest {
		return fmt.Errorf("installed app changed before restart")
	}
	p := &Prepared{Location: job.Location, Version: job.Version, Backup: job.Backup, dir: filepath.Dir(path), before: job.PreviousDigest, after: job.Digest, beforeTarget: job.PreviousTargetDigest, afterTarget: job.TargetDigest, Package: job.Package}
	defer p.Close()
	ready := filepath.Join(p.dir, "ready")
	cmd := exec.Command(job.Location.Executable)
	cmd.Env = append(withoutReady(os.Environ()), readyEnv+"="+ready)
	detach(cmd)
	if err = cmd.Start(); err == nil {
		done := make(chan error, 1)
		go func() { done <- cmd.Wait(); close(done) }()
		err = waitReady(startup, ready, cmd.Process.Pid, done)
		if err != nil {
			_ = cmd.Process.Kill()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
			}
		}
	}
	if err == nil {
		return p.Commit()
	}
	if rollback := p.Rollback(); rollback != nil {
		return errors.Join(err, rollback)
	}
	old := exec.Command(job.Location.Executable)
	old.Env = withoutReady(os.Environ())
	detach(old)
	return errors.Join(fmt.Errorf("new app did not start; restored previous version: %w", err), old.Start())
}

func readRestart(path string) (restartJob, error) {
	var job restartJob
	if !filepath.IsAbs(path) || filepath.Base(path) != "restart.json" || !strings.HasPrefix(filepath.Base(filepath.Dir(path)), ".koto-update-") {
		return job, fmt.Errorf("invalid update restart path")
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() > 16<<10 {
		return job, fmt.Errorf("invalid update restart record")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return job, err
	}
	if err = json.Unmarshal(b, &job); err != nil {
		return job, err
	}
	l := job.Location
	if job.Parent <= 0 || job.Parent == os.Getpid() || (l.Product != App && l.Product != CLI) || !Released(job.Version) || !filepath.IsAbs(l.Target) || !filepath.IsAbs(l.Executable) || len(job.Digest) != 64 {
		return job, fmt.Errorf("invalid update restart record")
	}
	executable, err := os.Executable()
	if err != nil {
		return job, err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return job, err
	}
	if job.Package != nil {
		if err = validatePackageRestart(job, path, executable); err != nil {
			return job, err
		}
	} else {
		parent := filepath.Dir(l.Target)
		if filepath.Dir(filepath.Dir(path)) != parent || filepath.Dir(filepath.Dir(job.Backup)) != parent || !strings.HasPrefix(filepath.Base(filepath.Dir(job.Backup)), ".koto-previous-") || filepath.Base(job.Backup) != filepath.Base(l.Target) {
			return job, fmt.Errorf("invalid update restart record")
		}
		if job.Helper != "" {
			expected := filepath.Join(filepath.Dir(path), "helper"+filepath.Ext(l.Executable))
			if job.Helper != expected || executable != expected {
				return job, fmt.Errorf("update helper executable mismatch")
			}
			if hash, e := fileHash(expected); e != nil || hash != job.HelperDigest || (hash != job.PreviousDigest && hash != job.Digest) {
				return job, fmt.Errorf("update recovery helper changed")
			}
		} else if executable != l.Executable {
			return job, fmt.Errorf("update helper executable mismatch")
		}
	}

	return job, nil
}

func waitProcess(ctx context.Context, pid int) error {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for platform.ProcessAlive(pid) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

func waitReady(ctx context.Context, path string, pid int, done <-chan error) error {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-done:
			return fmt.Errorf("app exited before becoming ready: %v", err)
		case <-t.C:
			b, _ := os.ReadFile(path)
			if string(b) == strconv.Itoa(pid) {
				return nil
			}
		}
	}
}
