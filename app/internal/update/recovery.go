package update

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func checkPortableRecovery(l Location) error {
	dirs, err := updateDirectories(filepath.Dir(l.Target), ".koto-update-")
	if err != nil {
		return err
	}
	for _, dir := range dirs {
		path := filepath.Join(dir, "restart.json")
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<10 {
			continue
		}
		b, err := os.ReadFile(path)
		var job restartJob
		if err == nil && json.Unmarshal(b, &job) == nil && job.Location == l {
			helper := l.Executable
			if job.Helper != "" {
				helper = job.Helper
			}
			return fmt.Errorf("an interrupted update needs recovery first: %q --koto-update-recover %q", helper, path)
		}
	}
	return nil
}

func recoverPortable(path string, job restartJob) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := waitProcess(ctx, job.Parent); err != nil {
		return fmt.Errorf("close the previous process before recovery: %w", err)
	}
	unlock, err := lock(ctx, filepath.Join(filepath.Dir(job.Location.Target), ".koto-update.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	job, err = readRestart(path)
	if err != nil {
		return err
	}
	if len(job.PreviousDigest) != 64 {
		return fmt.Errorf("missing previous executable digest")
	}
	p := &Prepared{Location: job.Location, Version: job.Version, dir: filepath.Dir(path), Backup: job.Backup, before: job.PreviousDigest, after: job.Digest, beforeTarget: job.PreviousTargetDigest, afterTarget: job.TargetDigest}
	defer p.Close()
	return p.Rollback()
}
