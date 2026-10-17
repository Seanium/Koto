package koto

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"koto/internal/platform"
)

// Run the test executable itself as a harmless, temporary Agent stand-in. The
// launcher exits before the probe, so the holder is not this test's descendant.
// This works without Unix shells, sleep, signals, or installed Agent programs.
func TestHolderProcess(t *testing.T) {
	mode := os.Getenv("KOTO_TEST_HOLDER_MODE")
	if mode == "" {
		return
	}
	if mode == "launch" {
		var args []string
		if err := json.Unmarshal([]byte(os.Getenv("KOTO_TEST_HOLDER_ARGS")), &args); err != nil || len(args) == 0 {
			os.Exit(2)
		}
		null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
		if err != nil {
			os.Exit(3)
		}
		defer null.Close()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = null, null, null
		cmd.Env = append(os.Environ(), "KOTO_TEST_HOLDER_MODE=hold")
		if err := cmd.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(4)
		}
		fmt.Println(cmd.Process.Pid)
		_ = cmd.Process.Release()
		os.Exit(0)
	}
	if mode != "hold" {
		os.Exit(5)
	}
	if name := os.Getenv("KOTO_TEST_HOLDER_FILE"); name != "" {
		file, err := os.Open(name)
		if err != nil {
			os.Exit(6)
		}
		defer file.Close()
	}
	if err := os.WriteFile(os.Getenv("KOTO_TEST_HOLDER_READY"), []byte("ready"), 0600); err != nil {
		os.Exit(7)
	}
	time.Sleep(10 * time.Minute)
	os.Exit(0)
}

type holder struct{ pid int }

func startHolder(t *testing.T, cwd string, args ...string) *holder {
	t.Helper()
	return startTestHolder(t, cwd, "", args)
}

func startFileHolder(t *testing.T, file string) *holder {
	t.Helper()
	return startTestHolder(t, "", file, nil)
}

func startTestHolder(t *testing.T, cwd, file string, args []string) *holder {
	t.Helper()
	exe, err := os.Executable()
	must(t, err)
	ready := filepath.Join(t.TempDir(), "ready")
	self := len(args) == 0
	if self {
		args = []string{exe, "-test.run=^TestHolderProcess$"}
	}
	encoded, err := json.Marshal(args)
	must(t, err)
	launcher := exec.Command(exe, "-test.run=^TestHolderProcess$")
	launcher.Dir = cwd
	launcher.Env = append(os.Environ(), "KOTO_TEST_HOLDER_MODE=launch", "KOTO_TEST_HOLDER_ARGS="+string(encoded), "KOTO_TEST_HOLDER_READY="+ready, "KOTO_TEST_HOLDER_FILE="+file)
	out, err := launcher.CombinedOutput()
	if err != nil {
		t.Fatalf("holder launcher: %v: %s", err, out)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	must(t, err)
	h := &holder{pid: pid}
	t.Cleanup(func() { h.kill(); h.exited() })
	if self {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(ready); err == nil {
				return h
			}
			if !platform.ProcessAlive(pid) {
				t.Fatal("holder exited before opening its test file")
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("holder did not become ready")
	}
	time.Sleep(150 * time.Millisecond) // Let the requested interpreter exec.
	return h
}

func (h *holder) kill() {
	if h.pid > 0 {
		if p, err := os.FindProcess(h.pid); err == nil {
			_ = p.Kill()
			_ = p.Release()
		}
	}
}

func (h *holder) exited() {
	for i := 0; i < 250 && platform.ProcessAlive(h.pid); i++ {
		time.Sleep(20 * time.Millisecond)
	}
}
