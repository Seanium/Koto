package codex

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func processGroupRunning(pid int) bool {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", pid)
	if err != nil {
		return syscall.Kill(-pid, 0) == nil
	}
	for _, process := range processes {
		// SZOMB processes have already released files; launchd may reap later.
		if process.Proc.P_stat != 5 {
			return true
		}
	}
	return false
}
