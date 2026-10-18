package koto

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/core"
	"koto/internal/engine"
)

func TestProfilesIsolateSameNativeIDAndCopyDefaults(t *testing.T) {
	s := newSandbox(t)
	first := writeCcSession(t, s.Claude, "", true)
	other := filepath.Join(s.Root, "other account [中文]")
	second := writeCcSession(t, other, "", true)
	original, err := os.ReadFile(first)
	must(t, err)
	secondBytes := []byte(strings.ReplaceAll(string(original), "Hello", "OTHER ACCOUNT"))
	// A second root deliberately reuses every native ID but has different content.
	secondBytes = append(secondBytes, []byte("\n{\"type\":\"user\",\"uuid\":\"other-only\",\"message\":{\"role\":\"user\",\"content\":\"SECOND ROOT SECRET\"}}\n")...)
	must(t, os.WriteFile(second, secondBytes, 0600))
	a := s.app(enable("claude-code", "pi"))
	p, err := a.AddRoot("claude-code", other)
	must(t, err)
	api := newAPI(a)
	report := api.Reindex(ctx)
	eq(t, len(report.Errors), 0)
	rows := a.SearchThreadsFiltered("", "claude-code", "", "", "", 0)
	eq(t, len(rows), 2)
	_, err = a.OpenThread(ctx, "claude-code:"+CCSession)
	ok(t, err != nil && strings.Contains(err.Error(), "multiple"), err)
	_, err = api.Carry(ctx, CarryRequest{Selector: "claude-code:" + CCSession, To: "pi"})
	ok(t, err != nil, "ambiguous short selector must not write")
	left, right := core.SessionSelector(a.Registry.Get("claude-code").ID, "claude-code", CCSession), core.SessionSelector(p.ID, "claude-code", CCSession)
	for _, sel := range []string{left, right} {
		page, err := api.Thread(ctx, sel, 0, "")
		must(t, err)
		eq(t, len(page.Bindings), 1)
		has := strings.Contains(core.ToJSON(page.Events), "SECOND ROOT SECRET")
		eq(t, has, sel == right)
	}
	r, err := api.Carry(ctx, CarryRequest{Selector: right, To: "claude-code", DryRun: true})
	must(t, err)
	eq(t, r.Plan.Sink, p.ID)
	eq(t, r.Plan.Target.Profile, p.ID)
	eq(t, r.Plan.Target.Root, other)
	r, err = api.Carry(ctx, CarryRequest{Selector: right, To: "claude-code", TargetProfile: "claude-code", DryRun: true})
	must(t, err)
	eq(t, r.Plan.Sink, a.Registry.Get("claude-code").ID)
	_, err = api.Carry(ctx, CarryRequest{Selector: right, To: "pi", TargetProfile: "claude-code", DryRun: true})
	ok(t, err != nil, "target profile from another product rejected")
	_, err = api.Carry(ctx, CarryRequest{Selector: right, To: "pi", TargetAccount: "invented", DryRun: true})
	ok(t, err != nil, "unsupported target account rejected")
	comparison, err := api.Analyze(ctx, []string{left, right})
	must(t, err)
	eq(t, len(comparison.Sessions), 2)
	profiles := a.Profiles(ctx)
	eq(t, len(profiles), 3)
	for _, row := range rows {
		eq(t, core.ReadIdentity(row.Bindings[0].Meta).Status, "unknown")
	}
	// A refreshed scan must retain both roots and its decoded identity metadata.
	report = api.Reindex(ctx)
	eq(t, len(report.Errors), 0)
	eq(t, report.Changed, 0)
	eq(t, len(a.ListThreads()), 2)
	got, err := os.ReadFile(first)
	must(t, err)
	eq(t, string(got), string(original))
	got, err = os.ReadFile(second)
	must(t, err)
	eq(t, string(got), string(secondBytes))
}

func TestProfileCopyRequestCannotChangeDestination(t *testing.T) {
	s := newSandbox(t)
	writeCcSession(t, s.Claude, "", true)
	a := s.app(enable("claude-code", "pi"))
	root := filepath.Join(s.Root, "second pi")
	must(t, os.MkdirAll(root, 0700))
	p, err := a.AddRoot("pi", root)
	must(t, err)
	api := newAPI(a)
	api.Reindex(ctx)
	req := CarryRequest{Selector: "claude-code:" + CCSession, To: "pi", DryRun: true, RequestID: "profile-freeze"}
	_, err = api.Carry(ctx, req)
	must(t, err)
	req.TargetProfile = p.ID
	_, err = api.Carry(ctx, req)
	ok(t, err != nil && strings.Contains(err.Error(), "request changed"), err)
	// Engine scans keep identical native IDs separate outside per-session views too.
	r, err := a.Engine.Scan(ctx, engine.ScanOptions{})
	must(t, err)
	eq(t, len(r.Errors), 0)
}

func TestCLIProfileProofDoesNotClaimDesktopOwner(t *testing.T) {
	s := newSandbox(t)
	writeCcSession(t, s.Claude, "", true)
	desktop := filepath.Join(s.Root, "Claude")
	writeJSON(t, filepath.Join(desktop, "claude-code-sessions", "account-one", "org-one", "local_abc.json"), map[string]any{"sessionId": "local_abc", "cliSessionId": CCSession})
	a := s.app(enable("claude-code"), func(c *Config) { c.NoDesktop = false; c.ClaudeDesktopDirs = []string{desktop} })
	eq(t, len(a.ProfileIDsForRoot("claude-code", s.Claude)), 2)
	ids := a.ProfileIDsForRoot("claude-code", s.Claude, "cli")
	eq(t, len(ids), 1)
	eq(t, ids[0], a.Registry.Get("claude-code").ID)
	eq(t, len(newAPI(a).Reindex(ctx).Errors), 0)
	row, err := a.ResolveSession(core.SessionSelector(ids[0], "claude-code", CCSession))
	must(t, err)
	eq(t, core.ReadIdentity(row.Meta).Status, "unknown")
}

func TestCursorAppVariantsAreSeparateProfiles(t *testing.T) {
	s := newSandbox(t)
	appRoot, ideRoot := filepath.Join(s.Root, "Cursor"), filepath.Join(s.Root, "Cursor IDE")
	must(t, os.MkdirAll(appRoot, 0700))
	must(t, os.MkdirAll(ideRoot, 0700))
	a := s.app(enable("cursor"), func(c *Config) {
		c.NoDesktop = false
		c.CursorAppRoot = appRoot
	})
	ide, err := a.AddRoot("cursor", ideRoot, "ide")
	must(t, err)
	profiles := a.Profiles(ctx)
	eq(t, len(profiles), 3)
	ids := map[string]string{}
	for _, p := range profiles {
		ids[p.Variant] = p.ID
		eq(t, p.Writable, true)
		eq(t, p.Default, p.Variant == "cli")
	}
	eq(t, ids["ide"], ide.ID)
	ok(t, ids["cli"] != ide.ID)
	ok(t, a.Registry.Get(engine.ProfileID("cursor", appRoot, "ide")) != nil)
	b := s.app(enable("cursor"))
	found := false
	for _, p := range b.Profiles(ctx) {
		if p.ID == ide.ID {
			found = true
			eq(t, p.Variant, "ide")
			ok(t, p.Writable)
		}
	}
	ok(t, found, "custom App variant persists")
	_, err = a.AddRoot("cursor", filepath.Join(s.Root, "missing"), "invented")
	ok(t, err != nil)
}

func TestProfileAccountEnumerationFailureIsNotNoAccounts(t *testing.T) {
	s := newSandbox(t)
	a := s.app(enable("workbuddy"))
	must(t, os.WriteFile(filepath.Join(s.Root, "workbuddy", "workbuddy.db"), []byte("not a sqlite database"), 0600))
	profiles := a.Profiles(ctx)
	eq(t, len(profiles), 1)
	ok(t, profiles[0].AccountSelectable)
	ok(t, profiles[0].TargetAccountsError != "", "enumeration error was reported as no accounts")
}
