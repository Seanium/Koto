package main

// App preferences are independent of the derived index. They live beside the
// sandbox in development and in the user config directory otherwise.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Prefs struct {
	// Terminal / Editor: an opener id from the preset tables (openers.go); "" or not installed = the
	// first installed one. *Custom: the template for id "custom".
	Terminal       string `json:"terminal"`
	TerminalCustom string `json:"terminalCustom"`
	Editor         string `json:"editor"`
	EditorCustom   string `json:"editorCustom"`
	// Appearance: "light", "dark", or "" (follow the system).
	Appearance string `json:"appearance"`
	Language   string `json:"language"`
	// AgentOrder is independent of the derived session index. Unknown IDs survive upgrades.
	AgentOrder []string `json:"agentOrder,omitempty"`
	// Main window frame, restored at launch (zero = default).
	Window *WindowFrame `json:"window,omitempty"`
}

type WindowFrame struct{ X, Y, W, H int }

var defaultPrefs = Prefs{Language: "system"}

type prefStore struct {
	mu   sync.Mutex
	path string
	p    Prefs
}

func prefsPath() string {
	if s := os.Getenv("KOTO_APP_SANDBOX"); s != "" {
		return filepath.Join(s, "app-prefs.json")
	}
	if s := os.Getenv("KOTO_APP_DATA_DIR"); s != "" {
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
	p := s.p
	p.AgentOrder = append([]string(nil), p.AgentOrder...)
	return p
}

func (s *prefStore) Set(p Prefs) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Sidebar order has its own atomic setter; a stale Settings form must not replace it.
	p.AgentOrder = s.p.AgentOrder
	if p.Window == nil {
		p.Window = s.p.Window // the frontend does not send it
	}
	p.Language = normalLanguage(p.Language)
	return s.saveLocked(p)
}

func (s *prefStore) SetAgentOrder(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.p
	p.AgentOrder = nil
	seen := make(map[string]bool)
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			p.AgentOrder = append(p.AgentOrder, id)
		}
	}
	return s.saveLocked(p)
}

func (s *prefStore) saveLocked(p Prefs) error {
	if s.path == "" {
		s.p = p
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	// Publish one complete file. Interruption before the rename leaves the old preferences intact.
	f, err := os.CreateTemp(filepath.Dir(s.path), ".prefs-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	s.p = p
	return nil
}
