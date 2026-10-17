package main

import (
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Wails' Windows window bounds and screen work areas use the same DIP space.
// Keep the complete frame on its current monitor, including the title bar and
// taskbar clearance. A restored frame can belong to a disconnected monitor.
func fitWindowFrame(frame, area application.Rect, minWidth, minHeight int) (application.Rect, int, int) {
	if area.Width <= 0 || area.Height <= 0 {
		return frame, minWidth, minHeight
	}
	minWidth, minHeight = min(minWidth, area.Width), min(minHeight, area.Height)
	frame.Width = min(max(frame.Width, minWidth), area.Width)
	frame.Height = min(max(frame.Height, minHeight), area.Height)
	frame.X = max(area.X, min(frame.X, area.X+area.Width-frame.Width))
	frame.Y = max(area.Y, min(frame.Y, area.Y+area.Height-frame.Height))
	return frame, minWidth, minHeight
}

func windowWorkArea(w *application.WebviewWindow) (application.Rect, bool) {
	if runtime.GOOS == "windows" {
		if screen, err := w.GetScreen(); err == nil && screen != nil && !screen.WorkArea.IsEmpty() {
			return screen.WorkArea, true
		}
	}
	return application.Rect{}, false
}

func windowMinimum(w *application.WebviewWindow, width, height int) (int, int) {
	if area, ok := windowWorkArea(w); ok {
		return min(width, area.Width), min(height, area.Height)
	}
	return width, height
}

func enforceWindowFrame(w *application.WebviewWindow, minWidth, minHeight int) {
	if w.IsFullscreen() || w.IsMinimised() {
		return
	}
	if area, ok := windowWorkArea(w); ok {
		bounds := w.Bounds()
		if bounds.Width > 0 && bounds.Height > 0 {
			frame, width, height := fitWindowFrame(bounds, area, minWidth, minHeight)
			w.SetMinSize(width, height)
			// Maximised frames may include invisible borders outside the work area.
			// Windows owns their geometry; clamp again when the window is restored.
			if !w.IsMaximised() && bounds != frame {
				w.SetBounds(frame)
			}
			return
		}
	}
	width, height := w.Size()
	if width < minWidth || height < minHeight {
		w.SetSize(max(width, minWidth), max(height, minHeight))
	}
	w.SetMinSize(minWidth, minHeight)
}
