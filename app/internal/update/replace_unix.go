//go:build !windows

package update

import "os"

func replace(from, to string) error { return os.Rename(from, to) }
