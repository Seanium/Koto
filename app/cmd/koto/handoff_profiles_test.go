package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/koto"
)

func TestHandoffProfileProofAndExplicitTarget(t *testing.T) {
	s := newHandoffSandbox(t)
	first := s.writeCodex(t, testThread, "FIRST PROFILE", true)
	other := s
	other.codex = filepath.Join(s.root, "another account [中文]")
	second := other.writeCodex(t, testThread, "SECOND PROFILE", true)
	a, err := koto.New(koto.Config{DataDir: filepath.Join(s.root, "data"), CodexHome: s.codex, PiRoot: s.pi, Enable: []string{"codex", "pi"}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.AddRoot("codex", other.codex)
	a.Close()
	if err != nil {
		t.Fatal(err)
	}
	before1, _ := os.ReadFile(first)
	before2, _ := os.ReadFile(second)
	r, code, raw := s.run(t, []string{"copy", "codex:" + testThread}, []string{"--to", "pi"}, nil)
	if code == 0 || r.Error.Code != "SOURCE_AMBIGUOUS" || !strings.Contains(r.Error.Hint, p.ID) {
		t.Fatalf("ambiguous source must fail with profiles: %s", raw)
	}
	r, code, raw = s.run(t, []string{"current"}, nil, map[string]string{"CODEX_THREAD_ID": testThread, "CODEX_HOME": other.codex})
	if code != 0 || r.Source.Profile != p.ID || r.Source.Selector != core.SessionSelector(p.ID, "codex", testThread) {
		t.Fatalf("current root proof: %s", raw)
	}
	// The canonical selector remains exact outside the source process environment.
	target := filepath.Join(s.root, "target pi [账号]")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	r, code, raw = s.run(t, []string{"copy", r.Source.Selector}, []string{"--to", "pi", "--to-root", target}, nil)
	if code != 0 || !r.OK || engine.NormalizeRoot(r.Destination.Root) != engine.NormalizeRoot(target) || r.Destination.Profile == "pi" || r.Resume == nil || engine.NormalizeRoot(r.Resume.Env["PI_CODING_AGENT_DIR"]) != engine.NormalizeRoot(target) {
		t.Fatalf("explicit target root: %s", raw)
	}
	if r.Counts.Turns != 2 || r.Deferred.Events == 0 {
		t.Fatalf("active migration request copied: %s", raw)
	}
	var entries []string
	err = filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
			entries = append(entries, path)
		}
		return nil
	})
	if err != nil || len(entries) != 1 {
		t.Fatalf("target files: %v %v", entries, err)
	}
	content, err := os.ReadFile(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "SECOND PROFILE") || strings.Contains(string(content), "FIRST PROFILE") || strings.Contains(string(content), "MIGRATE CURRENT REQUEST") {
		t.Fatal("copy crossed profile or current-request boundary")
	}
	a, err = koto.New(koto.Config{DataDir: filepath.Join(s.root, "data"), CodexHome: s.codex, PiRoot: s.pi, Enable: []string{"codex", "pi"}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if c := a.Registry.Get(r.Destination.Profile); c == nil || engine.NormalizeRoot(c.Root) != engine.NormalizeRoot(target) {
		t.Fatal("explicit target root did not persist")
	}
	after1, _ := os.ReadFile(first)
	after2, _ := os.ReadFile(second)
	if string(before1) != string(after1) || string(before2) != string(after2) {
		t.Fatal("source files changed")
	}
}

func TestCurrentRejectsUnprovenRoot(t *testing.T) {
	s := newHandoffSandbox(t)
	s.writeCodex(t, testThread, "SOURCE", false)
	r, code, raw := s.run(t, []string{"current"}, nil, map[string]string{"CODEX_THREAD_ID": testThread, "CODEX_HOME": filepath.Join(s.root, "unregistered")})
	if code == 0 || r.Error.Code != "CURRENT_PROFILE_UNVERIFIED" {
		t.Fatalf("unproven root fell back: %s", raw)
	}
}

func TestHandoffCursorAppRootTargetsIDE(t *testing.T) {
	s := newHandoffSandbox(t)
	s.writeCodex(t, testThread, "CURSOR APP TARGET", false)
	appRoot := filepath.Join(s.root, "Cursor App [中文]")
	cliRoot := filepath.Join(s.root, "Cursor CLI")
	for _, root := range []string{appRoot, cliRoot} {
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
	}
	profile := engine.ProfileID("cursor", appRoot, "ide")
	r, code, raw := s.run(t, []string{"copy", "codex:" + testThread}, []string{
		"--only", "codex,cursor", "--cursor-root", cliRoot, "--cursor-app-root", appRoot,
		"--to", "cursor", "--to-profile", profile, "--dry-run",
	}, nil)
	if code != 0 || !r.OK || r.Destination == nil || r.Destination.Profile != profile || engine.NormalizeRoot(r.Destination.Root) != engine.NormalizeRoot(appRoot) {
		t.Fatalf("Cursor App flag did not select the isolated IDE target: %s", raw)
	}
	if r.Resume != nil {
		t.Fatal("IDE target must not return a CLI resume command")
	}
	if !strings.Contains(handoffHelp, "--cursor-app-root") {
		t.Fatal("handoff help omits Cursor App configuration")
	}
	for _, root := range []string{appRoot, cliRoot} {
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 0 {
			t.Fatalf("preview wrote native target: %s: %v %v", root, entries, err)
		}
	}
}
