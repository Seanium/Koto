package update

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestStaleCheckCannotDowngradeInstalledVersion(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "koto.exe")
	if err := os.WriteFile(target, image, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target+".version", []byte("2.0.0"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KOTO_VERSION_TEST_PROCESS", "1")
	asset, err := AssetName(CLI, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	r, bodies := testRelease("v1.1.0", asset, image)
	c := testClient([]release{r}, bodies)
	check, err := c.Check(context.Background(), "1.0.0", CLI, runtime.GOOS, runtime.GOARCH, "stable")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := fileHash(target)
	p, err := c.Prepare(context.Background(), check, Location{target, target, CLI}, nil)
	if err == nil {
		p.Close()
		t.Fatal("stale check accepted a newer installation")
	}
	if !strings.Contains(err.Error(), "check again") {
		t.Fatal(err)
	}
	after, _ := fileHash(target)
	if after != before {
		t.Fatal("stale check modified the installation")
	}
	entries, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".koto-update-*"))
	if len(entries) != 0 {
		t.Fatal("stale check downloaded or left a stage")
	}
}

// Exit without defers after installation, as an abrupt process termination.
// A later process must find the old executable and its recovery information.
func TestPortableProcessInterruption(t *testing.T) {
	if root := os.Getenv("KOTO_TRANSACTION_TEST_ROOT"); root != "" {
		target := filepath.Join(root, "koto.exe")
		stage, err := os.MkdirTemp(root, ".koto-update-")
		if err != nil {
			t.Fatal(err)
		}
		payload := filepath.Join(stage, "new")
		body := []byte("new")
		if os.Getenv("KOTO_TRANSACTION_NATIVE_HELPER") == "1" {
			exe, _ := os.Executable()
			body, err = os.ReadFile(exe)
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(payload, body, 0755); err != nil {
			t.Fatal(err)
		}
		before, _ := fileHash(target)
		p := &Prepared{Location: Location{target, target, CLI}, Version: "1.1.0", dir: stage, payload: payload, before: before}
		if err := p.Install(); err != nil {
			t.Fatal(err)
		}
		if os.Getenv("KOTO_TRANSACTION_COMMIT_DECISION") == "1" {
			b, _ := json.Marshal(map[string]string{"target": target, "stage": p.dir})
			if err := writeJournal(filepath.Join(filepath.Dir(p.Backup), "committed.json"), b); err != nil {
				t.Fatal(err)
			}
		}
		os.Exit(91)
	}
	root := literalTransactionRoot(t)
	target := filepath.Join(root, "koto.exe")
	if err := os.WriteFile(target, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	cmd := exec.Command(exe, "-test.run=^TestPortableProcessInterruption$")
	cmd.Env = append(os.Environ(), "KOTO_TRANSACTION_TEST_ROOT="+root)
	if out, err := cmd.CombinedOutput(); err == nil || commandExitTest(err) != 91 {
		t.Fatalf("child: %v %s", err, out)
	}
	if body, err := os.ReadFile(target); err != nil || string(body) != "new" {
		t.Fatalf("installed path missing/invalid: %q %v", body, err)
	}
	paths := transactionJournals(t, root)
	if len(paths) != 1 {
		t.Fatalf("recovery journal missing: %v", paths)
	}
	var job restartJob
	b, _ := os.ReadFile(paths[0])
	if err := json.Unmarshal(b, &job); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(job.Backup); err != nil || string(body) != "old" {
		t.Fatalf("old version missing: %q %v", body, err)
	}
	if err := checkPortableRecovery(job.Location); err == nil {
		t.Fatal("pending recovery may be overwritten")
	}
	p := &Prepared{Location: job.Location, Version: job.Version, dir: filepath.Dir(paths[0]), Backup: job.Backup, before: job.PreviousDigest, after: job.Digest, beforeTarget: job.PreviousTargetDigest, afterTarget: job.TargetDigest}
	p.Close()
	if _, err := os.Stat(paths[0]); err != nil {
		t.Fatal("Close discarded unresolved recovery", err)
	}
	if err := p.Rollback(); err != nil {
		t.Fatal(err)
	}
	p.Close()
	if body, err := os.ReadFile(target); err != nil || string(body) != "old" {
		t.Fatalf("rollback: %q %v", body, err)
	}
	if _, err := os.Stat(paths[0]); !os.IsNotExist(err) {
		t.Fatal("completed recovery journal remains", err)
	}
}

func TestNativeRecoveryAfterInterruption(t *testing.T) {
	root, err := filepath.EvalSymlinks(literalTransactionRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "koto.exe")
	if err := os.WriteFile(target, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	cmd := exec.Command(exe, "-test.run=^TestPortableProcessInterruption$")
	cmd.Env = append(os.Environ(), "KOTO_TRANSACTION_TEST_ROOT="+root, "KOTO_TRANSACTION_NATIVE_HELPER=1")
	if out, err := cmd.CombinedOutput(); err == nil || commandExitTest(err) != 91 {
		t.Fatalf("child: %v %s", err, out)
	}
	paths := transactionJournals(t, root)
	if len(paths) != 1 {
		t.Fatal("missing recovery record")
	}
	var job restartJob
	b, _ := os.ReadFile(paths[0])
	if err := json.Unmarshal(b, &job); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(job.Helper, "--koto-update-recover", paths[0])
	cmd.Env = append(os.Environ(), "KOTO_UPDATE_TEST_PROCESS=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("recovery helper: %v %s", err, out)
	}
	if b, _ := os.ReadFile(target); string(b) != "old" {
		t.Fatalf("recovery did not restore original: %q", b)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		cleanPrevious(job.Location)
		if _, err := os.Stat(filepath.Dir(paths[0])); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("recovery staging remains after helper exit")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestCommitDecisionSurvivesProcessInterruption(t *testing.T) {
	root := literalTransactionRoot(t)
	target := filepath.Join(root, "koto.exe")
	os.WriteFile(target, []byte("old"), 0755)
	exe, _ := os.Executable()
	cmd := exec.Command(exe, "-test.run=^TestPortableProcessInterruption$")
	cmd.Env = append(os.Environ(), "KOTO_TRANSACTION_TEST_ROOT="+root, "KOTO_TRANSACTION_COMMIT_DECISION=1")
	if out, err := cmd.CombinedOutput(); err == nil || commandExitTest(err) != 91 {
		t.Fatalf("child: %v %s", err, out)
	}
	l := Location{target, target, CLI}
	cleanPrevious(l)
	if err := checkPortableRecovery(l); err != nil {
		t.Fatal("committed transaction blocks future updates", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "new" {
		t.Fatal("committed update was reverted")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".koto-update-") || strings.HasPrefix(entry.Name(), ".koto-previous-") {
			t.Fatal("committed recovery files remain", entry.Name())
		}
	}
}

func literalTransactionRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "install [test] 中文")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	return root
}

func transactionJournals(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name(), "restart.json")
		if entry.IsDir() {
			if _, err := os.Stat(path); err == nil {
				paths = append(paths, path)
			}
		}
	}
	return paths
}

func commandExitTest(err error) int {
	if e, ok := err.(*exec.ExitError); ok {
		return e.ExitCode()
	}
	return -1
}

func TestRollbackRejectsIndependentChange(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "koto")
	stage, _ := os.MkdirTemp(root, ".koto-update-")
	payload := filepath.Join(stage, "new")
	os.WriteFile(target, []byte("old"), 0755)
	os.WriteFile(payload, []byte("new"), 0755)
	before, _ := fileHash(target)
	p := &Prepared{Location: Location{target, target, CLI}, Version: "1.1.0", dir: stage, payload: payload, before: before}
	if err := p.Install(); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(target, []byte("external"), 0755)
	if err := p.Rollback(); err == nil {
		t.Fatal("rollback overwrote another installer")
	}
	p.Close()
	if body, _ := os.ReadFile(target); string(body) != "external" {
		t.Fatal("independent installation overwritten")
	}
	if _, err := os.Stat(filepath.Join(stage, "restart.json")); err != nil {
		t.Fatal("recovery record discarded", err)
	}
}

func TestBundleRollbackDistinguishesIdenticalExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows updates standalone executables")
	}
	root := t.TempDir()
	target := filepath.Join(root, "koto.app")
	stage, _ := os.MkdirTemp(root, ".koto-update-")
	payload := filepath.Join(stage, "new.app")
	for dir, label := range map[string]string{target: "old resources", payload: "new resources"} {
		os.MkdirAll(dir, 0700)
		os.WriteFile(filepath.Join(dir, "koto"), []byte("identical executable"), 0755)
		os.WriteFile(filepath.Join(dir, "resources"), []byte(label), 0600)
	}
	binary := filepath.Join(target, "koto")
	before, _ := fileHash(binary)
	p := &Prepared{Location: Location{binary, target, App}, Version: "1.1.0", dir: stage, payload: payload, before: before}
	defer p.Close()
	if err := p.Install(); err != nil {
		t.Fatal(err)
	}
	if err := p.Rollback(); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(filepath.Join(target, "resources")); string(body) != "old resources" {
		t.Fatalf("resources were not restored: %q", body)
	}
}
