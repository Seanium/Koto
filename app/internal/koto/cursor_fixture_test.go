package koto

import (
	"crypto/md5"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/connectors/shared"
	"koto/internal/core"
)

// This snapshot was exported from four real turns made by the official local
// Cursor CLI, using its own codec. System text and host paths were sanitized;
// neither koto's planner nor its writer produced these source records.
func writeCursorNativeFixture(t *testing.T, root string) (string, string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "connectors", "cursor", "testdata", "native-cli-2026.10.json"))
	must(t, err)
	var f struct {
		ID       string                      `json:"id"`
		Metadata core.JsonObject             `json:"metadata"`
		Blobs    []struct{ ID, Data string } `json:"blobs"`
	}
	must(t, json.Unmarshal(raw, &f))
	workspace := md5.Sum([]byte("/example/koto"))
	file := filepath.Join(root, "chats", hex.EncodeToString(workspace[:]), f.ID, "store.db")
	must(t, os.MkdirAll(filepath.Dir(file), 0700))
	db, err := sql.Open("sqlite", shared.FileURL(file).String())
	must(t, err)
	defer db.Close()
	_, err = db.Exec("PRAGMA user_version=1; CREATE TABLE blobs(id TEXT PRIMARY KEY,data BLOB); CREATE TABLE meta(key TEXT PRIMARY KEY,value TEXT)")
	must(t, err)
	tx, err := db.Begin()
	must(t, err)
	defer tx.Rollback()
	for _, b := range f.Blobs {
		data, err := base64.StdEncoding.DecodeString(b.Data)
		must(t, err)
		eq(t, core.SHA256Hex(data), b.ID)
		_, err = tx.Exec("INSERT INTO blobs VALUES(?,?)", b.ID, data)
		must(t, err)
	}
	metadata, err := json.Marshal(f.Metadata)
	must(t, err)
	_, err = tx.Exec("INSERT INTO meta VALUES('0',?)", hex.EncodeToString(metadata))
	must(t, err)
	must(t, tx.Commit())
	must(t, db.Close())
	return file, f.ID
}

func TestCursorNativeFixtureSelectionTargets(t *testing.T) {
	for _, target := range copyAgents {
		t.Run(target, func(t *testing.T) {
			requireCopyAgent(t, "cursor", target)
			s := newSandbox(t)
			file, id := writeCursorNativeFixture(t, filepath.Join(s.Root, "cursor"))
			original := text(t, file)
			a := s.app(enable("cursor", target))
			api := newAPI(a)
			api.Reindex(ctx)
			source, err := api.Thread(ctx, "cursor:"+id, 0, "")
			must(t, err)
			messages := []*core.Event{}
			for _, event := range source.Events {
				if event.Kind == "message" {
					messages = append(messages, event)
				}
			}
			eq(t, len(messages), 8, "native fixture must have four complete rounds")
			for _, choice := range []struct {
				name    string
				indices []int
			}{
				{"all", []int{0, 1, 2, 3, 4, 5, 6, 7}},
				{"middle", []int{2, 3}},
				{"reply-only", []int{5}},
				{"question-only", []int{2}},
				{"nonconsecutive", []int{0, 1, 4, 5}},
			} {
				t.Run(choice.name, func(t *testing.T) {
					ids := []string{}
					selected := map[int]bool{}
					for _, i := range choice.indices {
						ids = append(ids, messages[i].ID)
						selected[i] = true
					}
					r, err := api.Carry(ctx, CarryRequest{From: source.Head, To: target, Cwd: s.Root, EventIDs: ids})
					must(t, err)
					ok(t, r.Applied)
					out, err := api.Thread(ctx, target+":"+r.Plan.Target.Session, 30, "")
					must(t, err)
					body := core.ToJSON(out.Events)
					byRole := map[string]string{}
					for _, event := range out.Events {
						byRole[event.Actor] += core.ToJSON(event.Parts)
					}
					for i := range 8 {
						role := "user"
						marker := "NATIVE_ROUND_" + itoa(i/2+1) + "_中文🐙"
						if i%2 == 1 {
							role = "assistant"
							marker = "ACK_" + itoa(i/2+1)
						}
						eq(t, strings.Contains(byRole[role], marker), selected[i], marker)
					}
					ok(t, !strings.Contains(body, "Synthetic fixture system context"), "selected copy included unselected system context")
					eq(t, text(t, file), original, "native source DB changed")
				})
			}
		})
	}
}
