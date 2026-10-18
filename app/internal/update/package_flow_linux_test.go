//go:build linux

package update

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageTransactionRecoveryFiles(t *testing.T) {
	for _, state := range []string{"success", "denied", "partial-restored", "partial-denied"} {
		t.Run(state, func(t *testing.T) {
			f := newPackageFixture(t, false)
			p := &Prepared{Location: f.location, Version: "0.1.0", dir: f.stage, Package: f.prepare(), helper: filepath.Join(f.stage, "helper")}
			if err := os.WriteFile(p.helper, f.oldBody, 0700); err != nil {
				t.Fatal(err)
			}
			switch state {
			case "denied":
				f.failInstall = 126
			case "partial-restored":
				f.failInstall, f.partial = 37, true
			case "partial-denied":
				f.failInstall, f.failRollback, f.partial = 37, 127, true
			}
			p.Package.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
				if name == "/usr/bin/pkexec" {
					b, err := os.ReadFile(filepath.Join(f.stage, "restart.json"))
					var journal restartJob
					if err != nil || json.Unmarshal(b, &journal) != nil || journal.Package == nil || !journal.Package.NeedsRecovery || journal.Helper != p.helper {
						t.Fatalf("privileged write started without a recovery record: %s %v", b, err)
					}
				}
				return f.run(ctx, name, args...)
			}
			err := p.Install()
			if (err == nil) != (state == "success") {
				t.Fatalf("unexpected install result: %v", err)
			}
			p.Close()
			retained := state == "success" || state == "partial-denied"
			_, statErr := os.Stat(f.stage)
			if retained != (statErr == nil) {
				t.Fatalf("recovery material retained=%v, expected %v", statErr == nil, retained)
			}
			if state == "partial-denied" {
				if !strings.Contains(err.Error(), "recovery files retained") {
					t.Fatalf("failure did not identify recoverable material: %v", err)
				}
				f.failRollback = 0
				if err := p.Rollback(); err != nil {
					t.Fatal(err)
				}
			} else if state == "success" {
				p.Commit()
			}
			p.Close()
			if _, err := os.Stat(f.stage); !os.IsNotExist(err) {
				t.Fatalf("completed transaction left staging files: %v", err)
			}
			if state != "success" {
				f.assertOld()
			}
		})
	}
}

func TestPackageTransactionCancelledBeforeWrite(t *testing.T) {
	f := newPackageFixture(t, false)
	p := &Prepared{Location: f.location, Version: "0.1.0", dir: f.stage, Package: f.prepare()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.InstallContext(ctx); err != context.Canceled {
		t.Fatal(err)
	}
	if len(f.privileged) != 0 || p.Package.NeedsRecovery {
		t.Fatal("cancelled transaction requested privileged writes")
	}
	p.Close()
	f.assertOld()
}

func TestPackageRecoveryBlocksNextUpdate(t *testing.T) {
	parent := t.TempDir()
	stage, err := os.MkdirTemp(parent, ".koto-update-")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkPackageRecovery(parent); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(stage, "restart.json")
	if err := writeRestartRecord(journal, restartJob{Version: "0.1.0"}); err != nil {
		t.Fatal(err)
	}
	if err := checkPackageRecovery(parent); err == nil || !strings.Contains(err.Error(), journal) {
		t.Fatalf("pending transaction not reported: %v", err)
	}
	if _, err := os.Stat(journal); err != nil {
		t.Fatalf("checking recovery removed it: %v", err)
	}
}
