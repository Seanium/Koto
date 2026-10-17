package main

// App preferences (not hub state): what to do when the target session looks open, and how long
// "open" lasts. Stored next to the sandbox in development, else in the user config dir.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Prefs struct {
	// LiveMinutes: a session modified within this many minutes counts as open; 0 = never.
	LiveMinutes int `json:"liveMinutes"`
	// Terminal / Editor: an opener id from the preset tables (openers.go); "" or not installed = the
	// first installed one. *Custom: the template for id "custom".
	Terminal       string `json:"terminal"`
	TerminalCustom string `json:"terminalCustom"`
	Editor         string `json:"editor"`
	EditorCustom   string `json:"editorCustom"`
	// Appearance: "light", "dark", or "" (follow the system).
	Appearance string `json:"appearance"`
	Language   string `json:"language"`
	// Main window frame, restored at launch (zero = default).
	Window *WindowFrame `json:"window,omitempty"`
}

type WindowFrame struct{ X, Y, W, H int }

var defaultPrefs = Prefs{LiveMinutes: 2, Language: "system"}

type prefStore struct {
	mu   sync.Mutex
	path string
	p    Prefs
}

func prefsPath() string {
	if s := os.Getenv("KOTO_APP_SANDBOX"); s != "" {
		return filepath.Join(s, "app-prefs.json")
	}
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, productName, "prefs.json")
	}
	return ""
}

func loadPrefs() *prefStore {
	s := &prefStore{path: prefsPath(), p: defaultPrefs}
	if raw, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(raw, &s.p)
	}
	s.p.Language = normalLanguage(s.p.Language)
	return s
}

func normalLanguage(value string) string {
	switch value {
	case "zh", "zh-TW", "en", "ja", "de":
		return value
	default:
		return "system"
	}
}

func (s *prefStore) Get() Prefs {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.p
}

func (s *prefStore) Set(p Prefs) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.Window == nil {
		p.Window = s.p.Window // the frontend does not send it
	}
	if p.LiveMinutes < 0 {
		p.LiveMinutes = 0
	}
	p.Language = normalLanguage(p.Language)
	if s.path == "" {
		s.p = p
		return nil
	}
	_ = os.MkdirAll(filepath.Dir(s.path), 0o755)
	raw, _ := json.MarshalIndent(p, "", "  ")
	if err := os.WriteFile(s.path, raw, 0o644); err != nil {
		return err
	}
	s.p = p
	return nil
}

// liveMs: the open-session window in ms.
func (p Prefs) liveMs() *int64 {
	n := int64(p.LiveMinutes) * 60_000
	return &n
}
