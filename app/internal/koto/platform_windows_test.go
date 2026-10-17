//go:build windows

package koto

import (
	"path/filepath"
	"testing"
)

func TestWindowsDefaultAndOverriddenRoots(t *testing.T) {
	home := filepath.Join(t.TempDir(), "用户 O'Brien")
	local, roaming := filepath.Join(home, "AppData", "Local"), filepath.Join(home, "AppData", "Roaming")
	for key, value := range map[string]string{"USERPROFILE": home, "LOCALAPPDATA": local, "APPDATA": roaming, "XDG_DATA_HOME": "ignored-on-windows", "KOTO_HOME": "", "CLAUDE_CONFIG_DIR": "", "PI_CODING_AGENT_DIR": "", "CODEX_HOME": "", "DSH_HOME": ""} {
		t.Setenv(key, value)
	}
	d := PlatformDefaults()
	for label, pair := range map[string][2]string{
		"app":         {d.DataDir, filepath.Join(local, "koto")},
		"claude":      {d.ClaudeRoot, filepath.Join(home, ".claude")},
		"pi":          {d.PiRoot, filepath.Join(home, ".pi", "agent")},
		"codex":       {d.CodexHome, filepath.Join(home, ".codex")},
		"dsh":         {d.DshHome, filepath.Join(home, ".dsh")},
		"antigravity": {d.AntigravityRoot, filepath.Join(home, ".gemini", "antigravity-cli")},
		"opencode":    {d.OpenCodeRoot, filepath.Join("ignored-on-windows", "opencode")},
		"desktop":     {d.ClaudeDesktopDirs[0], filepath.Join(roaming, "Claude")},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s: %q, want %q", label, pair[0], pair[1])
		}
	}
	custom := filepath.Join(home, "custom [会话]")
	t.Setenv("XDG_DATA_HOME", custom)
	for _, key := range []string{"KOTO_HOME", "CLAUDE_CONFIG_DIR", "PI_CODING_AGENT_DIR", "CODEX_HOME", "DSH_HOME"} {
		t.Setenv(key, custom)
	}
	d = PlatformDefaults()
	if d.OpenCodeRoot != filepath.Join(custom, "opencode") {
		t.Fatalf("OpenCode XDG override changed: %q", d.OpenCodeRoot)
	}
	for _, actual := range []string{d.DataDir, d.ClaudeRoot, d.PiRoot, d.CodexHome, d.DshHome} {
		if actual != custom {
			t.Fatalf("root override changed: %q", actual)
		}
	}
}
