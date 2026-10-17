package codex

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func processGroupRunning(pid int) bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return syscall.Kill(-pid, 0) == nil
	}
	group := strconv.Itoa(pid)
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue // It exited between the directory scan and read.
		}
		// comm is parenthesized and may itself contain spaces/parentheses.
		end := strings.LastIndexByte(string(b), ')')
		if end < 0 {
			continue
		}
		fields := strings.Fields(string(b[end+1:]))
		if len(fields) >= 3 && fields[2] == group && fields[0] != "Z" && fields[0] != "X" {
			return true
		}
	}
	// Zombies cannot write and need not wait for the host's init to reap them.
	return false
}
