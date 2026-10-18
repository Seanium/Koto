package koto

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/hub"
)

func TestOpenCodeEnvironmentDatabaseIsItsOwnProfile(t *testing.T) {
	s := newSandbox(t)
	t.Setenv("XDG_DATA_HOME", filepath.Join(s.Root, "xdg"))
	files := []string{filepath.Join(s.Root, "account A.db"), filepath.Join(s.Root, "account B.db")}
	const sid = "ses_same-native-id"
	hashes := map[string]string{}
	for i, file := range files {
		db, err := sql.Open("sqlite", shared.FileURL(file).String())
		must(t, err)
		_, err = db.Exec(`CREATE TABLE session(id TEXT PRIMARY KEY,directory TEXT,title TEXT,time_created INTEGER,time_updated INTEGER,time_archived INTEGER);
CREATE TABLE message(id TEXT PRIMARY KEY,session_id TEXT,time_created INTEGER,time_updated INTEGER,data TEXT);
CREATE TABLE part(id TEXT PRIMARY KEY,message_id TEXT,session_id TEXT,time_created INTEGER,time_updated INTEGER,data TEXT);`)
		must(t, err)
		_, err = db.Exec("INSERT INTO session VALUES (?,?,?,1,1,NULL)", sid, s.Root, []string{"ACCOUNT A", "ACCOUNT B"}[i])
		must(t, err)
		_, err = db.Exec("INSERT INTO message VALUES ('msg_same',?,1,1,?)", sid, `{"role":"user","time":{"created":1}}`)
		must(t, err)
		_, err = db.Exec("INSERT INTO part VALUES ('prt_same','msg_same',?,1,1,?)", sid, core.ToJSON(map[string]any{"type": "text", "text": []string{"ONLY A", "ONLY B"}[i]}))
		must(t, err)
		must(t, db.Close())
		b, err := os.ReadFile(file)
		must(t, err)
		hashes[file] = core.SHA256Hex(b)
	}
	open := func(file string) *App {
		t.Setenv("OPENCODE_DB", file)
		return s.app(enable("opencode", "pi"), func(c *Config) { c.OpenCodeRoot = "" })
	}
	a := open(files[0])
	c := a.Registry.Get("opencode")
	eq(t, c.Source.(interface{ DatabasePath() string }).DatabasePath(), files[0])
	eq(t, len(newAPI(a).Reindex(ctx).Errors), 0)
	selector := core.SessionSelector(c.ID, "opencode", sid)
	page, err := newAPI(a).Thread(ctx, selector, 0, "")
	must(t, err)
	ok(t, strings.Contains(core.ToJSON(page.Events), "ONLY A"))
	v := a.WithResume(hub.Binding{Harness: "opencode", Connector: c.ID, Session: sid})
	ok(t, v.Resume != nil)
	eq(t, v.Resume.Env["OPENCODE_DB"], files[0])
	// The same XDG root and same native ID must not redirect an existing selector
	// when the caller selects a different official OpenCode database.
	b := open(files[1])
	ok(t, b.Registry.Get("opencode").ID != c.ID)
	eq(t, len(newAPI(b).Reindex(ctx).Errors), 0)
	_, err = b.ResolveSession(selector)
	ok(t, err != nil)
	page, err = newAPI(b).Thread(ctx, "opencode:"+sid, 0, "")
	must(t, err)
	ok(t, strings.Contains(core.ToJSON(page.Events), "ONLY B"))
	ok(t, !strings.Contains(core.ToJSON(page.Events), "ONLY A"))
	for file, hash := range hashes {
		content, err := os.ReadFile(file)
		must(t, err)
		eq(t, core.SHA256Hex(content), hash)
	}
}
