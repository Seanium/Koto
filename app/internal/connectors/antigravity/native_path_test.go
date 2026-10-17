package antigravity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeDatabaseAtUnicodeSpacedPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "数据库 [copy] O'Brien.db")
	db, err := openNative(path, "rwc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE verify (value TEXT); INSERT INTO verify VALUES ('preserved')"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	db, err = openNative(path, "ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRow("SELECT value FROM verify").Scan(&value); err != nil || value != "preserved" {
		t.Fatalf("native readback: %q %v", value, err)
	}
	if _, err := db.Exec("DELETE FROM verify"); err == nil {
		t.Fatal("readonly mode permitted a write")
	}
}
