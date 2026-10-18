package koto

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"koto/internal/connectors/shared"
	"koto/internal/core"
)

// Both inspected WorkBuddy 5.8 App variants use this local schema. Fixtures
// establish only a synthetic account, never login state or model credentials.
func initWorkBuddyFixture(t *testing.T, root string) {
	t.Helper()
	file := filepath.Join(root, "workbuddy.db")
	if _, err := os.Stat(file); err == nil {
		return
	}
	must(t, os.MkdirAll(root, 0700))
	db, err := sql.Open("sqlite", file)
	must(t, err)
	defer db.Close()
	schema, err := os.ReadFile(filepath.Join("..", "connectors", "workbuddy", "testdata", "schema-5.8.sql"))
	must(t, err)
	_, err = db.Exec(string(schema))
	must(t, err)
	_, err = db.Exec(`INSERT INTO sessions(id,cwd,user_id,created_at,updated_at,deleted_at) VALUES ('fixture-account',?,'fixture-user',1,1,1)`, root)
	must(t, err)
}

func workBuddySessionSnapshot(t *testing.T, transcript, session string) string {
	t.Helper()
	root := filepath.Dir(filepath.Dir(filepath.Dir(transcript)))
	u := shared.FileURL(filepath.Join(root, "workbuddy.db"))
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	must(t, err)
	defer db.Close()
	rows, err := db.Query(`SELECT * FROM sessions WHERE id=?`, session)
	must(t, err)
	defer rows.Close()
	columns, err := rows.Columns()
	must(t, err)
	ok(t, rows.Next(), "source WorkBuddy account row must exist")
	row := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range row {
		pointers[i] = &row[i]
	}
	must(t, rows.Scan(pointers...))
	ok(t, !rows.Next(), "source ID must identify one account row")
	must(t, rows.Err())
	return core.ToJSON(map[string]any{"columns": columns, "values": row, "transcript": text(t, transcript)})
}
