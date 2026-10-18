package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/connectors/claudecode"
	"koto/internal/core"
)

func TestCurrentCoworkSourceAndReadOnlyTarget(t *testing.T) {
	for _, profile := range []string{"Claude", "Claude-3p"} {
		t.Run(profile, func(t *testing.T) {
			s := newClaudeCurrentSandbox(t, profile)
			cowork := filepath.Join(s.desktop, "local-agent-mode-sessions", "account", "org", "local_task", ".claude")
			file := s.writeClaude(t, cowork, "COWORK")
			before, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			roots := claudecode.DiscoverCoworkRoots([]string{s.desktop})
			if len(roots) != 1 {
				t.Fatal(roots)
			}
			entrypoint := "claude-desktop"
			if profile == "Claude-3p" {
				entrypoint = "claude-desktop-3p"
			}
			env := map[string]string{"CLAUDE_CODE_SESSION_ID": testThread, "CLAUDE_CODE_ENTRYPOINT": entrypoint, "CLAUDE_CONFIG_DIR": cowork}
			r, code, raw := s.run(t, []string{"current"}, nil, env)
			if code != 0 || r.Source.Profile != roots[0].ID {
				t.Fatalf("exact Cowork root should remain readable: %s", raw)
			}
			delete(env, "CLAUDE_CONFIG_DIR")
			r, code, raw = s.run(t, []string{"current"}, nil, env)
			if code == 0 || r.Error.Code != "SOURCE_AMBIGUOUS" {
				t.Fatalf("same UUID in independent Code/Cowork files must stay ambiguous: %s", raw)
			}
			env["CLAUDE_CONFIG_DIR"], env["CLAUDE_CODE_ENTRYPOINT"] = cowork, "cli"
			r, code, raw = s.run(t, []string{"current"}, nil, env)
			if code == 0 || r.Error.Code != "SOURCE_NOT_FOUND" {
				t.Fatalf("CLI host must not impersonate a Cowork source: %s", raw)
			}
			env["CLAUDE_CODE_ENTRYPOINT"] = entrypoint
			for _, target := range [][]string{{"--to-profile", roots[0].ID}, {"--to-root", cowork}} {
				for _, dryRun := range []bool{true, false} {
					flags := append([]string{"--to", "claude-code"}, target...)
					if dryRun {
						flags = append(flags, "--dry-run")
					}
					r, code, raw = s.run(t, []string{"copy", core.SessionSelector(s.cliID, "claude-code", testThread)}, flags, env)
					if code == 0 || r.OK || r.Resume != nil || r.Error.Code == "USAGE" || len(nativeJSONLs(t, cowork)) != 1 || !strings.Contains(raw, "target") && !strings.Contains(raw, "registered") {
						t.Fatalf("Cowork target must not produce a receipt/resume or new files: %s", raw)
					}
				}
			}
			r, code, raw = s.run(t, []string{"copy"}, []string{"--current", "--to", "pi"}, env)
			if code != 0 || !r.OK || r.Source.Profile != roots[0].ID || r.Counts.Turns != 1 || r.Deferred.Events != 2 {
				t.Fatalf("read-only Cowork source must copy outward: %s", raw)
			}
			files := nativeJSONLs(t, s.pi)
			if len(files) != 1 {
				t.Fatal(files)
			}
			data, _ := os.ReadFile(files[0])
			if !bytes.Contains(data, []byte("COWORK question")) || !bytes.Contains(data, []byte("COWORK answer")) || bytes.Contains(data, []byte("CURRENT MIGRATION")) {
				t.Fatal("Cowork source content/range changed")
			}
			after, _ := os.ReadFile(file)
			if !bytes.Equal(before, after) {
				t.Fatal("Cowork source changed")
			}
		})
	}
}

func TestCurrentCoworkActualConfiguredRoot(t *testing.T) {
	for _, throughEnv := range []bool{true, false} {
		t.Run(map[bool]string{true: "CLAUDE_CONFIG_DIR", false: "claude-root"}[throughEnv], func(t *testing.T) {
			s := newClaudeCurrentSandbox(t, "Claude-3p")
			cowork := filepath.Join(s.desktop, "local-agent-mode-sessions", "account", "org", "local_task", ".claude")
			file := s.writeClaude(t, cowork, "COWORK")
			before, _ := os.ReadFile(file)
			var extra []string
			if throughEnv {
				t.Setenv("CLAUDE_CONFIG_DIR", cowork)
			} else {
				extra = []string{"--claude-root", cowork}
			}
			env := map[string]string{"CLAUDE_CODE_SESSION_ID": testThread, "CLAUDE_CODE_ENTRYPOINT": "claude-desktop-3p", "CLAUDE_CONFIG_DIR": cowork}
			r, code, raw := s.run(t, []string{"current"}, extra, env)
			roots := claudecode.DiscoverCoworkRoots([]string{s.desktop})
			if code != 0 || r.Source.Profile != roots[0].ID || r.Source.Identity.Claims[0].Kind != "api-configuration" {
				t.Fatalf("configured root lost Cowork's Desktop/storage identity: %s", raw)
			}
			// The root is readable, but cannot become an implicit writable CLI target.
			flags := append(append([]string{}, extra...), "--current", "--to", "claude-code", "--to-profile", roots[0].ID, "--dry-run")
			r, code, raw = s.run(t, []string{"copy"}, flags, env)
			if code == 0 || r.Error.Code == "USAGE" || !strings.Contains(raw, "cannot be a delivery target") {
				t.Fatalf("configured Cowork became writable: %s", raw)
			}
			after, _ := os.ReadFile(file)
			if !bytes.Equal(before, after) || len(nativeJSONLs(t, cowork)) != 1 {
				t.Fatal("configured Cowork source changed")
			}
		})
	}
}
