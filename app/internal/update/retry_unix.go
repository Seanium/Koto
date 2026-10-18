//go:build !windows

package update

func retryFileAccess(run func() error) error { return run() }
