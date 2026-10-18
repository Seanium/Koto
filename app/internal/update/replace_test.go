package update

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAtomicReplaceRunningProcess(t *testing.T) {
	if ready := os.Getenv("KOTO_REPLACE_TEST_READY"); ready != "" {
		if err := os.WriteFile(ready, []byte("ready"), 0600); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Second)
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	target := filepath.Join(root, "koto.exe")
	next := filepath.Join(root, "next.exe")
	backup := filepath.Join(root, "previous.exe")
	ready := filepath.Join(root, "ready")
	for _, path := range []string{target, next} {
		if err := os.WriteFile(path, image, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(target, backup); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(target, "-test.run=^TestAtomicReplaceRunningProcess$")
	cmd.Env = append(os.Environ(), "KOTO_REPLACE_TEST_READY="+ready)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := replace(next, target); err != nil {
		t.Fatal(err)
	}
	if err := replace(backup, target); err != nil {
		t.Fatalf("rollback while EXE mapped: %v", err)
	}
	check := exec.Command(target, "-test.run=^TestVersionOrdering$")
	if out, err := check.CombinedOutput(); err != nil {
		t.Fatalf("replacement does not run: %v %s", err, out)
	}
	stage, err := os.MkdirTemp(root, ".koto-update-")
	if err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(stage, "new.exe")
	if err := os.WriteFile(payload, image, 0755); err != nil {
		t.Fatal(err)
	}
	before, _ := fileHash(target)
	p := &Prepared{Location: Location{target, target, CLI}, Version: "1.1.0", dir: stage, payload: payload, before: before}
	defer p.Close()
	if err := p.Install(); err != nil {
		t.Fatalf("full transaction with old EXE still mapped: %v", err)
	}
	if err := p.Rollback(); err != nil {
		t.Fatalf("full transaction rollback while old EXE mapped: %v", err)
	}
}
