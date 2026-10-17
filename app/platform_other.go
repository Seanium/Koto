//go:build !darwin

package main

// Windows / Linux: native title bar and the packaged emoji tray icon.

import (
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func mainWindowOptions() application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Name: "main", Title: windowTitle(), Width: 1280, Height: 800, MinWidth: 1120, MinHeight: 680, URL: "/",
		DefaultContextMenuDisabled: true,
		BackgroundColour:           application.NewRGB(255, 255, 255),
	}
}

// Other platforms retain the icon embedded in their native bundle resources.
func platformApplicationIcon() []byte { return nil }

func setTrayIcon(t *application.SystemTray, png []byte) {
	t.SetIcon(png)
}

func setAppearance(string) {}

// No menu bar; 设置 opens from the main window's toolbar and the tray icon.
func appMenu(*UI) *application.Menu { return nil }

// ---------------------------------------------------------------- openers (see openers.go). Untested in v1.

var fileManagerName = func() string {
	if runtime.GOOS == "windows" {
		return "资源管理器"
	}
	return "文件管理器"
}()

func appInstalled(program string) bool { _, err := exec.LookPath(program); return err == nil }

func terminalPresets() []Opener {
	if runtime.GOOS == "windows" {
		return []Opener{
			{ID: "system", Name: "系统默认", argv: []string{"cmd", "/c", "start", "", "cmd", "/k", "{cmd}"}},
			{ID: "wt", Name: "Windows Terminal", detect: "wt", argv: []string{"wt", "-d", "{cwd}", "cmd", "/k", "{cmd}"}},
		}
	}
	keep := "{cmd}; exec \"${SHELL:-/bin/sh}\""
	return []Opener{
		{ID: "system", Name: "系统默认", detect: "x-terminal-emulator", argv: []string{"x-terminal-emulator", "-e", "sh", "-c", keep}},
		{ID: "gnome-terminal", Name: "GNOME Terminal", detect: "gnome-terminal", argv: []string{"gnome-terminal", "--working-directory={cwd}", "--", "sh", "-c", keep}},
		{ID: "konsole", Name: "Konsole", detect: "konsole", argv: []string{"konsole", "--workdir", "{cwd}", "-e", "sh", "-c", keep}},
		{ID: "ghostty", Name: "Ghostty", detect: "ghostty", argv: []string{"ghostty", "--working-directory={cwd}", "-e", "sh", "-c", keep}},
	}
}

func editorPresets() []Opener {
	return []Opener{
		{ID: "vscode", Name: "VS Code", detect: "code", argv: []string{"code", "{path}"}},
		{ID: "cursor", Name: "Cursor", detect: "cursor", argv: []string{"cursor", "{path}"}},
		{ID: "zed", Name: "Zed", detect: "zed", argv: []string{"zed", "{path}"}},
	}
}

func revealPath(p string) bool {
	if runtime.GOOS == "windows" {
		return run([]string{"explorer", "/select,", p})
	}
	return run([]string{"xdg-open", filepath.Dir(p)})
}
