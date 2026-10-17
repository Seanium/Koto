//go:build !windows

package main

import "github.com/wailsapp/wails/v3/pkg/application"

func platformRuntimeReady(_ *application.App, _ string) bool { return true }
