package main

import "strings"

const webViewRuntimeDownload = "https://developer.microsoft.com/microsoft-edge/webview2/#download-section"

// Run before creating a window: Wails exits inside native window creation when
// WebView2 is missing, before app.Run can return an error to us.
func checkWebViewRuntime(version, locale string, prompt func(string, string) (bool, error), open func(string) error) (bool, error) {
	if strings.TrimSpace(version) != "" {
		return true, nil
	}
	title, body := webViewRuntimeNotice(locale)
	confirmed, err := prompt(title, body)
	if err != nil || !confirmed {
		return false, err
	}
	return false, open(webViewRuntimeDownload)
}

// Keep the fallback rules aligned with frontend/src/i18n/core.ts. A saved App
// preference takes precedence over the Windows display language.
func runtimeNoticeLocale(preference, system string) string {
	switch preference {
	case "en", "zh", "zh-TW", "ja", "de":
		return preference
	}
	parts := strings.Split(strings.ToLower(strings.ReplaceAll(system, "_", "-")), "-")
	switch parts[0] {
	case "zh":
		for _, part := range parts[1:] {
			switch part {
			case "tw", "hk", "mo", "hant":
				return "zh-TW"
			}
		}
		return "zh"
	case "ja", "de":
		return parts[0]
	default:
		return "en"
	}
}

func webViewRuntimeNotice(locale string) (title, body string) {
	switch locale {
	case "zh":
		return "koto 需要 WebView2", "打开 koto 需要 Microsoft Edge WebView2 Runtime。\n安装后，请重新打开 koto。\n\n现在打开 Microsoft 官方下载页面吗？"
	case "zh-TW":
		return "koto 需要 WebView2", "開啟 koto 需要 Microsoft Edge WebView2 Runtime。\n安裝後，請重新開啟 koto。\n\n現在開啟 Microsoft 官方下載頁面嗎？"
	case "ja":
		return "koto には WebView2 が必要です", "koto を開くには Microsoft Edge WebView2 Runtime が必要です。\nインストール後、koto をもう一度開いてください。\n\nMicrosoft の公式ダウンロードページを開きますか？"
	case "de":
		return "koto benötigt WebView2", "Zum Öffnen von koto wird die Microsoft Edge WebView2 Runtime benötigt.\nÖffne koto nach der Installation erneut.\n\nJetzt die offizielle Downloadseite von Microsoft öffnen?"
	default:
		return "koto needs WebView2", "Microsoft Edge WebView2 Runtime is needed to open koto.\nInstall it, then reopen koto.\n\nOpen Microsoft’s official download page now?"
	}
}
