package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"koto/internal/connectors/claudecode"
	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/koto"
)

type claudeCurrentSandbox struct {
	handoffSandbox
	claude, desktop, file, cliID, desktopID string
}

func newClaudeCurrentSandbox(t *testing.T, profile string) claudeCurrentSandbox {
	t.Helper()
	s := newHandoffSandbox(t)
	for k, v := range map[string]string{
		"HOME": s.root, "USERPROFILE": s.root, "CLAUDE_CONFIG_DIR": "",
		"APPDATA": filepath.Join(s.root, "AppData", "Roaming"), "LOCALAPPDATA": filepath.Join(s.root, "AppData", "Local"),
		"XDG_CONFIG_HOME": filepath.Join(s.root, ".config"),
	} {
		t.Setenv(k, v)
	}
	cs := claudeCurrentSandbox{handoffSandbox: s, claude: filepath.Join(s.root, ".claude")}
	for _, d := range claudecode.DefaultDesktopDirs(runtime.GOOS, s.root, os.Getenv("APPDATA"), os.Getenv("LOCALAPPDATA"), os.Getenv("XDG_CONFIG_HOME")) {
		if filepath.Base(d) == profile {
			cs.desktop = d
			break
		}
	}
	if cs.desktop == "" {
		t.Fatal("missing native Desktop directory")
	}
	cs.flags = []string{"--json", "--data", filepath.Join(s.root, "data"), "--pi-root", s.pi, "--only", "claude-code,pi"}
	cs.file = cs.writeClaude(t, cs.claude, "ORIGINAL")
	cs.cliID = engine.ProfileID("claude-code", cs.claude, "cli")
	cs.desktopID = cs.writeScope(t, "account-one", "organization-one")
	return cs
}

func (s claudeCurrentSandbox) writeClaude(t *testing.T, root, marker string) string {
	t.Helper()
	file := filepath.Join(root, "projects", claudecode.ProjectDir(s.cwd), testThread+".jsonl")
	rows := []core.JsonObject{
		{"type": "user", "uuid": "u1", "parentUuid": nil, "message": core.JsonObject{"role": "user", "content": marker + " question"}},
		{"type": "assistant", "uuid": "a1", "parentUuid": "u1", "message": core.JsonObject{"role": "assistant", "content": marker + " answer", "stop_reason": "end_turn"}},
		{"type": "user", "uuid": "u2", "parentUuid": "a1", "message": core.JsonObject{"role": "user", "content": "CURRENT MIGRATION REQUEST"}},
		{"type": "assistant", "uuid": "a2", "parentUuid": "u2", "message": core.JsonObject{"role": "assistant", "content": []any{core.JsonObject{"type": "tool_use", "id": "running", "name": "Bash", "input": core.JsonObject{"command": "koto copy --current --to pi --json"}}}, "stop_reason": "tool_use"}},
	}
	var data bytes.Buffer
	for _, row := range rows {
		row["cwd"], row["sessionId"], row["version"] = s.cwd, testThread, "2.1.300"
		// Historical origin must never select the currently calling host.
		row["entrypoint"] = "claude-desktop-3p"
		if err := json.NewEncoder(&data).Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return file
}

func (s claudeCurrentSandbox) writeScope(t *testing.T, account, org string) string {
	t.Helper()
	scope := filepath.Join(s.desktop, "claude-code-sessions", account, org)
	if err := os.MkdirAll(scope, 0700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(core.JsonObject{"cliSessionId": testThread, "sessionId": "local_" + testThread, "cwd": s.cwd, "title": "Shared native transcript"})
	if err := os.WriteFile(filepath.Join(scope, "local_"+testThread+".json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	return "claude-desktop-" + core.SHA256Hex(scope)[:16]
}

func TestCurrentClaudeOfficialHostSelectsSharedTranscriptBinding(t *testing.T) {
	for _, entrypoint := range []string{"claude-desktop", "claude-desktop-3p", "cli", "sdk-cli"} {
		t.Run(entrypoint, func(t *testing.T) {
			profile := "Claude"
			if entrypoint != "claude-desktop" {
				profile = "Claude-3p"
			}
			s := newClaudeCurrentSandbox(t, profile)
			before, _ := os.ReadFile(s.file)
			env := map[string]string{"CLAUDE_CODE_SESSION_ID": testThread, "CLAUDE_CODE_ENTRYPOINT": entrypoint}
			want := s.desktopID
			if claudeHostVariant(entrypoint) == "cli" {
				want = s.cliID
			}
			for _, withRoot := range []bool{false, true} {
				if withRoot {
					env["CLAUDE_CONFIG_DIR"] = s.claude
				}
				r, code, raw := s.run(t, []string{"current"}, nil, env)
				if code != 0 || r.Source.Profile != want || r.ResolvedBy != "CLAUDE_CODE_SESSION_ID" {
					t.Fatalf("host binding (root=%v): %d %s", withRoot, code, raw)
				}
				if (len(r.Source.Identity.Claims) > 0) != (want == s.desktopID) {
					t.Fatalf("host must not inherit another surface's account: %s", raw)
				}
			}
			r, code, raw := s.run(t, []string{"copy"}, []string{"--current", "--to", "pi"}, env)
			if code != 0 || r.Source.Profile != want || r.Counts.Turns != 1 || r.Deferred.Events != 2 {
				t.Fatalf("current copy: %d %s", code, raw)
			}
			files := nativeJSONLs(t, s.pi)
			if len(files) != 1 {
				t.Fatalf("targets: %v", files)
			}
			target, _ := os.ReadFile(files[0])
			if !bytes.Contains(target, []byte("ORIGINAL question")) || !bytes.Contains(target, []byte("ORIGINAL answer")) || bytes.Contains(target, []byte("CURRENT MIGRATION")) {
				t.Fatal("copy lost completed content or included current invocation")
			}
			after, _ := os.ReadFile(s.file)
			if !bytes.Equal(before, after) {
				t.Fatal("source changed")
			}
		})
	}
}

func TestCurrentClaudeUnknownHostAndIndependentNamespacesStayAmbiguous(t *testing.T) {
	for _, entrypoint := range []string{"", "unknown", "sdk-ts", "claude-desktop-future"} {
		t.Run("unknown/"+entrypoint, func(t *testing.T) {
			s := newClaudeCurrentSandbox(t, "Claude-3p")
			r, code, raw := s.run(t, []string{"current"}, nil, map[string]string{"CLAUDE_CODE_SESSION_ID": testThread, "CLAUDE_CODE_ENTRYPOINT": entrypoint})
			if code == 0 || r.Error.Code != "SOURCE_AMBIGUOUS" {
				t.Fatalf("unknown host guessed historical origin: %s", raw)
			}
		})
	}
	for _, independent := range []bool{false, true} {
		t.Run(map[bool]string{false: "multiple-account-bindings", true: "independent-transcripts"}[independent], func(t *testing.T) {
			s := newClaudeCurrentSandbox(t, "Claude-3p")
			if independent {
				other := filepath.Join(s.root, "another Claude root")
				s.writeClaude(t, other, "INDEPENDENT")
				a, err := koto.New(koto.Config{DataDir: filepath.Join(s.root, "data"), PiRoot: s.pi, Enable: []string{"claude-code", "pi"}})
				if err != nil {
					t.Fatal(err)
				}
				_, err = a.AddRoot("claude-code", other)
				a.Close()
				if err != nil {
					t.Fatal(err)
				}
			} else {
				s.writeScope(t, "account-two", "organization-two")
			}
			env := map[string]string{"CLAUDE_CODE_SESSION_ID": testThread, "CLAUDE_CODE_ENTRYPOINT": "claude-desktop-3p"}
			r, code, raw := s.run(t, []string{"copy"}, []string{"--current", "--to", "pi"}, env)
			if code == 0 || r.Error.Code != "SOURCE_AMBIGUOUS" || len(nativeJSONLs(t, s.pi)) != 0 {
				t.Fatalf("host guessed independent root/account: %s", raw)
			}
			// An exact runtime root can exclude an independent file, but cannot
			// select between two account scopes bound to the same transcript.
			env["CLAUDE_CONFIG_DIR"] = s.claude
			r, code, raw = s.run(t, []string{"current"}, nil, env)
			if independent && (code != 0 || r.Source.Profile != s.desktopID) || !independent && (code == 0 || r.Error.Code != "SOURCE_AMBIGUOUS") {
				t.Fatalf("explicit root boundary: %s", raw)
			}
		})
	}
}

func TestCurrentClaudeExplicitIdentityWinsAndHostMismatchFailsClosed(t *testing.T) {
	s := newClaudeCurrentSandbox(t, "Claude-3p")
	env := map[string]string{"CLAUDE_CODE_SESSION_ID": testThread, "CLAUDE_CODE_ENTRYPOINT": "claude-desktop-3p"}
	cli := core.SessionSelector(s.cliID, "claude-code", testThread)
	env["KOTO_SESSION"] = cli
	r, code, raw := s.run(t, []string{"current"}, nil, env)
	if code != 0 || r.Source.Profile != s.cliID || r.ResolvedBy != "KOTO_SESSION" {
		t.Fatalf("explicit current identity ignored: %s", raw)
	}
	delete(env, "KOTO_SESSION")
	r, code, raw = s.run(t, []string{"copy", cli}, []string{"--to", "pi", "--dry-run"}, env)
	if code != 0 || r.Source.Profile != s.cliID || r.ResolvedBy != "explicit" {
		t.Fatalf("explicit selector ignored: %s", raw)
	}
	env["PI_SESSION_ID"] = otherThread
	r, code, raw = s.run(t, []string{"current"}, nil, env)
	if code == 0 || r.Error.Code != "CURRENT_SESSION_AMBIGUOUS" {
		t.Fatalf("conflicting process identities ignored: %s", raw)
	}
	delete(env, "PI_SESSION_ID")
	env["CLAUDE_CONFIG_DIR"] = filepath.Join(s.root, "not-registered")
	r, code, raw = s.run(t, []string{"current"}, nil, env)
	if code == 0 || r.Error.Code != "CURRENT_PROFILE_UNVERIFIED" {
		t.Fatalf("unproven root substituted: %s", raw)
	}
	delete(env, "CLAUDE_CONFIG_DIR")
	// The directory here belongs solely to this isolated fixture.
	if err := os.RemoveAll(s.desktop); err != nil {
		t.Fatal(err)
	}
	r, code, raw = s.run(t, []string{"current"}, nil, env)
	if code == 0 || r.Error.Code != "SOURCE_NOT_FOUND" || strings.Contains(raw, `"source":`) {
		t.Fatalf("Desktop host silently fell back to CLI: %s", raw)
	}
}

func TestCurrentClaudeDesktopEditionDoesNotClaimOtherAccount(t *testing.T) {
	for _, profile := range []string{"Claude", "Claude-3p"} {
		t.Run(profile, func(t *testing.T) {
			s := newClaudeCurrentSandbox(t, profile)
			entrypoint := "claude-desktop-3p"
			if profile == "Claude-3p" {
				entrypoint = "claude-desktop"
			}
			env := map[string]string{"CLAUDE_CODE_SESSION_ID": testThread, "CLAUDE_CODE_ENTRYPOINT": entrypoint}
			r, code, raw := s.run(t, []string{"current"}, nil, env)
			if code == 0 || r.Error.Code != "SOURCE_NOT_FOUND" {
				t.Fatalf("Desktop host claimed another edition's account: %s", raw)
			}
			otherProfile := "Claude"
			if profile == "Claude" {
				otherProfile = "Claude-3p"
			}
			for _, d := range claudecode.DefaultDesktopDirs(runtime.GOOS, s.root, os.Getenv("APPDATA"), os.Getenv("LOCALAPPDATA"), os.Getenv("XDG_CONFIG_HOME")) {
				if filepath.Base(d) == otherProfile {
					s.desktop = d
					break
				}
			}
			otherID := s.writeScope(t, "other-edition-account", "other-edition-org")
			r, code, raw = s.run(t, []string{"current"}, nil, env)
			if code != 0 || r.Source.Profile != otherID {
				t.Fatalf("host must select the exact edition while both bind the same file: %s", raw)
			}
		})
	}
}

func TestCurrentClaudeUnknownHostRootIsOnlyAScopeConstraint(t *testing.T) {
	for _, entrypoint := range []string{"", "unknown-host"} {
		t.Run(entrypoint, func(t *testing.T) {
			s := newClaudeCurrentSandbox(t, "Claude-3p")
			other := filepath.Join(s.root, "independent Claude root")
			s.writeClaude(t, other, "UNRELATED ROOT")
			a, err := koto.New(koto.Config{DataDir: filepath.Join(s.root, "data"), PiRoot: s.pi, Enable: []string{"claude-code", "pi"}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = a.AddRoot("claude-code", other)
			a.Close()
			if err != nil {
				t.Fatal(err)
			}
			env := map[string]string{"CLAUDE_CODE_SESSION_ID": testThread, "CLAUDE_CODE_ENTRYPOINT": entrypoint, "CLAUDE_CONFIG_DIR": s.claude}
			r, code, raw := s.run(t, []string{"copy"}, []string{"--current", "--to", "pi"}, env)
			if code == 0 || r.Error.Code != "SOURCE_AMBIGUOUS" || len(nativeJSONLs(t, s.pi)) != 0 {
				t.Fatalf("unknown host claimed the CLI binding of a shared root: %s", raw)
			}
			// Remove only this fixture's Desktop metadata. The same explicit root
			// can now prove one source without selecting the unrelated directory.
			if err := os.RemoveAll(s.desktop); err != nil {
				t.Fatal(err)
			}
			r, code, raw = s.run(t, []string{"current"}, nil, env)
			if code != 0 || r.Source.Profile != s.cliID {
				t.Fatalf("unique source in the proven root was not resolved: %s", raw)
			}
			env["CLAUDE_CONFIG_DIR"] = filepath.Join(s.root, "not-registered")
			r, code, raw = s.run(t, []string{"current"}, nil, env)
			if code == 0 || r.Error.Code != "CURRENT_PROFILE_UNVERIFIED" {
				t.Fatalf("unknown host substituted an unproven root: %s", raw)
			}
		})
	}
}
