package main

import (
	"errors"
	"strings"
	"testing"
)

func TestWebViewRuntimeStartup(t *testing.T) {
	failure := errors.New("unavailable")
	for _, tt := range []struct {
		name, version         string
		confirm               bool
		promptErr, openErr    error
		wantReady, wantPrompt bool
		wantOpen, wantError   bool
	}{
		{name: "installed", version: "140.0.3485.54", wantReady: true},
		{name: "missing and dismissed", wantPrompt: true},
		{name: "missing and accepted", confirm: true, wantPrompt: true, wantOpen: true},
		{name: "empty lookup", version: " \n", wantPrompt: true},
		{name: "prompt failed", promptErr: failure, wantPrompt: true, wantError: true},
		{name: "browser failed", confirm: true, openErr: failure, wantPrompt: true, wantOpen: true, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			prompted, opened := false, false
			ready, err := checkWebViewRuntime(tt.version, "en", func(title, body string) (bool, error) {
				prompted = true
				if title == "" || !strings.Contains(body, "Microsoft Edge WebView2 Runtime") {
					t.Fatalf("missing startup guidance: %q, %q", title, body)
				}
				return tt.confirm, tt.promptErr
			}, func(url string) error {
				opened = true
				if url != webViewRuntimeDownload {
					t.Fatalf("unexpected destination %q", url)
				}
				return tt.openErr
			})
			if ready != tt.wantReady || prompted != tt.wantPrompt || opened != tt.wantOpen || (err != nil) != tt.wantError {
				t.Fatalf("ready=%v prompted=%v opened=%v error=%v", ready, prompted, opened, err)
			}
		})
	}
}

func TestRuntimeNoticeLocale(t *testing.T) {
	for _, tt := range []struct{ preference, system, want string }{
		{"de", "zh-CN", "de"}, {"zh", "en-US", "zh"}, {"zh-TW", "zh-CN", "zh-TW"},
		{"ja", "de-DE", "ja"}, {"en", "ja-JP", "en"},
		{"system", "zh-CN", "zh"}, {"system", "zh_Hans_CN", "zh"},
		{"system", "zh-Hant", "zh-TW"}, {"system", "zh-TW", "zh-TW"},
		{"system", "zh-HK", "zh-TW"}, {"system", "zh-MO", "zh-TW"},
		{"system", "ja-JP", "ja"}, {"system", "de-DE", "de"},
		{"system", "fr-FR", "en"}, {"", "", "en"}, {"invalid", "de-AT", "de"},
	} {
		if got := runtimeNoticeLocale(tt.preference, tt.system); got != tt.want {
			t.Errorf("locale(%q, %q) = %q; want %q", tt.preference, tt.system, got, tt.want)
		}
	}
	seen := map[string]bool{}
	for _, locale := range []string{"en", "zh", "zh-TW", "ja", "de"} {
		title, body := webViewRuntimeNotice(locale)
		if !strings.Contains(title, "koto") || !strings.Contains(body, "Microsoft Edge WebView2 Runtime") || seen[body] {
			t.Errorf("missing or duplicate %s notice: %q / %q", locale, title, body)
		}
		seen[body] = true
	}
}
