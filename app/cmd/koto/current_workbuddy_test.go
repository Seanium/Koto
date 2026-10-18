package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"koto/internal/connectors/shared"
	"koto/internal/connectors/workbuddy"
	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/koto"
)

func seedWorkBuddyHost(t *testing.T, root, cwd, id, marker string) string {
	t.Helper()
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", shared.FileURL(filepath.Join(root, "workbuddy.db")).String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema, err := os.ReadFile(filepath.Join("..", "..", "internal", "connectors", "workbuddy", "testdata", "schema-5.8.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions(id,cwd,user_id,title,status,created_at,updated_at) VALUES(?,?,'test-account','current','working',1,1)`, id, cwd); err != nil {
		t.Fatal(err)
	}
	rows := []core.JsonObject{
		{"id": "system", "type": "message", "role": "system", "content": "SOURCE SYSTEM EXCLUDED"},
		{"id": "question", "type": "message", "role": "user", "content": marker + " question"},
		{"id": "reply", "type": "message", "role": "assistant", "content": marker + " answer"},
		{"id": "migration", "type": "message", "role": "user", "content": "MIGRATION REQUEST EXCLUDED"},
		{"id": "call", "type": "function_call", "name": "Bash", "callId": "pending-copy", "arguments": `{"command":"koto copy --current --to pi --json"}`},
	}
	var out bytes.Buffer
	for i, row := range rows {
		row["timestamp"] = time.Now().UnixMilli()
		if i > 0 {
			row["parentId"] = rows[i-1]["id"]
		}
		if err := json.NewEncoder(&out).Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(root, "projects", workbuddy.ProjectKey(cwd), id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, out.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWorkBuddyCurrentCopyKeepsRegionsSeparate(t *testing.T) {
	for _, region := range []string{"workbuddy", "workbuddy-ai"} {
		t.Run(region, func(t *testing.T) {
			s := newHandoffSandbox(t)
			cn, intl := filepath.Join(s.root, "workbuddy"), filepath.Join(s.root, "workbuddy-ai")
			files := map[string]string{
				"workbuddy":    seedWorkBuddyHost(t, cn, s.cwd, testThread, "DOMESTIC"),
				"workbuddy-ai": seedWorkBuddyHost(t, intl, s.cwd, testThread, "INTERNATIONAL"),
			}
			root, marker, other := cn, "DOMESTIC", "INTERNATIONAL"
			if region == "workbuddy-ai" {
				root, marker, other = intl, other, marker
			}
			before, _ := os.ReadFile(files[region])
			env := map[string]string{"WORKBUDDY_CONFIG_DIR": root, "CODEBUDDY_SESSION_ID": testThread, "CLAUDE_SESSION_ID": testThread, "WORKBUDDY_DATA_FOLDER_NAME": "." + region}
			flags := []string{"--only", "workbuddy,workbuddy-ai,pi", "--workbuddy-root", cn, "--workbuddy-ai-root", intl}
			r, code, raw := s.run(t, []string{"current"}, flags, env)
			if code != 0 || !r.OK || r.Source.Selector != "@"+engine.ProfileID(region, root, "desktop")+":"+testThread {
				t.Fatalf("current: %d %s", code, raw)
			}
			flags = append(flags, "--current", "--to", "pi")
			r, code, raw = s.run(t, []string{"copy"}, flags, env)
			if code != 0 || !r.OK || r.State != "copied" || r.Counts.Turns != 1 || r.Counts.Messages != 2 || r.Deferred.Events != 2 {
				t.Fatalf("copy: %d %s", code, raw)
			}
			for _, file := range nativeJSONLs(t, s.pi) {
				b, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Contains(b, []byte(marker+" question")) || !bytes.Contains(b, []byte(marker+" answer")) {
					t.Fatal("missing completed content")
				}
				for _, absent := range []string{other, "SOURCE SYSTEM", "MIGRATION REQUEST", "pending-copy"} {
					if bytes.Contains(b, []byte(absent)) {
						t.Fatalf("leaked %s", absent)
					}
				}
			}
			after, _ := os.ReadFile(files[region])
			if !bytes.Equal(before, after) {
				t.Fatal("source changed")
			}
		})
	}
}

func TestWorkBuddyCustomRootCopyDoesNotInventResume(t *testing.T) {
	for _, region := range []string{"workbuddy", "workbuddy-ai"} {
		t.Run(region, func(t *testing.T) {
			s := newHandoffSandbox(t)
			s.writeCodex(t, testThread, "COPIED CONTENT", false)
			root := filepath.Join(s.root, region)
			seedWorkBuddyHost(t, root, s.cwd, otherThread, "EXISTING ACCOUNT")
			flag := "--workbuddy-root"
			if region == "workbuddy-ai" {
				flag = "--workbuddy-ai-root"
			}
			r, code, raw := s.run(t, []string{"copy", "codex:" + testThread}, []string{"--only", "codex," + region, flag, root, "--to", region}, nil)
			if code != 0 || !r.OK || r.State != "copied" || r.Destination == nil || r.Destination.Agent != region || r.Counts.Messages != 4 {
				t.Fatalf("copy: %d %s", code, raw)
			}
			if r.Resume != nil || strings.Contains(raw, `"resume":`) || !strings.Contains(r.Instructions, "Do not invent a resume command") {
				t.Fatalf("custom root command could launch another profile: %s", raw)
			}
			var out bytes.Buffer
			printHandoff(&out, &r)
			if !strings.Contains(out.String(), r.Destination.Selector) || !strings.Contains(out.String(), "matching App/profile") || strings.Contains(out.String(), "continue with:") {
				t.Fatalf("plain-text copy receipt: %s", out.String())
			}
		})
	}
}

func TestWorkBuddyHostRequiresMatchingEnabledRoot(t *testing.T) {
	s := newHandoffSandbox(t)
	root := filepath.Join(s.root, "workbuddy")
	seedWorkBuddyHost(t, root, s.cwd, testThread, "SOURCE")
	for _, test := range []struct {
		name       string
		env        map[string]string
		only, code string
	}{
		{"codebuddy-only", map[string]string{"CODEBUDDY_SESSION_ID": testThread}, "workbuddy", "CURRENT_SESSION_UNKNOWN"},
		{"unregistered-root", map[string]string{"WORKBUDDY_CONFIG_DIR": s.cwd, "CODEBUDDY_SESSION_ID": testThread}, "workbuddy", "SOURCE_NOT_FOUND"},
		{"disabled", map[string]string{"WORKBUDDY_CONFIG_DIR": root, "CODEBUDDY_SESSION_ID": testThread}, "pi", "SOURCE_NOT_FOUND"},
		{"stale-id", map[string]string{"WORKBUDDY_CONFIG_DIR": root, "CODEBUDDY_SESSION_ID": otherThread}, "workbuddy", "SOURCE_NOT_FOUND"},
		{"inherited-other-agent", map[string]string{"WORKBUDDY_CONFIG_DIR": root, "CODEBUDDY_SESSION_ID": testThread, "PI_SESSION_ID": otherThread}, "workbuddy", "CURRENT_SESSION_AMBIGUOUS"},
		{"native-ids-disagree", map[string]string{"WORKBUDDY_CONFIG_DIR": root, "CODEBUDDY_SESSION_ID": testThread, "CLAUDE_SESSION_ID": otherThread}, "workbuddy", "CURRENT_SESSION_AMBIGUOUS"},
		{"wrong-edition", map[string]string{"WORKBUDDY_CONFIG_DIR": root, "CODEBUDDY_SESSION_ID": testThread, "WORKBUDDY_DATA_FOLDER_NAME": ".workbuddy-ai"}, "workbuddy", "CURRENT_SESSION_AMBIGUOUS"},
		{"relative-root", map[string]string{"WORKBUDDY_CONFIG_DIR": "workbuddy", "CODEBUDDY_SESSION_ID": testThread}, "workbuddy", "CURRENT_SESSION_UNKNOWN"},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, code, raw := s.run(t, []string{"current"}, []string{"--only", test.only, "--workbuddy-root", root}, test.env)
			if code == 0 || r.Error == nil || r.Error.Code != test.code {
				t.Fatalf("wanted %s: %d %s", test.code, code, raw)
			}
		})
	}
	// The explicitly supplied selector still takes precedence over inherited identities.
	r, code, raw := s.run(t, []string{"current"}, []string{"--only", "workbuddy", "--workbuddy-root", root}, map[string]string{"KOTO_SESSION": "workbuddy:" + testThread, "WORKBUDDY_CONFIG_DIR": s.cwd, "PI_SESSION_ID": otherThread})
	if code != 0 || r.ResolvedBy != "KOTO_SESSION" {
		t.Fatalf("explicit: %d %s", code, raw)
	}
}

func TestWorkBuddyCurrentLimitsDiscoveryToCallingRoot(t *testing.T) {
	s := newHandoffSandbox(t)
	first, second := filepath.Join(s.root, "first"), filepath.Join(s.root, "second")
	seedWorkBuddyHost(t, first, s.cwd, testThread, "OTHER ROOT")
	want := seedWorkBuddyHost(t, second, s.cwd, testThread, "CALLING ROOT")
	app, err := koto.New(koto.Config{DataDir: filepath.Join(s.root, "hub"), WorkBuddyRoot: first, Enable: []string{"workbuddy"}, Extra: []*engine.Connector{workbuddy.New(workbuddy.Options{Root: second, ID: "workbuddy-extra"})}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	env := map[string]string{"WORKBUDDY_CONFIG_DIR": second, "CODEBUDDY_SESSION_ID": testThread}
	sel, by, connector, err := workBuddySourceSelector(app, func(k string) string { return env[k] })
	if err != nil || connector != "workbuddy-extra" {
		t.Fatalf("%s %v", connector, err)
	}
	native, _, err := discoverSource(context.Background(), app, sel, by, connector)
	if err != nil || native.ref.Path != want {
		t.Fatalf("%+v %v", native.ref, err)
	}
	// A symlink is the same App root, not a separate account or edition.
	alias := filepath.Join(s.root, "alias")
	if err := os.Symlink(second, alias); err == nil {
		env["WORKBUDDY_CONFIG_DIR"] = alias
		_, _, connector, err = workBuddySourceSelector(app, func(k string) string { return env[k] })
		if err != nil || connector != "workbuddy-extra" {
			t.Fatalf("alias: %s %v", connector, err)
		}
	}
	if strings.Contains(by, "running-tool") {
		t.Fatal("native ID should not claim nonce proof")
	}
}
