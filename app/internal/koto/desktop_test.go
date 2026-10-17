package koto

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/engine"
)

func writeJSON(t *testing.T, file string, v any) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(file), 0o755))
	b, _ := json.Marshal(v)
	must(t, os.WriteFile(file, b, 0o644))
}

func TestDesktopIndexAndCowork(t *testing.T) {
	s := newSandbox(t)
	writeCcSession(t, s.Claude, "", true)
	desk := filepath.Join(s.Root, "ClaudeDesktop")
	writeJSON(t, filepath.Join(desk, "claude-code-sessions", "acct", "org", "local_abc.json"), map[string]any{"sessionId": "local_abc", "cliSessionId": CCSession, "title": "Desktop title", "isArchived": false})
	cowork := filepath.Join(desk, "local-agent-mode-sessions", "acct", "org", "local_cw1", ".claude")
	writeCcSession(t, cowork, "22222222-2222-4333-8444-555555555555", true)
	a := s.app(enable("claude-code"), func(c *Config) { c.NoDesktop, c.ClaudeDesktopDirs = false, []string{desk} })
	ok(t, a.Registry.Get("claude-code@cowork:cw1") != nil, "cowork root registered")
	scan(t, a)
	b := binding(t, a.Hub, "claude-code", CCSession)
	eq(t, b.Meta["desktopTitle"], any("Desktop title"))
	eq(t, b.Meta["frontend"], any("claude-desktop"))
	eq(t, binding(t, a.Hub, "claude-code", "22222222-2222-4333-8444-555555555555").Connector, "claude-code@cowork:cw1")
}

func TestDesktopProfiles(t *testing.T) {
	s := newSandbox(t)
	writeCcSession(t, s.Claude, "", true)
	acct, gw := filepath.Join(s.Root, "Claude"), filepath.Join(s.Root, "Claude-3p")
	writeJSON(t, filepath.Join(gw, "claude-code-sessions", "acct", "org", "local_abc.json"), map[string]any{"sessionId": "local_abc", "cliSessionId": CCSession, "title": "via gateway"})
	writeCcSession(t, filepath.Join(acct, "local-agent-mode-sessions", "acct", "org", "local_cw1", ".claude"), "22222222-2222-4333-8444-555555555555", true)
	writeCcSession(t, filepath.Join(gw, "local-agent-mode-sessions", "acct", "org", "local_cw1", ".claude"), "33333333-2222-4333-8444-555555555555", true)
	a := s.app(enable("claude-code"), func(c *Config) { c.NoDesktop, c.ClaudeDesktopDirs = false, []string{acct, gw} })
	ok(t, a.Registry.Get("claude-code@cowork:cw1") != nil && a.Registry.Get("claude-code@cowork-3p:cw1") != nil)
	scan(t, a)
	b := binding(t, a.Hub, "claude-code", CCSession)
	eq(t, b.Meta["desktopTitle"], any("via gateway"))
	eq(t, b.Meta["desktopProfile"], any("Claude-3p"))
	eq(t, binding(t, a.Hub, "claude-code", "33333333-2222-4333-8444-555555555555").Connector, "claude-code@cowork-3p:cw1")
	// a Cowork session from the account profile can be carried into the gateway profile's root
	r := carry(t, a, engine.CarryOptions{Selector: "claude-code:22222222", To: "claude-code@cowork-3p:cw1"})
	ok(t, r.Applied && strings.HasPrefix(r.Plan.Target.Path, gw))
}

// A session written from Desktop's Code tab says so even when Desktop's index has no entry for it,
// and the list carries the marker; a CLI session does not.
func TestDesktopEntrypointMarker(t *testing.T) {
	s := newSandbox(t)
	other := "33333333-2222-4333-8444-555555555555"
	writeCcSession(t, s.Claude, "", true)
	f := writeCcSession(t, s.Pi+"-unused", other, true) // build the fixture, then re-home it with entrypoint claude-desktop
	b, err := os.ReadFile(f)
	must(t, err)
	dst := filepath.Join(filepath.Dir(writeCcSession(t, s.Claude, "", true)), other+".jsonl")
	must(t, os.WriteFile(dst, []byte(strings.ReplaceAll(strings.ReplaceAll(string(b), `"entrypoint":"cli"`, `"entrypoint":"claude-desktop"`), s.Pi+"-unused", s.Claude)), 0o644))
	a := s.app(enable("claude-code"))
	api := newAPI(a)
	api.Reindex(ctx)
	marks := map[string]any{}
	for _, l := range api.Threads(0) {
		for _, x := range l.Bindings {
			marks[x.Session] = x.Meta["frontend"]
		}
	}
	eq(t, marks[other], any("claude-desktop"))
	eq(t, marks[CCSession], nil)
}
