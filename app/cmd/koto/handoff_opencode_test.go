package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"koto/internal/core"
)

// A database is shared by many conversations. Compare this source's rows rather
// than the entire database file when a same-Agent copy inserts another session.
func matrixSourceBytes(t *testing.T, ref core.NativeRef) []byte {
	t.Helper()
	if ref.Harness != "opencode" {
		b, err := os.ReadFile(ref.Path)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	db, err := sql.Open("sqlite", ref.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var out bytes.Buffer
	for _, query := range []string{
		`SELECT id,directory,title,time_updated,time_archived FROM session WHERE id=?`,
		`SELECT id,data FROM message WHERE session_id=? ORDER BY id`,
		`SELECT id,data FROM part WHERE session_id=? ORDER BY id`,
	} {
		rows, err := db.Query(query, ref.Session)
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			if err := json.NewEncoder(&out).Encode(values); err != nil {
				t.Fatal(err)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	return out.Bytes()
}

func appendOpenCodeHandoff(t *testing.T, ref core.NativeRef, texts, ids []string) {
	t.Helper()
	db, err := sql.Open("sqlite", ref.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stamp := time.Now().UnixMilli() + 1000
	for i, text := range texts {
		userID, assistantID := "msg_zz"+ids[i]+"u", "msg_zz"+ids[i]+"a"
		for j, item := range []struct {
			id         string
			info, part map[string]any
		}{
			{userID, map[string]any{"role": "user", "time": map[string]any{"created": stamp + int64(i*2)}, "model": map[string]any{"providerID": "fixture", "modelID": "fixture"}}, map[string]any{"type": "text", "text": text}},
			{assistantID, map[string]any{"role": "assistant", "parentID": userID, "time": map[string]any{"created": stamp + int64(i*2+1)}, "providerID": "fixture", "modelID": "fixture"}, map[string]any{"type": "tool", "tool": "bash", "callID": ids[i], "state": map[string]any{"status": "running", "input": map[string]any{"command": "koto copy --current --to pi"}}}},
		} {
			when := stamp + int64(i*2+j)
			data, _ := json.Marshal(item.info)
			if _, err := tx.Exec(`INSERT INTO message(id,session_id,time_created,time_updated,data) VALUES(?,?,?,?,?)`, item.id, ref.Session, when, when, string(data)); err != nil {
				t.Fatal(err)
			}
			data, _ = json.Marshal(item.part)
			if _, err := tx.Exec(`INSERT INTO part(id,message_id,session_id,time_created,time_updated,data) VALUES(?,?,?,?,?,?)`, "prt_"+item.id, item.id, ref.Session, when, when, string(data)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err = tx.Exec(`UPDATE session SET time_updated=? WHERE id=?`, stamp+5, ref.Session); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
