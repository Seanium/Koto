package cursor

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/hub"
	"koto/internal/planner"
)

func projected(role, text string) planner.Item {
	return planner.Item{Key: role + text, Role: role, Event: &core.Event{ID: role + text, Kind: "message"}, Parts: []core.Part{{"type": "text", "text": text}}}
}
func prepare(t *testing.T, c *connector, key string, items []planner.Item, h *hub.Hub) *engine.DeliveryPlan {
	t.Helper()
	p, e := c.Plan(engine.PlanInput{Projection: &planner.Projection{Items: items}, Target: engine.DeliveryTarget{Mode: "create", Session: c.NewSessionID(key), Title: "Copied test", Cwd: c.root}, Hub: h})
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func TestIDEWithoutProjectUsesNativeEmptyWindow(t *testing.T) {
	c, _ := setup(t)
	h := hub.New()
	u := projected("user", "No repository selected")
	u.Parts = append(u.Parts, core.Part{"type": "file", "name": "材料.txt", "mediaType": "text/plain", "blob": h.PutBlob([]byte("NO_REPO_FILE 中文🐙"))})
	p, err := c.Plan(engine.PlanInput{Hub: h, Projection: &planner.Projection{Items: []planner.Item{u, projected("assistant", "Ready")}}, Target: engine.DeliveryTarget{Mode: "create", Session: c.NewSessionID("no-repo")}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Target.Cwd != "" {
		t.Fatal("invented a project directory", p.Target.Cwd)
	}
	var d ideDelivery
	if err := json.Unmarshal([]byte(p.Content), &d); err != nil {
		t.Fatal(err)
	}
	for _, w := range []core.JsonObject{core.Obj(d.Header["workspaceIdentifier"]), core.Obj(d.Metadata["workspaceIdentifier"]), core.Obj(core.Obj(d.Header["agentLocation"])["environment"])} {
		if len(w) != 1 || w["id"] != "empty-window" {
			t.Fatal("incorrect native no-project identity", w)
		}
	}
	state, err := readState(core.S(d.Metadata["conversationState"]))
	if err != nil || state.has(9) || state.text(22) != "ide" {
		t.Fatal("copied an execution directory into an empty workspace", err)
	}
	if len(d.Assets) == 0 {
		t.Fatal("attachment was not independently materialized")
	}
	assetRoot := filepath.Join(c.root, "User", "workspaceStorage", "empty-window", "koto", p.Target.Session)
	for path := range d.Assets {
		if !strings.HasPrefix(path, assetRoot+string(filepath.Separator)) {
			t.Fatal("attachment depends on another workspace", path)
		}
	}
	if _, err := c.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	events, _, _, err := readAll(t, c, p.Target.Session)
	if err != nil || len(events) != 2 {
		t.Fatal("no-project history could not be read", len(events), err)
	}
	if _, err := c.Apply(context.Background(), p); err != nil {
		t.Fatal("no-project idempotency", err)
	}
}

func TestNativeOriginalTextAndModelContextRemainSeparate(t *testing.T) {
	for _, original := range []string{"An ordinary question 中文🐙", "<timestamp>authored</timestamp>\n<user_query>literal XML</user_query>", ""} {
		t.Run(original, func(t *testing.T) {
			c, _ := setup(t)
			modelText := "<timestamp>native timestamp</timestamp>\n<user_query>\n" + original + "\n</user_query>\nSELECTED_CONTEXT_5831"
			u := projected("user", modelText)
			u.Event.Actor = "user"
			u.Event.Attrs = core.JsonObject{"displayText": original}
			for round := 0; round < 3; round++ {
				p := prepare(t, c, "display-"+original+string(rune('0'+round)), []planner.Item{u, projected("assistant", "answer")}, hub.New())
				var d ideDelivery
				if err := json.Unmarshal([]byte(p.Content), &d); err != nil {
					t.Fatal(err)
				}
				if d.Bubbles[0]["text"] != original {
					t.Fatal("native UI text replaced by model context", d.Bubbles[0]["text"])
				}
				if _, err := c.Apply(context.Background(), p); err != nil {
					t.Fatal(err)
				}
				events, _, _, err := readAll(t, c, p.Target.Session)
				if err != nil || len(events) != 2 {
					t.Fatal("round trip", len(events), err)
				}
				if got, ok := events[0].Attrs["displayText"].(string); !ok || got != original {
					t.Fatal("original input changed or lost", events[0].Attrs)
				}
				if len(events[0].Parts) != 1 || events[0].Parts[0]["text"] != modelText {
					t.Fatal("actual model context changed", events[0].Parts)
				}
				u.Parts = events[0].Parts
				u.Event.Attrs = events[0].Attrs
			}
		})
	}
}

func TestSelectedIRRebuildsIndependentModelHistory(t *testing.T) {
	c, f := setup(t)
	ctx := context.Background()
	db, e := c.open(ctx)
	if e != nil {
		t.Fatal(e)
	}
	source, e := readKV(ctx, db, "composerData:"+f.ID)
	db.Close()
	if e != nil {
		t.Fatal(e)
	}
	p := prepare(t, c, "selection", []planner.Item{projected("user", "Only selected turn 中文🐙"), projected("assistant", "Selected answer")}, hub.New())
	var delivery ideDelivery
	if e := json.Unmarshal([]byte(p.Content), &delivery); e != nil {
		t.Fatal(e)
	}
	state, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(core.S(delivery.Metadata["conversationState"]), "~"))
	if e != nil {
		t.Fatal(e)
	}
	root, e := parseWire(state)
	if e != nil {
		t.Fatal(e)
	}
	if root.has(26) || root.has(27) {
		t.Fatal("new native session must let Cursor initialize timestamp and timezone together")
	}
	if _, e = c.Apply(ctx, p); e != nil {
		t.Fatal(e)
	}
	db, _ = c.open(ctx)
	var metadataType, bubbleType, headerType string
	_ = db.QueryRow("SELECT typeof(value) FROM cursorDiskKV WHERE key=?", "composerData:"+p.Target.Session).Scan(&metadataType)
	_ = db.QueryRow("SELECT typeof(value) FROM cursorDiskKV WHERE key LIKE ? LIMIT 1", "bubbleId:"+p.Target.Session+":%").Scan(&bubbleType)
	_ = db.QueryRow("SELECT typeof(value) FROM composerHeaders WHERE composerId=?", p.Target.Session).Scan(&headerType)
	db.Close()
	if metadataType != "text" || bubbleType != "text" || headerType != "text" {
		t.Fatalf("native JSON values must be SQLite TEXT: %s %s %s", metadataType, bubbleType, headerType)
	}
	ev, _, _, e := readAll(t, c, p.Target.Session)
	if e != nil {
		t.Fatal(e)
	}
	if len(ev) != 2 || ev[0].Kind != "message" || ev[1].Kind != "message" {
		t.Fatalf("unexpected history %+v", ev)
	}
	raw, _ := json.Marshal(ev)
	if strings.Contains(string(raw), "MARKER_FIRST") || strings.Contains(string(raw), "Synthetic system") {
		t.Fatal("unselected native context leaked")
	}
	if x, e := c.Apply(ctx, p); e != nil || x["noop"] != true {
		t.Fatalf("idempotency %v %v", x, e)
	}
	db, _ = c.open(ctx)
	after, e := readKV(ctx, db, "composerData:"+f.ID)
	db.Close()
	if e != nil || !bytes.Equal(source, after) {
		t.Fatal("copy changed source")
	}
	mutate(t, c, func(db *sql.DB) {
		_, e := db.Exec("UPDATE cursorDiskKV SET value=json_set(value,'$.name','Edited by user') WHERE key=?", "composerData:"+p.Target.Session)
		if e != nil {
			t.Fatal(e)
		}
	})
	if _, e = c.Apply(ctx, p); e == nil {
		t.Fatal("overwrote existing target edit")
	}
}
func TestIDESelectionBoundaryAndMaterialBytes(t *testing.T) {
	c, _ := setup(t)
	h := hub.New()
	image := []byte("image bytes")
	doc := []byte("document 中文🐙")
	u := projected("user", "Material turn")
	u.Parts = append(u.Parts, core.Part{"type": "image", "name": "../picture.png", "mediaType": "image/png", "blob": h.PutBlob(image)}, core.Part{"type": "file", "name": "..\\note.txt", "mediaType": "text/plain", "blob": h.PutBlob(doc)})
	p := prepare(t, c, "materials", []planner.Item{u, projected("assistant", "Done"), projected("user", "Next selected turn"), projected("assistant", "Next answer")}, h)
	if _, e := c.Apply(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	ev, blobs, _, e := readAll(t, c, p.Target.Session)
	if e != nil {
		t.Fatal(e)
	}
	if len(ev) != 4 {
		t.Fatalf("event count %d", len(ev))
	}
	wanted := map[string]bool{core.SHA256Hex(image): false, core.SHA256Hex(doc): false}
	for _, b := range blobs {
		wanted[core.SHA256Hex(b)] = true
	}
	for sha, found := range wanted {
		if !found {
			t.Fatalf("lost material %s", sha)
		}
	}
	var d ideDelivery
	if e = json.Unmarshal([]byte(p.Content), &d); e != nil {
		t.Fatal(e)
	}
	for path, want := range d.Assets {
		rel, e := filepath.Rel(c.root, path)
		if e != nil || strings.HasPrefix(rel, "..") {
			t.Fatal("attachment escaped")
		}
		got, e := os.ReadFile(path)
		if e != nil || !bytes.Equal(got, want) {
			t.Fatal("attachment bytes changed")
		}
	}
	if core.S(ev[2].Parts[0]["text"]) != "Next selected turn" {
		t.Fatal("turn boundary mismatch")
	}
}
func TestIDEAssistantOnlyExcerptAndQuestionOnly(t *testing.T) {
	for _, role := range []string{"user", "assistant"} {
		t.Run(role, func(t *testing.T) {
			c, _ := setup(t)
			p := prepare(t, c, role, []planner.Item{projected(role, "Only chosen content")}, hub.New())
			if _, e := c.Apply(context.Background(), p); e != nil {
				t.Fatal(e)
			}
			ev, _, _, e := readAll(t, c, p.Target.Session)
			if e != nil {
				t.Fatal(e)
			}
			if core.S(ev[len(ev)-1].Parts[0]["text"]) != "Only chosen content" {
				t.Fatal("selection not preserved")
			}
			if role == "assistant" && len(p.Losses) != 1 {
				t.Fatal("synthetic leading user must be disclosed")
			}
		})
	}
}
func TestIDEFailedIndexWriteRollsBackAssetsAndRows(t *testing.T) {
	c, _ := setup(t)
	h := hub.New()
	u := projected("user", "Files")
	u.Parts = append(u.Parts, core.Part{"type": "file", "mediaType": "text/plain", "name": "test.txt", "blob": h.PutBlob([]byte("FILE"))})
	p := prepare(t, c, "rollback", []planner.Item{u}, h)
	mutate(t, c, func(d *sql.DB) {
		if _, e := d.Exec(`CREATE TRIGGER reject_target BEFORE INSERT ON composerHeaders BEGIN SELECT RAISE(ABORT,'index failed');END`); e != nil {
			t.Fatal(e)
		}
	})
	if _, e := c.Apply(context.Background(), p); e == nil {
		t.Fatal("expected index write failure")
	}
	db, e := c.open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var count int
	if e = db.QueryRow("SELECT count(*) FROM cursorDiskKV WHERE key=?", "composerData:"+p.Target.Session).Scan(&count); e != nil || count != 0 {
		t.Fatal("partial target left behind", count, e)
	}
	var d ideDelivery
	json.Unmarshal([]byte(p.Content), &d)
	for file := range d.Assets {
		if _, e = os.Stat(file); !os.IsNotExist(e) {
			t.Fatal("failed delivery left attachment")
		}
	}
}

func TestStructuredToolRoundTrip(t *testing.T) {
	c, _ := setup(t)
	h := hub.New()
	u := projected("user", "Read and inspect")
	a := projected("assistant", "First reasoning")
	a.Parts = []core.Part{{"type": "reasoning", "text": "Check content"}, {"type": "tool_call", "callId": "foreign-call-1", "rawName": "read_file", "args": core.JsonObject{"path": "中文.txt", "options": []any{true, nil, float64(2)}}, "tool": core.JsonObject{"op": "read", "path": "中文.txt"}}}
	r := projected("tool", "")
	r.Parts = []core.Part{{"type": "tool_result", "callId": "foreign-call-1", "outcome": "success", "parts": core.PartsJSON([]core.Part{{"type": "text", "text": "RESULT 中文🐙"}, {"type": "image", "mediaType": "image/png", "blob": h.PutBlob([]byte("tool image"))}})}}
	p := prepare(t, c, "tools", []planner.Item{u, a, r, projected("assistant", "Complete")}, h)
	if _, e := c.Apply(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	ev, blobs, _, e := readAll(t, c, p.Target.Session)
	if e != nil {
		t.Fatal(e)
	}
	if len(ev) != 4 {
		t.Fatalf("event count %d", len(ev))
	}
	data, _ := json.Marshal(ev)
	for _, text := range []string{"read_file", nativeToolID(p.Target.Session, "foreign-call-1"), "RESULT 中文🐙", "reasoning", "tool_call", "tool_result", "options"} {
		if !strings.Contains(string(data), text) {
			t.Fatalf("lost %s", text)
		}
	}
	if len(blobs) != 1 || !bytes.Equal(blobs[0], []byte("tool image")) {
		t.Fatal("tool image lost")
	}
	if output := os.Getenv("KOTO_TEST_CURSOR_PLAN_OUTPUT"); output != "" {
		if e = os.WriteFile(output, []byte(p.Content), 0600); e != nil {
			t.Fatal(e)
		}
	}
}
func TestIncompleteToolRejectsBeforeWriting(t *testing.T) {
	c, _ := setup(t)
	call := projected("assistant", "")
	call.Parts = []core.Part{{"type": "tool_call", "callId": "one", "rawName": "Shell", "args": core.JsonObject{"command": "true"}}}
	for _, items := range [][]planner.Item{{projected("user", "Start"), call}, {projected("user", "Start"), call, projected("user", "Next")}} {
		_, e := c.Plan(engine.PlanInput{Projection: &planner.Projection{Items: items}, Target: engine.DeliveryTarget{Mode: "create", Session: c.NewSessionID("badtool"), Cwd: c.root}, Hub: hub.New()})
		if e == nil {
			t.Fatal("orphan tool accepted")
		}
	}
}
func TestExistingBubbleChangeRejected(t *testing.T) {
	c, _ := setup(t)
	p := prepare(t, c, "bubble", []planner.Item{projected("user", "Original")}, hub.New())
	if _, e := c.Apply(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	mutate(t, c, func(db *sql.DB) {
		if _, e := db.Exec("UPDATE cursorDiskKV SET value=json_set(value,'$.text','changed') WHERE key LIKE ?", "bubbleId:"+p.Target.Session+":%"); e != nil {
			t.Fatal(e)
		}
	})
	if _, e := c.Apply(context.Background(), p); e == nil {
		t.Fatal("existing target bubble modification accepted")
	}
}
func TestAttachmentSymlinkEscapeRejected(t *testing.T) {
	c, _ := setup(t)
	h := hub.New()
	u := projected("user", "File")
	u.Parts = append(u.Parts, core.Part{"type": "file", "name": "test.txt", "blob": h.PutBlob([]byte("data"))})
	p := prepare(t, c, "escape", []planner.Item{u}, h)
	var d ideDelivery
	json.Unmarshal([]byte(p.Content), &d)
	outside := t.TempDir()
	for path := range d.Assets {
		dir := filepath.Dir(path)
		if e := os.MkdirAll(filepath.Dir(dir), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.Symlink(outside, dir); e != nil {
			t.Skip(e)
		}
	}
	if _, e := c.Apply(context.Background(), p); e == nil {
		t.Fatal("symlink escape accepted")
	}
	entries, e := os.ReadDir(outside)
	if e != nil || len(entries) != 0 {
		t.Fatal("wrote outside Cursor root")
	}
}

func TestIDEPlanTamperingAndArchivedTargetRejected(t *testing.T) {
	c, _ := setup(t)
	p := prepare(t, c, "tamper", []planner.Item{projected("user", "Selected")}, hub.New())
	bad := *p
	bad.Content = strings.Replace(p.Content, "Selected", "Changed", 1)
	if _, e := c.Apply(context.Background(), &bad); e == nil {
		t.Fatal("modified plan content accepted")
	}
	if _, e := c.Apply(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	mutate(t, c, func(db *sql.DB) {
		if _, e := db.Exec("UPDATE composerHeaders SET isArchived=1 WHERE composerId=?", p.Target.Session); e != nil {
			t.Fatal(e)
		}
	})
	if _, e := c.Apply(context.Background(), p); e == nil {
		t.Fatal("archived target incorrectly treated as unchanged")
	}
}

func TestDataURIRetainsItsMediaType(t *testing.T) {
	c, _ := setup(t)
	raw := []byte("data URI contents 中文🐙")
	b, media, e := c.material(engine.PlanInput{}, core.Part{"type": "file", "uri": "data:text/plain;base64," + base64.StdEncoding.EncodeToString(raw)})
	if e != nil || !bytes.Equal(b, raw) || media != "text/plain" {
		t.Fatalf("data URI mismatch %q %q %v", b, media, e)
	}
}

func TestIDEArbitraryAttachmentsStayReadableAcrossCopy(t *testing.T) {
	c, _ := setup(t)
	h := hub.New()
	for _, tc := range []struct {
		name, role, typ, media string
		data                   []byte
	}{
		{"assistant.txt", "assistant", "file", "text/plain", []byte("ASSISTANT FILE")},
		{"binary.dat", "user", "file", "application/octet-stream", []byte{0, 255, 2, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := projected(tc.role, "attached")
			item.Parts = append(item.Parts, core.Part{"type": tc.typ, "name": tc.name, "mediaType": tc.media, "blob": h.PutBlob(tc.data)})
			p := prepare(t, c, tc.name, []planner.Item{item}, h)
			if _, e := c.Apply(context.Background(), p); e != nil {
				t.Fatal(e)
			}
			refs, e := c.Discover(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			found := false
			for _, ref := range refs {
				if ref.Session != p.Target.Session {
					continue
				}
				e = c.Tail(context.Background(), ref, nil, func(batch core.SourceBatch) error {
					for _, d := range batch.Drafts {
						for _, part := range d.Parts {
							if core.S(part["type"]) == "file" && core.S(part["name"]) == tc.name && core.S(core.Obj(part["blob"])["sha256"]) == core.SHA256Hex(tc.data) {
								found = true
							}
						}
					}
					return nil
				})
				if e != nil {
					t.Fatal(e)
				}
			}
			if !found {
				t.Fatal("IDE copy did not recover full attachment")
			}
		})
	}
}

func TestDeliveryRefusesAnotherStorageProfile(t *testing.T) {
	ide, _ := setup(t)
	cli := NewCLI(CLIOptions{Root: t.TempDir()}).Source.(*cliConnector)
	for _, tc := range []struct {
		name, id, root string
		sink           engine.Sink
	}{{"app", ide.id, ide.root, ide}, {"cli", cli.id, cli.root, cli}} {
		t.Run(tc.name, func(t *testing.T) {
			input := engine.PlanInput{Hub: hub.New(), Projection: &planner.Projection{Items: []planner.Item{projected("user", "profile boundary")}}, Target: engine.DeliveryTarget{Mode: "create", Session: tc.sink.NewSessionID("profile"), Cwd: tc.root, Profile: tc.id, Root: tc.root}}
			for _, field := range []string{"profile", "root"} {
				wrong := input
				if field == "profile" {
					wrong.Target.Profile = "another-profile"
				} else {
					wrong.Target.Root = t.TempDir()
				}
				if _, e := tc.sink.Plan(wrong); e == nil {
					t.Fatal("accepted wrong", field, "during planning")
				}
				p, e := tc.sink.Plan(input)
				if e != nil {
					t.Fatal(e)
				}
				p.Target = wrong.Target
				// Preserve the native path; profile changes alone must reject an otherwise valid delivery.
				if tc.name == "app" {
					p.Target.Path = ide.db
				} else {
					p.Target.Path = filepath.Join(cli.sessionDir(input.Target.Cwd, input.Target.Session), "store.db")
				}
				if _, e = tc.sink.Apply(context.Background(), p); e == nil {
					t.Fatal("accepted wrong", field, "during delivery")
				}
			}
		})
	}
	if New(Options{Root: t.TempDir()}).Variant != "ide" || NewCLI(CLIOptions{Root: t.TempDir()}).Variant != "cli" {
		t.Fatal("variant identity missing")
	}
}
