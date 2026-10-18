package koto

import (
	"database/sql"
	"os/exec"
	"path/filepath"
	"testing"

	"koto/internal/connectors/shared"
	"koto/internal/core"
)

var copyAgents = []string{"claude-code", "pi", "codex", "dsh", "antigravity", "opencode", "zcode", "workbuddy", "workbuddy-ai", "cursor"}

func requireCopyAgent(t *testing.T, from, to string) {
	t.Helper()
	if to == "codex" && !hasCodex() {
		t.Skip("Codex native app-server is not installed; target was not tested")
	}
	if from == "opencode" || to == "opencode" {
		if _, err := exec.LookPath("opencode"); err != nil {
			t.Skip("OpenCode official CLI is not installed; native schema was not tested")
		}
		// The official CLI creates its schema and resolves this local model config.
		// Tests never make a model request or inherit a real provider's credentials.
		t.Setenv("OPENCODE_CONFIG_CONTENT", `{"model":"fixture/model","provider":{"fixture":{"npm":"@ai-sdk/openai-compatible","models":{"model":{"name":"fixture"}},"options":{"baseURL":"http://127.0.0.1:9/v1","apiKey":"fixture"}}}}`)
	}
}

// SQLite agents store all conversations in one database. Compare the selected
// session's actual rows, so creating another session does not look like damage.
func nativeSessionSnapshot(t *testing.T, file, harness, session string) string {
	t.Helper()
	if harness == "workbuddy" || harness == "workbuddy-ai" {
		return workBuddySessionSnapshot(t, file, session)
	}
	if harness != "opencode" && harness != "zcode" {
		return text(t, file)
	}
	u := shared.FileURL(file)
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	must(t, err)
	defer db.Close()
	out := map[string]any{}
	for _, table := range []string{"session", "message", "part"} {
		column := "session_id"
		if table == "session" {
			column = "id"
		}
		rows, err := db.Query("SELECT * FROM "+table+" WHERE "+column+"=? ORDER BY id", session)
		must(t, err)
		columns, err := rows.Columns()
		must(t, err)
		values := [][]any{}
		for rows.Next() {
			row := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range row {
				pointers[i] = &row[i]
			}
			must(t, rows.Scan(pointers...))
			values = append(values, row)
		}
		must(t, rows.Err())
		must(t, rows.Close())
		if table == "session" {
			eq(t, len(values), 1, "snapshot must identify exactly one native session")
		}
		out[table] = map[string]any{"columns": columns, "rows": values}
	}
	return core.ToJSON(out)
}

func openCodeLinearSource(t *testing.T, s *sandbox, api *API) (string, string) {
	t.Helper()
	writeLines(t, filepath.Join(ccDir(s.Claude), CCA+".jsonl"), ccLinear(CCA, 4, ""))
	api.Reindex(ctx)
	r, err := api.Carry(ctx, CarryRequest{Selector: "claude-code:" + CCA, To: "opencode", Cwd: s.Root})
	must(t, err)
	ok(t, r.Applied)
	return r.Plan.Target.Path, r.Plan.Target.Session
}

// Seed each source through its native writer, then independently read it before
// selecting anything. Native-app acceptance is separate from this API matrix.
func linearMatrixSource(t *testing.T, s *sandbox, api *API, harness string) (string, string) {
	t.Helper()
	file := filepath.Join(ccDir(s.Claude), CCA+".jsonl")
	writeLines(t, file, ccLinear(CCA, 4, ""))
	api.Reindex(ctx)
	if harness == "claude-code" {
		return file, CCA
	}
	r, err := api.Carry(ctx, CarryRequest{Selector: "claude-code:" + CCA, To: harness, Cwd: s.Root})
	must(t, err)
	ok(t, r.Applied)
	return r.Plan.Target.Path, r.Plan.Target.Session
}
