package main

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestFitWindowFrame(t *testing.T) {
	rect := func(x, y, width, height int) application.Rect {
		return application.Rect{X: x, Y: y, Width: width, Height: height}
	}
	for _, tc := range []struct {
		name                string
		frame, area, want   application.Rect
		minWidth, minHeight int
	}{
		{"laptop-taskbar", rect(43, -36, 1280, 800), rect(0, 0, 1366, 728), rect(43, 0, 1280, 728), 1120, 680},
		{"saved-large-monitor", rect(1800, 500, 1800, 1200), rect(0, 0, 1366, 728), rect(0, 0, 1366, 728), 1120, 680},
		{"negative-monitor", rect(-2100, -30, 1280, 800), rect(-1920, 0, 1920, 1040), rect(-1920, 0, 1280, 800), 1120, 680},
		{"top-taskbar", rect(20, 0, 1280, 800), rect(0, 48, 1440, 852), rect(20, 48, 1280, 800), 1120, 680},
		{"scaled-small-workarea", rect(0, 0, 1280, 800), rect(0, 0, 960, 600), rect(0, 0, 960, 600), 960, 600},
		{"preserve-valid-frame", rect(40, 50, 1150, 700), rect(0, 0, 1440, 900), rect(40, 50, 1150, 700), 1120, 680},
		{"repair-too-small", rect(100, 100, 700, 500), rect(0, 0, 1440, 900), rect(100, 100, 1120, 680), 1120, 680},
		{"unavailable-screen", rect(1, 2, 1280, 800), application.Rect{}, rect(1, 2, 1280, 800), 1120, 680},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, minW, minH := fitWindowFrame(tc.frame, tc.area, 1120, 680)
			if got != tc.want || minW != tc.minWidth || minH != tc.minHeight {
				t.Fatalf("frame=%+v min=%dx%d; want %+v min=%dx%d", got, minW, minH, tc.want, tc.minWidth, tc.minHeight)
			}
		})
	}
}
