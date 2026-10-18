package koto

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/connectors/claudecode"
	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/store"
)

func deskRoots(dirs ...string) []string {
	var roots []string
	for _, dir := range dirs {
		roots = append(roots, filepath.Join(dir, "local-agent-mode-sessions"))
	}
	return roots
}

// Include directory entries as well as bytes, so a rejected copy cannot leave
// a new task, transcript, attachment directory, or metadata file behind.
func coworkFiles(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, root := range roots {
		must(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				files[path] = "directory"
			} else {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				files[path] = core.SHA256Hex(data)
			}
			return nil
		}))
	}
	return files
}

func TestCoworkSourceClassificationAndStalePlans(t *testing.T) {
	s := newSandbox(t)
	desk := filepath.Join(s.Root, "Claude-3p")
	root := filepath.Join(desk, "local-agent-mode-sessions", "gateway", "org", "local_task", ".claude")
	session := "22222222-2222-4333-8444-555555555555"
	writeCcSession(t, root, session, true)
	before := coworkFiles(t, deskRoots(desk)...)
	a := s.app(enable("claude-code", "pi"), func(c *Config) { c.NoDesktop, c.ClaudeDesktopDirs = false, []string{desk} })
	api := newAPI(a)
	api.Reindex(ctx)
	roots := claudecode.DiscoverCoworkRoots([]string{desk})
	eq(t, len(roots), 1)
	c := a.Registry.Get(roots[0].ID)
	ok(t, c.Source != nil && c.Sink == nil && c.Capabilities.Write == nil)
	eq(t, c.ProfileKind, "cowork")
	eq(t, engine.ProfileVariant(c), "desktop")
	ok(t, engine.ResumeArgv(c, "claude-code", session) == nil)
	for _, p := range api.Profiles(ctx) {
		if p.ID == c.ID {
			ok(t, p.Enabled && !p.Writable && !p.AccountSelectable)
			eq(t, p.Variant, "desktop")
			eq(t, p.Accounts[0].Kind, "api-configuration")
		}
	}
	rows := a.SearchThreadsFiltered("", "claude-code/desktop", "", "", "", 100)
	eq(t, len(rows), 1)
	meta := rows[0].Bindings[0].Meta
	eq(t, meta["frontend"], any("claude-desktop"))
	eq(t, meta["desktopMode"], any("cowork"))
	eq(t, meta["desktopProfile"], any("Claude-3p"))
	eq(t, len(a.SearchThreadsFiltered("", "claude-code/cli", "", "", "", 100)), 0)
	plan := &engine.DeliveryPlan{Mode: "create", Sink: c.ID, Harness: c.Harness, Content: "must not write", Target: engine.DeliveryTarget{Profile: c.ID, Root: c.Root, Path: filepath.Join(root, "new.jsonl")}}
	_, err := a.Engine.ApplyPlan(ctx, c, plan)
	ok(t, err != nil, "read-only target must reject a previously prepared plan")
	old := claudecode.New(claudecode.Options{ID: c.ID, Root: t.TempDir()})
	old.Root = c.Root // A held pointer from before the registry became read-only.
	_, err = a.Engine.ApplyPlan(ctx, old, plan)
	ok(t, err != nil, "stale writable connector cannot bypass registered read-only target")
	eq(t, core.ToJSON(coworkFiles(t, deskRoots(desk)...)), core.ToJSON(before))
	result, err := api.Carry(ctx, CarryRequest{Selector: core.SessionSelector(c.ID, c.Harness, session), To: "pi"})
	must(t, err)
	ok(t, result.Applied && result.Plan.Target.Session != session)
	ok(t, len(result.Plan.Records) > 0, "Cowork source must remain copyable")
	eq(t, core.ToJSON(coworkFiles(t, deskRoots(desk)...)), core.ToJSON(before))
}

func TestCoworkPrimaryAndExtraRootsCannotEnableWrites(t *testing.T) {
	s := newSandbox(t)
	desk := filepath.Join(s.Root, "Claude")
	root := filepath.Join(desk, "local-agent-mode-sessions", "account", "org", "local_task", ".claude")
	writeCcSession(t, root, "", true)
	before := coworkFiles(t, desk)
	a := s.app(enable("claude-code"), func(c *Config) { c.ClaudeRoot = root })
	cs := a.Registry.ByHarness("claude-code")
	eq(t, len(cs), 1)
	c := cs[0]
	eq(t, c.ProfileKind, "cowork")
	eq(t, engine.ProfileVariant(c), "desktop")
	ok(t, !c.Default && c.Source != nil && c.Sink == nil)
	_, err := a.AddRoot("claude-code", root)
	ok(t, err != nil && strings.Contains(err.Error(), "already registered"))
	eq(t, len(a.Registry.ByHarness("claude-code")), 1)
	// The same native layout is source-only even if desktop discovery was off.
	other := filepath.Join(desk, "local-agent-mode-sessions", "account", "org", "local_other", ".claude")
	writeCcSession(t, other, "", true)
	info, err := a.AddRoot("claude-code", other)
	must(t, err)
	ok(t, info.Source && !info.Sink)
	eq(t, info.Variant, "desktop")
	api := newAPI(a)
	eq(t, len(api.Reindex(ctx).Errors), 0)
	for _, dryRun := range []bool{true, false} {
		_, err := api.Carry(ctx, CarryRequest{Selector: core.SessionSelector(c.ID, c.Harness, CCSession), To: info.ID, DryRun: dryRun})
		ok(t, err != nil && strings.Contains(err.Error(), "cannot be a delivery target"))
	}
	for path, hash := range before {
		eq(t, coworkFiles(t, desk)[path], hash)
	}
}

func TestCoworkClassificationRefreshesExistingIndex(t *testing.T) {
	s := newSandbox(t)
	desk := filepath.Join(s.Root, "Claude-3p")
	root := filepath.Join(desk, "local-agent-mode-sessions", "gateway", "org", "local_task", ".claude")
	writeCcSession(t, root, "", true)
	before := coworkFiles(t, desk)
	a := s.app(enable("claude-code"), func(c *Config) { c.NoDesktop, c.ClaudeDesktopDirs = false, []string{desk} })
	api := newAPI(a)
	eq(t, len(api.Reindex(ctx).Errors), 0)
	profile := claudecode.DiscoverCoworkRoots([]string{desk})[0]
	c := a.Registry.Get(profile.ID)
	row, found := a.Store.Session("claude-code", CCSession, c.ID)
	ok(t, found)
	for _, key := range []string{"frontend", "desktopMode", "desktopProfile"} {
		delete(row.Meta, key)
	}
	core.Obj(row.Meta["sourceProfile"])["variant"] = "cowork"
	row.Turns = 999
	must(t, a.Store.PutSession(row))
	old := *c
	old.Version = strings.TrimSuffix(c.Version, "/cowork@1")
	var decoders map[string]string
	ok(t, a.Store.Setting("index-decoders", &decoders))
	decoders[c.ID] = store.DecoderFingerprint(&old)
	a.Store.SetSetting("index-decoders", decoders)
	// A normal scan, with unchanged native bytes, repairs the disposable index.
	eq(t, len(api.Reindex(ctx).Errors), 0)
	row, found = a.Store.Session("claude-code", CCSession, c.ID)
	ok(t, found && row.Turns != 999)
	eq(t, core.S(row.Meta["frontend"]), "claude-desktop")
	eq(t, core.S(row.Meta["desktopMode"]), "cowork")
	eq(t, core.S(core.Obj(row.Meta["sourceProfile"])["variant"]), "desktop")
	eq(t, len(a.SearchThreadsFiltered("", "claude-code/desktop", "", "", "", 100)), 1)
	eq(t, api.Reindex(ctx).Changed, 0)
	eq(t, core.ToJSON(coworkFiles(t, desk)), core.ToJSON(before))
}
