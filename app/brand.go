package main

// Product identity, in one place. The name, description and icons below are everything that says
// "koto"; the command line next to it is `koto` too (cmd/koto).
//
// macOS renders the system 💬 emoji in memory for its running Dock and menu-bar icons.
// The packaged Apple emoji PNGs serve Finder, Windows and Linux (see ASSETS.md).
// brand/appicon.png feeds the ICNS and ICO bundles.
// Regenerate packaged assets with scripts/generate-icons.cjs.

import _ "embed"

const (
	productName        = "koto"
	productDescription = "Continue a coding-agent session in another agent"
)

//go:embed brand/trayicon.png
var trayIconPNG []byte

// Preview builds (scripts/preview-mac.sh) bake these in with -ldflags -X so the bundle runs on the
// project sandbox even when started from Finder, where no environment variables are set.
var (
	previewSandbox string // like KOTO_APP_SANDBOX
	previewPath    string // PATH for the harness CLIs the core runs (codex), as the build shell had it
)

// windowTitle is the main window title; a preview says it runs on test data.
func windowTitle() string {
	if previewSandbox != "" {
		return productName + "（预览 · 测试数据）"
	}
	return productName
}
