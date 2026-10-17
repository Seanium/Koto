//go:build !windows

// Package platform contains the operating-system primitives used by the core.
package platform

import (
	"errors"
	"syscall"
)

// ProcessAlive is conservative when the process belongs to another user.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
