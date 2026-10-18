//go:build windows

package koto

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/connectors/shared"
	"koto/internal/core"
)

func TestCopyCodexExtendedWindowsProjectToPi(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "source-default", true: "explicit"}[explicit], func(t *testing.T) {
			s := newSandbox(t)
			cwd := filepath.Join(s.Root, "工作 [copy] O'Brien & literal")
			must(t, os.MkdirAll(cwd, 0700))
			extended := `\\?\` + cwd
			records := []map[string]any{
				{"type": "session_meta", "payload": core.JsonObject{"id": CodexThread, "cwd": cwd, "source": "cli"}},
				{"type": "response_item", "payload": core.JsonObject{"type": "message", "id": "user-1", "role": "user", "content": []any{core.JsonObject{"type": "input_text", "text": "Keep this Windows context."}}}},
				{"type": "response_item", "payload": core.JsonObject{"type": "message", "id": "assistant-1", "role": "assistant", "content": []any{core.JsonObject{"type": "output_text", "text": "Windows context retained."}}}},
			}
			file := writeCodexRollout(t, s.Codex, CodexThread, records)
			// Actual Windows Codex stores an ordinary rollout cwd and an extended
			// current cwd in its state DB. The latter wins only for new targets.
			dbPath := filepath.Join(s.Codex, "state_5.sqlite")
			db, err := sql.Open("sqlite", shared.FileURL(dbPath).String())
			must(t, err)
			_, err = db.Exec(`CREATE TABLE threads(id TEXT PRIMARY KEY, cwd TEXT); INSERT INTO threads VALUES(?,?)`, CodexThread, extended)
			must(t, err)
			must(t, db.Close())
			dbBefore, err := os.ReadFile(dbPath)
			must(t, err)
			before, err := os.ReadFile(file)
			must(t, err)
			a := s.app(enable("codex", "pi"))
			if report := a.ScanIndex(context.Background(), nil); len(report.Errors) > 0 {
				t.Fatalf("scan: %v", report.Errors)
			}
			api := &API{App: a}
			req := CarryRequest{From: "codex:" + CodexThread, To: "pi"}
			if explicit {
				req.Cwd = extended
			}
			result, err := api.Carry(context.Background(), req)
			must(t, err)
			if !result.Applied {
				t.Fatal("copy did not apply")
			}
			refs, err := a.Registry.Get("pi").Source.Discover(context.Background())
			must(t, err)
			if len(refs) != 1 {
				t.Fatalf("got %d targets", len(refs))
			}
			body, err := os.ReadFile(refs[0].Path)
			must(t, err)
			header := readLine(t, strings.SplitN(string(body), "\n", 2)[0])
			eq(t, core.S(header["cwd"]), cwd, "target has a normal Windows project path")
			if strings.Contains(filepath.Base(filepath.Dir(refs[0].Path)), "?") {
				t.Fatal("target encoded a namespace marker into its directory")
			}
			after, err := os.ReadFile(file)
			must(t, err)
			eq(t, core.SHA256Hex(after), core.SHA256Hex(before), "source was rewritten")
			dbAfter, err := os.ReadFile(dbPath)
			must(t, err)
			eq(t, core.SHA256Hex(dbAfter), core.SHA256Hex(dbBefore), "native current-directory DB was rewritten")
		})
	}
}
