package zcode

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
	"time"

	"koto/internal/core"
)

func TestNativeLaunchPreservesBundledPathsAndOverrides(t *testing.T) {
	missing := func(string) (string, error) { return "", exec.ErrNotFound }
	app := filepath.Join(t.TempDir(), "Applications 中文 space", "ZCode.app")
	files := []string{"Contents/MacOS/ZCode", "Contents/Resources/glm/zcode.cjs", "Contents/Resources/config/provider/zcode-builtin.json"}
	for _, rel := range files {
		p := filepath.Join(app, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("fixture"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	launch, err := resolveNativeLaunch(nil, "darwin", []string{app}, missing)
	if err != nil || !reflect.DeepEqual(launch.argv, []string{filepath.Join(app, filepath.FromSlash(files[0])), filepath.Join(app, filepath.FromSlash(files[1]))}) || launch.env["ELECTRON_RUN_AS_NODE"] != "1" {
		t.Fatal(launch, err)
	}
	found := func(s string) (string, error) { return filepath.Join("/tools", s), nil }
	launch, err = resolveNativeLaunch(nil, "darwin", []string{app}, found)
	if err != nil || len(launch.argv) != 1 || len(launch.env) != 0 {
		t.Fatal("PATH must take priority", launch, err)
	}
	launch, err = resolveNativeLaunch([]string{"node", "custom bundle.cjs"}, "darwin", []string{app}, found)
	if err != nil || !reflect.DeepEqual(launch.argv, []string{filepath.Join("/tools", "node"), "custom bundle.cjs"}) {
		t.Fatal("custom argv changed", launch, err)
	}
	if err := os.Remove(filepath.Join(app, filepath.FromSlash(files[2]))); err != nil {
		t.Fatal(err)
	}
	if _, err = resolveNativeLaunch(nil, "darwin", []string{app}, missing); err == nil {
		t.Fatal("incomplete desktop bundle accepted")
	}
	c := New(Options{Root: t.TempDir(), Command: []string{filepath.Join(t.TempDir(), "missing")}}).Source.(*connector)
	if argv, _ := c.NativeResume("sess_test"); argv != nil || c.ResumeCommand() != "" {
		t.Fatal("nonexistent resume advertised", argv)
	}
}

func TestNativeLaunchWindowsAndLinuxDesktopBundles(t *testing.T) {
	for _, goos := range []string{"windows", "linux"} {
		t.Run(goos, func(t *testing.T) {
			app := filepath.Join(t.TempDir(), "ZCode 中文 O'Brien")
			exe := "zcode"
			if goos == "windows" {
				exe = "ZCode.exe"
			}
			files := []string{exe, "resources/glm/zcode.cjs", "resources/config/provider/zcode-builtin.json"}
			for _, rel := range files {
				p := filepath.Join(app, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("fixture"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			binary := filepath.Join(app, exe)
			lookup := func(string) (string, error) { return binary, nil }
			missing := func(string) (string, error) { return "", exec.ErrNotFound }
			for _, tc := range []struct {
				name    string
				command []string
				lookup  func(string) (string, error)
			}{
				{"desktop only", nil, missing},
				{"desktop on PATH", nil, lookup},
				{"explicit desktop exe", []string{binary}, lookup},
			} {
				launch, err := resolveNativeLaunch(tc.command, goos, []string{app}, tc.lookup)
				if err != nil || !reflect.DeepEqual(launch.argv, []string{binary, filepath.Join(app, filepath.FromSlash(files[1]))}) || launch.env["ELECTRON_RUN_AS_NODE"] != "1" || launch.env["ZCODE_BUILTIN_PROVIDER_CONFIG_FILE"] != filepath.Join(app, filepath.FromSlash(files[2])) {
					t.Fatal(tc.name, launch, err)
				}
			}
			// Explicit multi-argument launchers already declare their runtime.
			launch, err := resolveNativeLaunch([]string{binary, "other.cjs"}, goos, []string{app}, lookup)
			if err != nil || !reflect.DeepEqual(launch.argv, []string{binary, "other.cjs"}) || len(launch.env) != 0 {
				t.Fatal("explicit argv rewritten", launch, err)
			}
			if err := os.Remove(filepath.Join(app, filepath.FromSlash(files[1]))); err != nil {
				t.Fatal(err)
			}
			if _, err := resolveNativeLaunch(nil, goos, []string{app}, missing); err == nil {
				t.Fatal("missing embedded agent accepted")
			}
		})
	}
}

func TestWindowsDesktopCandidatesRespectEnvironment(t *testing.T) {
	env := map[string]string{"LOCALAPPDATA": filepath.Join("C:", "User 中文", "Local"), "ProgramFiles": filepath.Join("C:", "Program Files")}
	got := desktopInstallations("windows", "unused", func(key string) string { return env[key] })
	want := []string{filepath.Join(env["LOCALAPPDATA"], "Programs", "ZCode"), filepath.Join(env["ProgramFiles"], "ZCode")}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
	if got := desktopInstallations("windows", "", func(string) string { return "" }); len(got) != 0 {
		t.Fatal("empty environment must not probe relative directories", got)
	}
}

func TestRunningToolProofIsExactLiveAndUnique(t *testing.T) {
	c := fixture(t)
	ctx := context.Background()
	p := planFor(t, c, "proof")
	if _, err := c.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	db, err := c.open(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var mid string
	if err = db.QueryRow(`SELECT id FROM message WHERE session_id=? LIMIT 1`, p.Target.Session).Scan(&mid); err != nil {
		t.Fatal(err)
	}
	proof := regexp.MustCompile(`(?:^|\s)koto current --proof 1234567890abcdef1234567890abcdef(?:\s|$)`)
	now := time.Now().UnixMilli()
	insert := func(id, tool, status, command string, updated int64) {
		t.Helper()
		b, _ := json.Marshal(core.JsonObject{"type": "tool", "tool": tool, "state": core.JsonObject{"status": status, "input": core.JsonObject{"command": command}}})
		if _, err := db.Exec(`INSERT INTO part(id,message_id,session_id,time_created,time_updated,data) VALUES(?,?,?,?,?,?)`, id, mid, p.Target.Session, now, updated, string(b)); err != nil {
			t.Fatal(err)
		}
	}
	exact := "koto current --proof 1234567890abcdef1234567890abcdef --json"
	insert("completed", "Bash", "completed", exact, now)
	insert("stale", "Bash", "running", exact, now-6*60*1000)
	insert("near", "Bash", "running", "koto current --proof 1234567890abcdef1234567890abcdef9 --json", now)
	insert("other-tool", "Read", "running", exact, now)
	ids, err := c.RunningToolSessions(ctx, proof)
	if err != nil || len(ids) != 0 {
		t.Fatal(ids, err)
	}
	insert("live", "Bash", "running", exact, now)
	insert("duplicate", "Bash", "running", exact, now)
	ids, err = c.RunningToolSessions(ctx, proof)
	if err != nil || !reflect.DeepEqual(ids, []string{p.Target.Session}) {
		t.Fatal(ids, err)
	}
	if _, err := db.Exec(`UPDATE session SET time_archived=? WHERE id=?`, now, p.Target.Session); err != nil {
		t.Fatal(err)
	}
	ids, err = c.RunningToolSessions(ctx, proof)
	if err != nil || len(ids) != 0 {
		t.Fatal("archived proof accepted", ids, err)
	}
	if _, err = c.RunningToolSessions(ctx, nil); err == nil {
		t.Fatal("nil proof accepted")
	}
}
