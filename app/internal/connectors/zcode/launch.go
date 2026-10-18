package zcode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

type nativeLaunch struct {
	argv []string
	env  map[string]string
}

// NativeResume preserves paths with spaces and uses ZCode's bundled Node runtime
// on desktop-only installations. No nonexistent bare command is returned.
func (c *connector) NativeResume(session string) ([]string, map[string]string) {
	launch, err := c.nativeLaunch()
	if err != nil {
		return nil, nil
	}
	launch.env = c.launchEnvironment(launch.env)
	return append(append([]string{}, launch.argv...), "--resume", session), launch.env
}

func (c *connector) launchEnvironment(env map[string]string) map[string]string {
	if env == nil {
		env = map[string]string{}
	}
	env["ZCODE_STORAGE_DIR"] = c.root
	env["ZCODE_SESSION_DB_PATH"] = c.db
	// Native provider configuration is independent of the transcript directory.
	// Use the same target configuration from which Plan selected its model, even
	// when the command is pasted into a terminal with a different HOME.
	env["ZCODE_PERSONAL_PROVIDER_CONFIG_FILE"] = filepath.Join(c.root, "v2", "provider_config.json")
	return env
}
func (c *connector) nativeLaunch() (nativeLaunch, error) {
	home, _ := os.UserHomeDir()
	apps := desktopInstallations(runtime.GOOS, home, os.Getenv)
	return resolveNativeLaunch(c.command, runtime.GOOS, apps, exec.LookPath)
}

func desktopInstallations(goos, home string, getenv func(string) string) []string {
	switch goos {
	case "darwin":
		return []string{filepath.Join(home, "Applications", "ZCode.app"), "/Applications/ZCode.app"}
	case "linux":
		return []string{"/opt/ZCode"}
	case "windows":
		local := getenv("LOCALAPPDATA")
		if local == "" && home != "" {
			local = filepath.Join(home, "AppData", "Local")
		}
		var dirs []string
		if local != "" {
			dirs = append(dirs, filepath.Join(local, "Programs", "ZCode"))
		}
		for _, key := range []string{"ProgramW6432", "ProgramFiles", "ProgramFiles(x86)"} {
			if base := getenv(key); base != "" {
				dirs = append(dirs, filepath.Join(base, "ZCode"))
			}
		}
		return dirs
	}
	return nil
}

func resolveNativeLaunch(command []string, goos string, apps []string, lookup func(string) (string, error)) (nativeLaunch, error) {
	if len(command) > 0 {
		binary, err := lookup(command[0])
		if err != nil {
			return nativeLaunch{}, fmt.Errorf("ZCode command unavailable: %w", err)
		}
		if len(command) == 1 {
			if launch, ok := bundledExecutableLaunch(binary, goos); ok {
				return launch, nil
			}
		}
		return nativeLaunch{argv: append([]string{binary}, command[1:]...)}, nil
	}
	if binary, err := lookup("zcode"); err == nil {
		if launch, ok := bundledExecutableLaunch(binary, goos); ok {
			return launch, nil
		}
		return nativeLaunch{argv: []string{binary}}, nil
	}
	for _, app := range apps {
		if launch, ok := desktopBundleLaunch(app, goos); ok {
			return launch, nil
		}
	}
	return nativeLaunch{}, fmt.Errorf("ZCode CLI unavailable; install ZCode or configure its native command")
}
func bundledExecutableLaunch(binary, goos string) (nativeLaunch, bool) {
	candidates := []string{binary}
	if resolved, err := filepath.EvalSymlinks(binary); err == nil && resolved != binary {
		candidates = append(candidates, resolved)
	}
	for _, candidate := range candidates {
		app := filepath.Dir(candidate)
		if goos == "darwin" {
			app = filepath.Dir(filepath.Dir(app))
		}
		launch, ok := desktopBundleLaunch(app, goos)
		if !ok {
			continue
		}
		actual, actualErr := os.Stat(binary)
		bundled, bundledErr := os.Stat(launch.argv[0])
		if actualErr == nil && bundledErr == nil && os.SameFile(actual, bundled) {
			return launch, true
		}
	}
	return nativeLaunch{}, false
}

func desktopBundleLaunch(app, goos string) (nativeLaunch, bool) {
	var exe, resources string
	switch goos {
	case "darwin":
		exe = filepath.Join(app, "Contents", "MacOS", "ZCode")
		resources = filepath.Join(app, "Contents", "Resources")
	case "windows":
		exe = filepath.Join(app, "ZCode.exe")
		resources = filepath.Join(app, "resources")
	case "linux":
		exe = filepath.Join(app, "zcode")
		resources = filepath.Join(app, "resources")
	default:
		return nativeLaunch{}, false
	}
	script := filepath.Join(resources, "glm", "zcode.cjs")
	provider := filepath.Join(resources, "config", "provider", "zcode-builtin.json")
	for _, p := range []string{exe, script, provider} {
		st, err := os.Stat(p)
		if err != nil || !st.Mode().IsRegular() {
			return nativeLaunch{}, false
		}
	}
	st, _ := os.Stat(exe)
	if runtime.GOOS != "windows" && goos != "windows" && st.Mode().Perm()&0111 == 0 {
		return nativeLaunch{}, false
	}
	return nativeLaunch{argv: []string{exe, script}, env: map[string]string{"ELECTRON_RUN_AS_NODE": "1", "ZCODE_BUILTIN_PROVIDER_CONFIG_FILE": provider}}, true
}
func (c *connector) nativeCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	launch, err := c.nativeLaunch()
	if err != nil {
		return nil, err
	}
	argv := append(append([]string{}, launch.argv[1:]...), args...)
	cmd := exec.CommandContext(ctx, launch.argv[0], argv...)
	cmd.Env = os.Environ()
	for key, value := range c.launchEnvironment(launch.env) {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	return cmd, nil
}
