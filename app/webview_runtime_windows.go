//go:build windows

package main

import (
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
)

func platformRuntimeReady(app *application.App, preference string) bool {
	version, _ := app.Env.Info().PlatformInfo["WebView2"].(string)
	system := "en"
	if languages, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME); err == nil && len(languages) > 0 {
		system = languages[0]
	}
	ready, err := checkWebViewRuntime(version, runtimeNoticeLocale(preference, system), func(title, body string) (bool, error) {
		// Windows supplies the native button labels in its display language.
		const yesNoWarning = 0x00000004 | 0x00000030 | 0x00010000
		answer, err := windows.MessageBox(0, windows.StringToUTF16Ptr(body), windows.StringToUTF16Ptr(title), yesNoWarning)
		return answer == 6, err
	}, func(url string) error {
		return windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr(url), nil, nil, windows.SW_SHOWNORMAL)
	})
	if err != nil {
		log.Printf("WebView2 setup: %v (download: %s)", err, webViewRuntimeDownload)
	}
	return ready
}
