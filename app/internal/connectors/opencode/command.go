package opencode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func nativeCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	executable, err := exec.LookPath("opencode")
	if err != nil {
		return nil, err
	}
	if runtime.GOOS == "windows" && (strings.EqualFold(filepath.Ext(executable), ".cmd") || strings.EqualFold(filepath.Ext(executable), ".bat")) {
		// npm's Windows shim cannot be passed to CreateProcess. OpenCode's official
		// package contains the real executable; invoke it directly, without a shell.
		dir := filepath.Dir(executable)
		found := ""
		for _, candidate := range []string{filepath.Join(dir, "node_modules", "opencode-ai", "bin", "opencode.exe"), filepath.Join(dir, "..", "opencode-ai", "bin", "opencode.exe")} {
			if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
				found = candidate
				break
			}
		}
		if found == "" {
			return nil, fmt.Errorf("OpenCode native executable not found beside its npm shim; reinstall opencode-ai")
		}
		executable = found
	}
	return exec.CommandContext(ctx, executable, args...), nil
}
