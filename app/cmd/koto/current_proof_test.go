package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"koto/internal/koto"
)

func TestRunningProofIgnoresHistoryAndOtherTools(t *testing.T) {
	file := filepath.Join(t.TempDir(), "proof # 中文 & space.db")
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE part(session_id TEXT, time_updated INTEGER, data TEXT)`); err != nil {
		t.Fatal(err)
	}
	token := "0123456789abcdef0123456789abcdef"
	command := `koto current --proof ` + token + ` --json`
	for _, item := range []struct {
		session, tool, status, command string
		age                            time.Duration
	}{
		{"actual", "bash", "running", command, 0},
		{"completed", "bash", "completed", command, 0},
		{"read-output", "read", "running", command, 0},
		{"stale", "bash", "running", command, 10 * time.Minute},
		{"newest-unrelated", "bash", "running", "koto current --proof ffffffffffffffffffffffffffffffff --json", 0},
	} {
		data, _ := json.Marshal(map[string]any{"type": "tool", "tool": item.tool, "state": map[string]any{"status": item.status, "input": map[string]any{"command": item.command}}})
		if _, err := db.Exec(`INSERT INTO part VALUES(?,?,?)`, item.session, time.Now().Add(-item.age).UnixMilli(), string(data)); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := findRunningProof(context.Background(), file, proofInvocation(token))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "actual" {
		t.Fatalf("selected %v instead of the running invocation", ids)
	}
	if _, err := db.Exec(`UPDATE part SET data=json_set(data,'$.state.status','completed') WHERE session_id='actual'`); err != nil {
		t.Fatal(err)
	}
	ids, err = findRunningProof(context.Background(), file, proofInvocation(token))
	if err != nil || len(ids) != 0 {
		t.Fatalf("completed proof was reusable: %v %v", ids, err)
	}
}

func TestProofInvocationPaths(t *testing.T) {
	token := "0123456789abcdef0123456789abcdef"
	rx := proofInvocation(token)
	for _, command := range []string{
		`koto current --proof ` + token + ` --json`,
		`'/Applications/koto.app/Contents/Helpers/koto' current --proof='` + token + `' --json`,
		`& 'C:\Users\测试 用户\koto-cli.exe' current --json --proof "` + token + `"`,
		`'/home/test/Downloads/koto-cli-linux-arm64' current --proof ` + token + ` --json`,
		`'/Users/test/Downloads/koto-cli-darwin-amd64' current --proof ` + token + ` --json`,
		`& 'C:\Users\测试 用户\Downloads\koto-cli-windows-arm64.exe' current --proof ` + token + ` --json`,
	} {
		if !rx.MatchString(command) {
			t.Fatalf("valid invocation rejected: %s", command)
		}
	}
	for _, command := range []string{
		`not-koto current --proof ` + token,
		`not-koto-cli-linux-arm64 current --proof ` + token,
		`koto-cli-linux-arm64-unrelated current --proof ` + token,
		`koto copy --proof ` + token,
		`koto current --proof ` + token + `ffff`,
	} {
		if rx.MatchString(command) {
			t.Fatalf("unrelated invocation matched: %s", command)
		}
	}
}

func TestCurrentProofRejectsAmbiguousSessions(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "opencode")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE part(session_id TEXT, time_updated INTEGER, data TEXT)`); err != nil {
		t.Fatal(err)
	}
	token := "0123456789abcdef0123456789abcdef"
	data, _ := json.Marshal(map[string]any{"type": "tool", "tool": "bash", "state": map[string]any{"status": "running", "input": map[string]any{"command": "koto current --proof " + token + " --json"}}})
	for _, session := range []string{"ses_a", "ses_b"} {
		if _, err := db.Exec(`INSERT INTO part VALUES(?,?,?)`, session, time.Now().UnixMilli(), string(data)); err != nil {
			t.Fatal(err)
		}
	}
	app, err := koto.New(koto.Config{DataDir: filepath.Join(root, "hub"), OpenCodeRoot: dir, Enable: []string{"opencode"}, NoDesktop: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	_, err = resolveCurrentProof(context.Background(), app, token)
	var he *handoffError
	if !errors.As(err, &he) || he.Code != "CURRENT_SESSION_UNVERIFIED" {
		t.Fatalf("ambiguous proof accepted: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM part WHERE session_id='ses_b'`); err != nil {
		t.Fatal(err)
	}
	got, err := resolveCurrentProof(context.Background(), app, token)
	if err != nil || got != "@"+app.Registry.Get("opencode").ID+":ses_a" {
		t.Fatalf("unique proof: %q %v", got, err)
	}
}
