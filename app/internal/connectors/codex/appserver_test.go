package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// The same executable acts as a launcher, RPC server, and background writer.
// No shell or installed Agent is needed, so lifecycle coverage runs on Windows too.
func TestMain(m *testing.M) {
	mode := os.Getenv("KOTO_RPC_HELPER")
	if mode != "" {
		runRPCProcess(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runRPCProcess(mode string) {
	root := os.Getenv("KOTO_RPC_ROOT")
	if root != "" {
		_ = os.MkdirAll(root, 0700)
		_ = os.WriteFile(filepath.Join(root, mode+".pid"), []byte(strconv.Itoa(os.Getpid())), 0600)
	}
	spawn := func(childMode string) *exec.Cmd {
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), "KOTO_RPC_HELPER="+childMode)
		child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		return child
	}
	if mode == "launcher" {
		child := spawn("rpc")
		go func() {
			for {
				if _, err := os.Stat(filepath.Join(root, "exit-launcher")); err == nil {
					os.Exit(0) // Deliberately leave the inherited pipes with children.
				}
				time.Sleep(5 * time.Millisecond)
			}
		}()
		_ = child.Wait()
		return
	}
	if mode == "writer" {
		for i := 0; ; i++ {
			dir := filepath.Join(root, ".tmp", "plugins-clone")
			_ = os.MkdirAll(dir, 0700)
			_ = os.WriteFile(filepath.Join(dir, "heartbeat"), []byte(strconv.Itoa(i)), 0600)
			time.Sleep(2 * time.Millisecond)
		}
	}
	if root != "" {
		spawn("writer")
		for {
			if _, err := os.Stat(filepath.Join(root, ".tmp", "plugins-clone", "heartbeat")); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
	}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var req map[string]any
		_ = json.Unmarshal(sc.Bytes(), &req)
		if req["id"] == nil {
			continue
		}
		if req["method"] == "slow" {
			time.Sleep(150 * time.Millisecond)
		}
		if req["method"] == "block" {
			_ = os.WriteFile(filepath.Join(root, "blocked"), nil, 0600)
			for {
				time.Sleep(time.Second)
			}
		}
		if req["method"] == "exit-launcher" {
			_ = os.WriteFile(filepath.Join(root, "exit-launcher"), nil, 0600)
			for {
				time.Sleep(time.Second)
			}
		}
		b, _ := json.Marshal(map[string]any{"id": req["id"], "result": map[string]any{"ok": true}})
		fmt.Println(string(b))
	}
}

func TestIdleTimerNeverInterruptsRequest(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s := &AppServer{Bin: bin, Env: map[string]string{"KOTO_RPC_HELPER": "rpc"}, IdleMs: 20, Timeout: 5 * time.Second}
	defer s.Stop()
	for i := 0; i < 3; i++ {
		if _, err := s.Call("slow", nil); err != nil {
			t.Fatalf("active request was interrupted: %v", err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		stopped := s.cmd == nil
		s.mu.Unlock()
		if stopped {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("idle helper process remained alive")
}

func lifecycleServer(t *testing.T, root string) *AppServer {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s := &AppServer{Bin: bin, Env: map[string]string{"KOTO_RPC_HELPER": "launcher", "KOTO_RPC_ROOT": root}, IdleMs: 60000, Timeout: 5 * time.Second}
	t.Cleanup(s.Stop)
	if _, err := s.Call("ping", nil); err != nil {
		t.Fatal(err)
	}
	return s
}

func assertServerStopped(t *testing.T, s *AppServer, root string) {
	t.Helper()
	s.mu.Lock()
	stopped := s.process == nil && s.cmd == nil && s.stdin == nil && len(s.pending) == 0
	s.mu.Unlock()
	if !stopped {
		t.Fatal("Stop returned before the process/readers/request cleanup completed")
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	// A surviving writer recreates this directory on every iteration.
	time.Sleep(80 * time.Millisecond)
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("descendant is still writing: %v", err)
	}
}

func TestStopWaitsForOwnedProcessTree(t *testing.T) {
	root := filepath.Join(t.TempDir(), "owned")
	s := lifecycleServer(t, root)
	other := lifecycleServer(t, filepath.Join(t.TempDir(), "unrelated"))
	var stopping sync.WaitGroup
	for range 2 {
		stopping.Go(s.Stop)
	}
	stopping.Wait()
	assertServerStopped(t, s, root)
	if _, err := other.Call("ping", nil); err != nil {
		t.Fatalf("another server was affected: %v", err)
	}
	if _, err := s.Call("ping", nil); err != nil {
		t.Fatalf("restart after Stop: %v", err)
	}
	s.Stop()
	assertServerStopped(t, s, root)
}

func TestStopInterruptsPendingCallAndReleasesPipes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "pending")
	s := lifecycleServer(t, root)
	done := make(chan error, 1)
	go func() { _, err := s.Call("block", nil); done <- err }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "blocked")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("request did not reach helper")
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.Stop()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("killed request unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("pending call was not released")
	}
	assertServerStopped(t, s, root)
}

func TestLauncherExitReapsInheritedPipeDescendants(t *testing.T) {
	root := filepath.Join(t.TempDir(), "exited")
	s := lifecycleServer(t, root)
	if _, err := s.Call("exit-launcher", nil); err == nil {
		t.Fatal("exited launcher unexpectedly succeeded")
	}
	// The RPC process and writer keep stdout/stderr open after their parent
	// exits. Completion must be driven by Wait, not by waiting for pipe EOF.
	assertServerStopped(t, s, root)
}
