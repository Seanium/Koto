// Package koto is the composition root: the store, the connectors, the engine and the working
// copies wired from a config, and the operations the app, the CLI and MCP share.
package koto

import (
	"os"
	"path/filepath"
	"runtime"
)

// Defaults are the per-platform locations; each can be overridden by the harness's own env var
// and by explicit config.
type Defaults struct {
	DataDir, ClaudeRoot, PiRoot, CodexHome, DshHome, AntigravityRoot, OpenCodeRoot string
	// ClaudeDesktopDirs: every Claude Desktop profile (signed in, and third-party gateway "-3p").
	ClaudeDesktopDirs []string
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// PlatformDefaults for this machine.
func PlatformDefaults() Defaults {
	home, _ := os.UserHomeDir()
	var dataHome, desktop string
	switch runtime.GOOS {
	case "windows":
		dataHome = env("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
		desktop = filepath.Join(env("APPDATA", filepath.Join(home, "AppData", "Roaming")), "Claude")
	case "darwin":
		dataHome = filepath.Join(home, ".local", "share") // CLI-style dotdirs on macOS too (like Codex/pi)
		desktop = filepath.Join(home, "Library", "Application Support", "Claude")
	default:
		dataHome = env("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
		desktop = filepath.Join(env("XDG_CONFIG_HOME", filepath.Join(home, ".config")), "Claude")
	}
	return Defaults{
		DataDir:           env("KOTO_HOME", filepath.Join(dataHome, "koto")),
		ClaudeRoot:        env("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude")),
		PiRoot:            env("PI_CODING_AGENT_DIR", filepath.Join(home, ".pi", "agent")),
		CodexHome:         env("CODEX_HOME", filepath.Join(home, ".codex")),
		DshHome:           env("DSH_HOME", filepath.Join(home, ".dsh")),
		AntigravityRoot:   filepath.Join(home, ".gemini", "antigravity-cli"),
		OpenCodeRoot:      filepath.Join(env("XDG_DATA_HOME", filepath.Join(home, ".local", "share")), "opencode"),
		ClaudeDesktopDirs: []string{desktop, desktop + "-3p"},
	}
}
