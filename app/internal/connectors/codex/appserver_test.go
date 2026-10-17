package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A separate test process speaks stdio JSON-RPC without touching native Agent data.
func TestRPCProcess(t *testing.T) {
	if os.Getenv("KOTO_RPC_HELPER") != "1" {
		return
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
		b, _ := json.Marshal(map[string]any{"id": req["id"], "result": map[string]any{"ok": true}})
		fmt.Println(string(b))
	}
	os.Exit(0)
}

func TestIdleTimerNeverInterruptsRequest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX helper wrapper")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "rpc")
	if err = os.WriteFile(bin, []byte("#!/bin/sh\nexec '"+strings.ReplaceAll(exe, "'", "'\\''")+"' -test.run=^TestRPCProcess$\n"), 0700); err != nil {
		t.Fatal(err)
	}
	s := &AppServer{Bin: bin, Env: map[string]string{"KOTO_RPC_HELPER": "1"}, IdleMs: 20, Timeout: 5 * time.Second}
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
