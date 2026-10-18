package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLanguagePreference(t *testing.T) {
	t.Setenv("KOTO_APP_SANDBOX", t.TempDir())
	s := loadPrefs()
	if s.Get().Language != "system" {
		t.Fatal("new preferences must follow the system")
	}
	for _, language := range []string{"en", "zh", "zh-TW", "ja", "de", "system"} {
		p := s.Get()
		p.Language = language
		if err := s.Set(p); err != nil {
			t.Fatal(err)
		}
		if got := loadPrefs().Get().Language; got != language {
			t.Fatalf("language persisted as %s, want %s", got, language)
		}
	}
	if err := os.WriteFile(s.path, []byte(`{"language":"unknown"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if loadPrefs().Get().Language != "system" {
		t.Fatal("unknown preferences must follow the system")
	}
	// A failed write must not claim that an unpersisted preference is active.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	s.path = filepath.Join(blocker, "prefs.json")
	p := s.Get()
	p.Language = "ja"
	if s.Set(p) == nil || s.Get().Language != "system" {
		t.Fatal("failed preference write changed active state")
	}
}

func TestAgentOrderPersistsIndependently(t *testing.T) {
	t.Setenv("KOTO_APP_SANDBOX", t.TempDir())
	s := loadPrefs()
	oldSettings := s.Get()
	ids := []string{"cursor", "future-agent", "codex", "cursor", ""}
	if err := s.SetAgentOrder(ids); err != nil {
		t.Fatal(err)
	}
	ids[0] = "mutated"
	got := s.Get()
	got.AgentOrder[0] = "external-mutation"
	if strings.Join(s.Get().AgentOrder, ",") != "cursor,future-agent,codex" {
		t.Fatal("order leaked mutable storage")
	}
	oldSettings.Appearance = "dark"
	if err := s.Set(oldSettings); err != nil {
		t.Fatal(err)
	}
	reopened := loadPrefs().Get()
	if reopened.Appearance != "dark" || strings.Join(reopened.AgentOrder, ",") != "cursor,future-agent,codex" {
		t.Fatal("settings or restart replaced custom order")
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	s.path = filepath.Join(blocker, "prefs.json")
	if s.SetAgentOrder([]string{"pi"}) == nil || strings.Join(s.Get().AgentOrder, ",") != "cursor,future-agent,codex" {
		t.Fatal("failed order write changed active preferences")
	}
}
