//go:build !windows

package codex

import (
	"os/exec"
	"syscall"
	"time"
)

type serverProcessTree struct{ pid int }

func startServerProcessTree(cmd *exec.Cmd) (*serverProcessTree, error) {
	// npm's node launcher and Codex's background git jobs inherit this group.
	// Never signal the app's own group or discover targets by executable name.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &serverProcessTree{pid: cmd.Process.Pid}, nil
}

func (p *serverProcessTree) kill() { _ = syscall.Kill(-p.pid, syscall.SIGKILL) }

func (p *serverProcessTree) wait() {
	for processGroupRunning(p.pid) {
		time.Sleep(5 * time.Millisecond)
	}
}
