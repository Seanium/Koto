package claudecode

import (
	"context"
	"encoding/json"
	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/hub"
	"koto/internal/planner"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDesktopDiscoveryUsesRecordedProjectWithoutListingTranscripts(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, ".claude")
	cwd := filepath.Join(base, "workspace 中文")
	scope := filepath.Join(base, "Claude", "claude-code-sessions", "account", "org")
	id := "11111111-1111-4111-8111-111111111111"
	file := filepath.Join(root, "projects", ProjectDir(cwd), id+".jsonl")
	if e := os.MkdirAll(filepath.Dir(file), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(file, []byte("not parsed during discovery\n"), 0600); e != nil {
		t.Fatal(e)
	}
	metadata := filepath.Join(scope, "local_known.json")
	desktopRecord(t, metadata, id, "Known project")
	b, _ := json.Marshal(core.JsonObject{"cliSessionId": id, "cwd": cwd, "originCwd": filepath.Join(base, "old project"), "title": "Known project"})
	if e := os.WriteFile(metadata, b, 0600); e != nil {
		t.Fatal(e)
	}
	// Traversal is sufficient for a known file; listing either directory is not.
	if runtime.GOOS != "windows" {
		for _, p := range []string{filepath.Join(root, "projects"), filepath.Dir(file)} {
			if e := os.Chmod(p, 0111); e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = os.Chmod(p, 0700) })
		}
	}
	c := New(Options{Root: root, DesktopScope: scope})
	refs, e := c.Source.Discover(context.Background())
	if e != nil || len(refs) != 1 || refs[0].Path != file || core.S(refs[0].Meta["desktopTitle"]) != "Known project" {
		t.Fatalf("known metadata lookup: %+v %v", refs, e)
	}
}

func TestDesktopDiscoveryFallsBackForMissingOrMovedProject(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".claude")
	scope := filepath.Join(t.TempDir(), "Claude", "claude-code-sessions", "account", "org")
	for i, id := range []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"} {
		p := filepath.Join(root, "projects", "moved-project", id+".jsonl")
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte("{}\n"), 0600); e != nil {
			t.Fatal(e)
		}
		metadata := filepath.Join(scope, "local_"+id+".json")
		desktopRecord(t, metadata, id, "Legacy")
		if i == 1 {
			b, _ := json.Marshal(core.JsonObject{"cliSessionId": id, "cwd": "stale project"})
			if e := os.WriteFile(metadata, b, 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
	// A metadata-only record does not invent a transcript, nor leak other scopes.
	desktopRecord(t, filepath.Join(scope, "local_absent.json"), "33333333-3333-4333-8333-333333333333", "Absent")
	c := New(Options{Root: root, DesktopScope: scope})
	refs, e := c.Source.Discover(context.Background())
	if e != nil || len(refs) != 2 {
		t.Fatalf("fallback: %+v %v", refs, e)
	}
}

func TestClaudeDiscoveryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, scope := range []string{"", filepath.Join(t.TempDir(), "scope")} {
		c := New(Options{Root: t.TempDir(), DesktopScope: scope})
		if _, e := c.Source.Discover(ctx); e != context.Canceled {
			t.Fatalf("cancelled discovery: %v", e)
		}
	}
}

func TestDesktopResumeDoesNotOpenCLIInAnotherProfile(t *testing.T) {
	t.Setenv(engine.ResumeEnvVar("claude-code"), "")
	root := filepath.Join(t.TempDir(), ".claude")
	id := "11111111-1111-4111-8111-111111111111"
	cli := New(Options{Root: root})
	if got := engine.ResumeArgv(cli, "claude-code", id); strings.Join(got, " ") != "claude --resume "+id {
		t.Fatalf("CLI resume: %v", got)
	}
	for _, profile := range []string{"Claude", "Claude-3p"} {
		scope := filepath.Join(t.TempDir(), profile, "claude-code-sessions", "account", "org")
		c := New(Options{Root: root, ID: "desktop-scope", DesktopScope: scope, Identity: scopeIdentity(profile, "account", "org", profile+"/claude-code-sessions")})
		if got := engine.ResumeArgv(c, "claude-code", id); len(got) != 0 || c.Sink.ResumeCommand() != "" {
			t.Fatalf("%s leaked CLI resume for Desktop account scope: %v", profile, got)
		}
	}
}

func desktopRecord(t *testing.T, path, session, title string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(map[string]any{"cliSessionId": session, "sessionId": strings.TrimSuffix(filepath.Base(path), ".json"), "title": title})
	if e := os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func TestDesktopNamespacesNeverOverwriteSharedSession(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, ".claude")
	id := "11111111-1111-4111-8111-111111111111"
	file := filepath.Join(root, "projects", "project", id+".jsonl")
	if e := os.MkdirAll(filepath.Dir(file), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(file, []byte("{}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	dirs := []string{filepath.Join(base, "Claude"), filepath.Join(base, "Claude-3p")}
	desktopRecord(t, filepath.Join(dirs[0], "claude-code-sessions", "account-a", "org-a", "local_one.json"), id, "account title")
	desktopRecord(t, filepath.Join(dirs[0], "claude-code-sessions", "account-b", "org-b", "local_two.json"), id, "other title")
	desktopRecord(t, filepath.Join(dirs[1], "claude-code-sessions", "api-user", "derived-scope", "local_three.json"), id, "api title")
	profiles := DiscoverDesktopProfiles(dirs, root)
	if len(profiles) != 3 {
		t.Fatalf("profiles=%+v", profiles)
	}
	seen := map[string]bool{}
	titles := map[string]bool{}
	for _, p := range profiles {
		if seen[p.ID] || strings.Contains(p.ID, ":") {
			t.Fatal("invalid profile ID", p.ID)
		}
		seen[p.ID] = true
		c := New(Options{Root: p.Root, ID: p.ID, DesktopScope: p.Scope, Identity: p.Identity})
		refs, e := c.Source.Discover(context.Background())
		if e != nil || len(refs) != 1 {
			t.Fatalf("%s: %v %v", p.ID, refs, e)
		}
		identity := core.ReadIdentity(refs[0].Meta)
		if identity.Status != "known" {
			t.Fatal(identity)
		}
		if refs[0].Session != id || refs[0].Connector != p.ID {
			t.Fatal("profile lost the shared transcript binding", refs[0])
		}
		titles[core.S(refs[0].Meta["desktopTitle"])] = true
		if p.Profile == "Claude-3p" && (len(identity.Claims) != 1 || identity.Claims[0].Kind != "api-configuration") {
			t.Fatal("API scope impersonates account", identity)
		}
	}
	if len(titles) != 3 || !titles["account title"] || !titles["other title"] || !titles["api title"] {
		t.Fatalf("profile discovery overwrote another namespace: %#v", titles)
	}
	cli := New(Options{Root: root})
	cliRefs, err := cli.Source.Discover(context.Background())
	if err != nil || len(cliRefs) != 1 || core.ReadIdentity(cliRefs[0].Meta).Status != "unknown" || cliRefs[0].Meta["desktopTitle"] != nil {
		t.Fatalf("CLI source inferred another profile's account: %v %#v", err, cliRefs)
	}
	// An unrelated transcript does not become visible under any account scope.
	if e := os.WriteFile(filepath.Join(filepath.Dir(file), "22222222-2222-4222-8222-222222222222.jsonl"), []byte("{}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	c := New(Options{Root: root, DesktopScope: profiles[0].Scope, Identity: profiles[0].Identity})
	refs, _ := c.Source.Discover(context.Background())
	if len(refs) != 1 {
		t.Fatal("scope leaked unrelated transcript", len(refs))
	}
}
func TestCoworkSameLocalIDHasDistinctAccountScopes(t *testing.T) {
	base := t.TempDir()
	for _, account := range []string{"first", "second"} {
		if e := os.MkdirAll(filepath.Join(base, "Claude", "local-agent-mode-sessions", account, "org", "local_same", ".claude"), 0700); e != nil {
			t.Fatal(e)
		}
	}
	roots := DiscoverCoworkRoots([]string{filepath.Join(base, "Claude")})
	if len(roots) != 2 || roots[0].ID == roots[1].ID {
		t.Fatal(roots)
	}
}
func TestWindowsDesktopRootsIncludeCurrentAndLegacyThirdParty(t *testing.T) {
	dirs := DefaultDesktopDirs("windows", "home", "roaming", "local", "")
	if len(dirs) != 8 || dirs[1] != filepath.Join("local", "Claude-3p") || dirs[2] != filepath.Join("roaming", "Claude-3p") {
		t.Fatal(dirs)
	}
}
func TestClaudeModelsDoNotReplaceStorageIdentity(t *testing.T) {
	d := Decoder{}
	ctx := &engine.DecodeContext{State: d.InitialState(), Ref: core.NativeRef{Meta: core.JsonObject{"identity": core.IdentityMeta(core.SessionIdentity{Claims: []core.IdentityClaim{{Kind: "account", ID: "a", Scope: "storage", Evidence: "test directory"}}})}}}
	for _, model := range []string{"first", "second", "first"} {
		r := core.JsonObject{"type": "assistant", "uuid": model, "message": core.JsonObject{"role": "assistant", "model": model, "content": []any{core.JsonObject{"type": "text", "text": "ok"}}}}
		out := d.Decode(r, nil, ctx)
		identity := core.ReadIdentity(out.Meta)
		if len(identity.Claims) != 1 || identity.Claims[0].Scope != "storage" {
			t.Fatal(identity)
		}
		ctx.Index++
	}
	identity := core.ReadIdentity(core.JsonObject{"identity": ctx.State["identity"]})
	if len(identity.Providers) != 2 {
		t.Fatal(identity)
	}
}

func TestDesktopCopyCreatesMatchingAccountMetadataAndRollsBack(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "metadata-conflict"}[fail], func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, ".claude")
			scope := filepath.Join(base, "Claude", "claude-code-sessions", "account-b", "org-b")
			if e := os.MkdirAll(root, 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.MkdirAll(scope, 0700); e != nil {
				t.Fatal(e)
			}
			identity := scopeIdentity("Claude", "account-b", "org-b", "Claude/claude-code-sessions")
			c := New(Options{Root: root, ID: "scope-b", DesktopScope: scope, Identity: identity})
			id := "33333333-3333-4333-8333-333333333333"
			h := hub.New()
			p, e := c.Sink.Plan(engine.PlanInput{Projection: &planner.Projection{Items: []planner.Item{{Key: "u", Role: "user", Event: &core.Event{ID: "u", Kind: "message"}, Parts: []core.Part{{"type": "text", "text": "synthetic copied content"}, {"type": "file", "name": "binary.dat", "mediaType": "application/octet-stream", "blob": h.PutBlob([]byte{0, 1, 2, 255})}}}}}, Hub: h, Target: engine.DeliveryTarget{Mode: "create", Session: id, Title: "Copied target", Cwd: base}})
			if e != nil {
				t.Fatal(e)
			}
			metadataPath := filepath.Join(scope, "local_"+id+".json")
			if fail {
				if e := os.WriteFile(metadataPath, []byte("original metadata"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			_, e = c.Sink.Apply(context.Background(), p)
			if fail {
				if e == nil {
					t.Fatal("conflict accepted")
				}
				if _, e := os.Stat(p.Target.Path); !os.IsNotExist(e) {
					t.Fatal("failed transcript remains", e)
				}
				b, _ := os.ReadFile(metadataPath)
				if string(b) != "original metadata" {
					t.Fatal("overwrote existing metadata")
				}
				for _, v := range core.Arr(p.Extra["attachments"]) {
					if _, e := os.Stat(core.S(core.Obj(v)["path"])); !os.IsNotExist(e) {
						t.Fatal("failed attachment remains", e)
					}
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			var m core.JsonObject
			b, e := os.ReadFile(metadataPath)
			if e != nil || json.Unmarshal(b, &m) != nil || m["cliSessionId"] != id || m["sessionId"] != "local_"+id || m["cwd"] != base {
				t.Fatal(string(b), e)
			}
			refs, e := c.Source.Discover(context.Background())
			if e != nil || len(refs) != 1 {
				t.Fatal(refs, e)
			}
			got := core.ReadIdentity(refs[0].Meta)
			if got.Claims[0].ID != "account-b" {
				t.Fatal(got)
			}
			if _, e = c.Sink.Apply(context.Background(), p); e != nil {
				t.Fatal("same plan retry", e)
			}
		})
	}
}

func TestClaudeRejectsChangedTargetProfile(t *testing.T) {
	for _, desktop := range []bool{false, true} {
		root := t.TempDir()
		o := Options{Root: root, ID: "chosen-profile"}
		if desktop {
			o.DesktopScope = filepath.Join(root, "metadata")
		}
		c := New(o).Source.(*cc)
		for _, target := range []engine.DeliveryTarget{{Profile: "another-profile", Root: root}, {Profile: o.ID, Root: filepath.Join(root, "other")}} {
			if _, e := c.Plan(engine.PlanInput{Target: target}); e == nil {
				t.Fatal("planned wrong profile")
			}
			if _, e := c.Apply(context.Background(), &engine.DeliveryPlan{Target: target}); e == nil {
				t.Fatal("applied wrong profile")
			}
		}
	}
}
