package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/w32"
)

func setAppearance(ui *UI, _ string) {
	if ui == nil || ui.main == nil {
		return
	}
	application.InvokeAsync(func() {
		window := ui.main.NativeWindow()
		if window == nil {
			return
		}
		// Read the current preference on the UI thread so a queued older change
		// cannot replace a newer one. High-contrast colours remain OS-controlled.
		dark := nativeWindowDark(ui.prefs.Get().Appearance, w32.IsCurrentlyDarkMode(), w32.IsCurrentlyHighContrastMode())
		w32.SetTheme(uintptr(window), dark)
	})
}

func bindWindowAppearance(ui *UI) {
	refresh := func() { setAppearance(ui, ui.prefs.Get().Appearance) }
	ui.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) { refresh() })
	ui.app.Event.OnApplicationEvent(events.Windows.SystemThemeChanged, func(*application.ApplicationEvent) { refresh() })
	ui.main.OnWindowEvent(events.Common.WindowShow, func(*application.WindowEvent) { refresh() })
}
