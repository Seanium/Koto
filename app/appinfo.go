package main

import (
	_ "embed"
	"strings"
)

// Embed the release source of truth so development and packaged builds report
// the same version without depending on a runtime file or linker override.
//
//go:embed VERSION
var appVersionFile string

type AppInfo struct {
	Version    string `json:"version"`
	ProjectURL string `json:"projectURL"`
}

func (s *AppService) AppInfo() AppInfo {
	return AppInfo{Version: strings.TrimSpace(appVersionFile), ProjectURL: "https://github.com/Seanium/Koto"}
}
