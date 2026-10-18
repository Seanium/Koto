package update

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The real updater helper and a tiny native app run in separate processes. This
// exercises process exit, readiness, rollback and cleanup without a GUI/network.
func TestMain(m *testing.M) {
	if os.Getenv("KOTO_VERSION_TEST_PROCESS") == "1" {
		exe, _ := os.Executable()
		b, _ := os.ReadFile(exe + ".version")
		fmt.Printf("koto %s\n", b)
		os.Exit(0)
	}
	if os.Getenv("KOTO_UPDATE_TEST_PROCESS") == "1" {
		if len(os.Args) == 3 && os.Args[1] == "--koto-update-recover" {
			if err := RunRecovery(os.Args[2]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(3)
			}
			os.Exit(0)
		}
		if len(os.Args) == 3 && os.Args[1] == "--koto-update-restart" {
			if err := RunRestart(os.Args[2]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(3)
			}
			os.Exit(0)
		}
		exe, _ := os.Executable()
		dir := filepath.Dir(exe)
		build := testBuild(exe)
		if string(build) == "broken" {
			os.Exit(4)
		}
		started := os.Getenv("KOTO_UPDATE_TEST_STARTED")
		if started == "" {
			started = filepath.Join(filepath.Dir(dir), "started")
		}
		_ = os.WriteFile(started+".pid", []byte(strconv.Itoa(os.Getpid())), 0600)
		_ = os.WriteFile(started, build, 0600)
		MarkReady()
		time.Sleep(300 * time.Millisecond)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestRestartNativeProcessAndRollback(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	for _, build := range []string{"new", "broken"} {
		t.Run(build, func(t *testing.T) {
			root := t.TempDir()
			root, err = filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, "app")
			if runtime.GOOS == "windows" {
				target = filepath.Join(root, "koto.exe")
			}
			backupDir, _ := os.MkdirTemp(root, ".koto-previous-")
			backup := filepath.Join(backupDir, filepath.Base(target))
			for dir, label := range map[string]string{target: build, backup: "previous"} {
				if runtime.GOOS == "windows" {
					if err := os.WriteFile(dir, append(append([]byte(nil), image...), []byte("\nKOTO_TEST_BUILD="+label+"\n")...), 0755); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "koto.exe"), image, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "build"), []byte(label), 0600); err != nil {
					t.Fatal(err)
				}
			}
			binary := filepath.Join(target, "koto.exe")
			if runtime.GOOS == "windows" {
				binary = target
			}
			digest, _ := fileHash(binary)
			stage, _ := os.MkdirTemp(root, ".koto-update-")
			oldBinary := filepath.Join(backup, "koto.exe")
			if runtime.GOOS == "windows" {
				oldBinary = backup
			}
			helper := filepath.Join(stage, "helper"+filepath.Ext(binary))
			if err := copyExecutable(oldBinary, helper); err != nil {
				t.Fatal(err)
			}
			oldDigest, _ := fileHash(oldBinary)
			beforeTarget, _ := targetHash(backup)
			afterTarget, _ := targetHash(target)
			path := filepath.Join(stage, "restart.json")
			job := restartJob{Parent: 2147483646, Location: Location{binary, target, App}, Backup: backup, Version: "9.0.0", Digest: digest, PreviousDigest: oldDigest, PreviousTargetDigest: beforeTarget, TargetDigest: afterTarget, Helper: helper, HelperDigest: oldDigest}
			b, _ := json.Marshal(job)
			if err := os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, helper, "--koto-update-restart", path)
			cmd.Env = append(os.Environ(), "KOTO_UPDATE_TEST_PROCESS=1", "KOTO_UPDATE_TEST_STARTED="+filepath.Join(root, "started"))
			out, err := cmd.CombinedOutput()
			if build == "new" && err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			if build == "broken" && err == nil {
				t.Fatal("expected startup failure")
			}
			if build == "broken" && !strings.Contains(string(out), "restored previous version") {
				t.Fatalf("restart helper did not complete rollback: %v: %s (stage %s)", err, out, stage)
			}
			want := "new"
			if build == "broken" {
				want = "previous"
			}
			actual := testBuild(binary)
			if string(actual) != want {
				t.Fatalf("installed %s, want %s; helper: %v: %s", actual, want, err, out)
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				started, _ := os.ReadFile(filepath.Join(root, "started"))
				if string(started) == want {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("%s did not start", want)
				}
				time.Sleep(20 * time.Millisecond)
			}
			// Emulated processes and Windows executable mapping may outlive a
			// fixed sleep. Wait for the real child, then exercise the persisted
			// cleanup marker until both owned directories are actually gone.
			pidBytes, err := os.ReadFile(filepath.Join(root, "started.pid"))
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(string(pidBytes))
			if err != nil || pid <= 0 {
				t.Fatalf("invalid started process: %q: %v", pidBytes, err)
			}
			exitCtx, exitCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer exitCancel()
			if err := waitProcess(exitCtx, pid); err != nil {
				t.Fatalf("app process %d did not exit: %v", pid, err)
			}
			deadline = time.Now().Add(5 * time.Second)
			for {
				cleanPrevious(job.Location)
				_, stageErr := os.Stat(stage)
				_, backupErr := os.Stat(filepath.Dir(backup))
				if os.IsNotExist(stageErr) && os.IsNotExist(backupErr) {
					break
				}
				if time.Now().After(deadline) {
					stageFiles, _ := os.ReadDir(stage)
					backupFiles, _ := os.ReadDir(filepath.Dir(backup))
					t.Fatalf("update cleanup after pid %d exit: stage=%v files=%v; backup=%v files=%v", pid, stageErr, stageFiles, backupErr, backupFiles)
				}
				time.Sleep(50 * time.Millisecond)
			}
		})
	}
}

func testBuild(exe string) []byte {
	if runtime.GOOS != "windows" {
		b, _ := os.ReadFile(filepath.Join(filepath.Dir(exe), "build"))
		return b
	}
	f, err := os.Open(exe)
	if err != nil {
		return nil
	}
	defer f.Close()
	info, _ := f.Stat()
	b := make([]byte, 128)
	_, _ = f.ReadAt(b, info.Size()-int64(len(b)))
	_, label, ok := strings.Cut(string(b), "\nKOTO_TEST_BUILD=")
	if !ok {
		return nil
	}
	return []byte(strings.TrimSpace(label))
}

func TestWaitProcessCancellationAndReadiness(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitProcess(ctx, os.Getpid()) == nil {
		t.Fatal("wait ignored cancellation")
	}
	done := make(chan error, 1)
	done <- nil
	if err := waitReady(context.Background(), filepath.Join(t.TempDir(), "absent"), 100, done); err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatal(err)
	}
}
