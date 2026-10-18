package koto

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// Native ZCode CLI 0.16.9 table contract, verified against its migrations and
// desktop 3.14.3's bundled CLI. These fixtures contain no user configuration.

func initZCodeFixture(t *testing.T, root string) {
	t.Helper()
	file := filepath.Join(root, "cli", "db", "db.sqlite")
	if _, err := os.Stat(file); err == nil {
		return
	}
	must(t, os.MkdirAll(filepath.Dir(file), 0700))
	db, err := sql.Open("sqlite", file)
	must(t, err)
	defer db.Close()
	schema, err := os.ReadFile(filepath.Join("..", "connectors", "zcode", "testdata", "schema-0.16.9.sql"))
	must(t, err)
	_, err = db.Exec(string(schema))
	must(t, err)
}
