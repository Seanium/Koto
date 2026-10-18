package cursor

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"

	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/hub"
	"koto/internal/planner"
)

func TestCLINativeSelectedRoundTrip(t *testing.T) {
	c := NewCLI(CLIOptions{Root: t.TempDir(), Command: []string{"cursor-agent"}}).Source.(*cliConnector)
	h := hub.New()
	material := []byte("FILE_SELECTED_5937 中文🐙")
	u := projected("user", "Selected round, not prior system or omitted questions")
	u.Parts = append(u.Parts, core.Part{"type": "file", "name": "test.txt", "mediaType": "text/plain", "blob": h.PutBlob(material)})
	p, e := c.Plan(engine.PlanInput{Hub: h, Projection: &planner.Projection{Items: []planner.Item{u, projected("assistant", "Selected reply")}}, Target: engine.DeliveryTarget{Mode: "create", Session: c.NewSessionID("selection"), Cwd: c.root, Title: "CLI copied"}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Apply(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	refs, e := c.Discover(context.Background())
	if e != nil || len(refs) != 1 {
		t.Fatalf("discover %v %v", refs, e)
	}
	var drafts []core.DraftEvent
	var blobs [][]byte
	var offset core.SourceOffset
	e = c.Tail(context.Background(), refs[0], nil, func(b core.SourceBatch) error {
		drafts = append(drafts, b.Drafts...)
		blobs = append(blobs, b.Blobs...)
		offset = b.Offset
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if len(drafts) != 2 {
		t.Fatalf("selected events %d", len(drafts))
	}
	if drafts[0].Actor != "user" || drafts[1].Actor != "assistant" {
		t.Fatal("roles changed")
	}
	found := false
	for _, b := range blobs {
		if string(b) == string(material) {
			found = true
		}
	}
	if !found {
		t.Fatal("file bytes lost")
	}
	if refs[0].Meta["cwd"] != c.root {
		t.Fatal("cwd lost")
	}
	if e = c.Tail(context.Background(), refs[0], &offset, func(core.SourceBatch) error { t.Fatal("unchanged session emitted events"); return nil }); e != nil {
		t.Fatal(e)
	}
	if out, e := c.Apply(context.Background(), p); e != nil || out["noop"] != true {
		t.Fatalf("idempotency %v %v", out, e)
	}
	// A resumed or manually edited target must never be overwritten by replaying a plan.
	if e = os.WriteFile(filepath.Join(filepath.Dir(p.Target.Path), "assets", core.SHA256Hex(material), "test.txt"), []byte("edited"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Apply(context.Background(), p); e == nil {
		t.Fatal("overwrote altered attachment")
	}
	argv, env := c.NativeResume(p.Target.Session)
	if !reflect.DeepEqual(argv, []string{"cursor-agent", "--resume", p.Target.Session}) || env["CURSOR_CONFIG_DIR"] != c.root {
		t.Fatal("wrong local CLI resume command or root", argv, env)
	}
}
func TestCLIWriteRejectsInvalidDeliveryAndCancelledApply(t *testing.T) {
	c := NewCLI(CLIOptions{Root: t.TempDir()}).Source.(*cliConnector)
	p, e := c.Plan(engine.PlanInput{Hub: hub.New(), Projection: &planner.Projection{Items: []planner.Item{projected("user", "Hello")}}, Target: engine.DeliveryTarget{Mode: "create", Session: c.NewSessionID("cancel"), Cwd: c.root}})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = c.Apply(ctx, p); e == nil {
		t.Fatal("cancelled apply succeeded")
	}
	if _, e = os.Stat(filepath.Dir(p.Target.Path)); !os.IsNotExist(e) {
		t.Fatalf("failed copy retained session: %v", e)
	}
	var d cliDelivery
	if e = json.Unmarshal([]byte(p.Content), &d); e != nil {
		t.Fatal(e)
	}
	for k := range d.Blobs {
		d.Blobs[k] = []byte("corrupted")
		break
	}
	b, _ := json.Marshal(d)
	p.Content = string(b)
	p.ID = "plan:" + core.SHA256Hex(p.Content)
	if _, e = c.Apply(context.Background(), p); e == nil {
		t.Fatal("accepted corrupted content")
	}
	p.Target.Path = filepath.Join(c.root, "outside.db")
	if _, e = c.Apply(context.Background(), p); e == nil {
		t.Fatal("accepted escaped target")
	}
	argv, _ := c.NativeResume(p.Target.Session)
	if len(argv) != 3 || argv[0] != "cursor-agent" {
		t.Fatal("wrong standard CLI resume", argv)
	}
}
func TestCLIReaderRejectsUnknownDatabaseAndSymlink(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "store.db")
	if e := os.WriteFile(file, []byte("not sqlite"), 0600); e != nil {
		t.Fatal(e)
	}
	if db, e := openCLI(context.Background(), file); e == nil {
		db.Close()
		t.Fatal("accepted invalid DB")
	}
	link := filepath.Join(root, "link.db")
	if e := os.Symlink(file, link); e != nil {
		t.Skip(e)
	}
	if db, e := openCLI(context.Background(), link); e == nil {
		db.Close()
		t.Fatal("followed symlink DB")
	}
}

func TestCLIReadAfterNativeSystemPrefixInsertion(t *testing.T) {
	c := NewCLI(CLIOptions{Root: t.TempDir()}).Source.(*cliConnector)
	p, e := c.Plan(engine.PlanInput{Hub: hub.New(), Projection: &planner.Projection{Items: []planner.Item{projected("user", "Selected request"), projected("assistant", "Selected answer")}}, Target: engine.DeliveryTarget{Mode: "create", Session: c.NewSessionID("prefix"), Cwd: c.root}})
	if e != nil {
		t.Fatal(e)
	}
	var d cliDelivery
	if e = json.Unmarshal([]byte(p.Content), &d); e != nil {
		t.Fatal(e)
	}
	root, e := parseWire(d.Blobs[core.S(d.Metadata["latestRootBlobId"])])
	if e != nil {
		t.Fatal(e)
	}
	system, _ := json.Marshal(core.JsonObject{"role": "system", "content": "Target adds its own system on first native resume"})
	sha := core.SHA256Hex(system)
	d.Blobs[sha] = system
	id, _ := hex.DecodeString(sha)
	root = append(wireMessage{wireBytes(1, id)}, root...)
	raw := root.encode()
	sha = core.SHA256Hex(raw)
	d.Blobs[sha] = raw
	d.Metadata["latestRootBlobId"] = sha
	raw, _ = json.Marshal(d)
	p.Content = string(raw)
	p.ID = "plan:" + core.SHA256Hex(p.Content)
	if _, e = c.Apply(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	refs, e := c.Discover(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	var ev []core.DraftEvent
	if e = c.Tail(context.Background(), refs[0], nil, func(b core.SourceBatch) error { ev = append(ev, b.Drafts...); return nil }); e != nil {
		t.Fatal(e)
	}
	if len(ev) != 3 || ev[0].Kind != "context" || ev[1].Actor != "user" || core.S(ev[1].Parts[0]["text"]) != "Selected request" {
		t.Fatalf("native prefix shifted user turn: %+v", ev)
	}
}

func TestCLIIdentityAndResumeKeepStorageIndependentFromRuntime(t *testing.T) {
	root := filepath.Join(t.TempDir(), "isolated config 中文")
	standard := NewCLI(CLIOptions{Root: root})
	command := []string{filepath.Join(t.TempDir(), "Cursor CLI", "cursor-agent"), "--force"}
	local := NewCLI(CLIOptions{Root: root, Command: command})
	if local.ID != standard.ID || local.Root != standard.Root || local.Sink.NewSessionID("same") != standard.Sink.NewSessionID("same") {
		t.Fatal("runtime changed storage identity")
	}
	command[0] = "mutated"
	id := local.Sink.NewSessionID("resume")
	argv := engine.ResumeArgv(local, "cursor", id)
	env := engine.ResumeEnvironment(local, "cursor", id)
	if len(argv) != 4 || filepath.Base(argv[0]) != "cursor-agent" || argv[1] != "--force" || argv[2] != "--resume" || argv[3] != id || env["CURSOR_CONFIG_DIR"] != root {
		t.Fatal("resume lost command argument boundaries or isolated root", argv, env)
	}
	if argv, _ := local.Sink.(*cliConnector).NativeResume("invalid"); len(argv) != 0 {
		t.Fatal("accepted invalid session")
	}
}

func TestCLIWriteSpecialPathAndConcurrentCopy(t *testing.T) {
	name := "config [test] # % 中文"
	if runtime.GOOS != "windows" {
		name += "?"
	}
	root := filepath.Join(t.TempDir(), name)
	c := NewCLI(CLIOptions{Root: root}).Source.(*cliConnector)
	p, err := c.Plan(engine.PlanInput{Hub: hub.New(), Projection: &planner.Projection{Items: []planner.Item{projected("user", "one exact request")}}, Target: engine.DeliveryTarget{Mode: "create", Session: c.NewSessionID("concurrent"), Cwd: root}})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = c.Apply(context.Background(), p) }()
	}
	wg.Wait()
	refs, err := c.Discover(context.Background())
	if err != nil || len(refs) != 1 {
		t.Fatal(refs, err)
	}
	var text string
	err = c.Tail(context.Background(), refs[0], nil, func(b core.SourceBatch) error {
		for _, d := range b.Drafts {
			for _, p := range d.Parts {
				text += core.S(p["text"])
			}
		}
		return nil
	})
	if err != nil || text != "one exact request" {
		t.Fatal("concurrent copies corrupted native database", text, err)
	}
	if out, err := c.Apply(context.Background(), p); err != nil || out["noop"] != true {
		t.Fatal(out, err)
	}
}

func TestCLIPublishPreservesConcurrentNativeTarget(t *testing.T) {
	for _, kind := range []string{"file", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, ".store.db"), []byte("staged copy"), 0600); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(dir, "store.db")
			switch kind {
			case "file":
				if err := os.WriteFile(target, []byte("native original"), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink("native-missing.db", target); err != nil {
					t.Skip(err)
				}
			}
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			if err := publishCLIDatabase(root, "."); err == nil {
				t.Fatal("overwrote native target")
			}
			if kind == "file" {
				b, err := os.ReadFile(target)
				if err != nil || string(b) != "native original" {
					t.Fatal("changed native data", err)
				}
			}
			if kind == "symlink" {
				if got, err := os.Readlink(target); err != nil || got != "native-missing.db" {
					t.Fatal("changed native link", err)
				}
			}
			if st, err := os.Lstat(target); err != nil || (kind == "directory" && !st.IsDir()) {
				t.Fatal("removed native target", err)
			}
		})
	}
}

func TestCLITrashKeepsDatabaseAndAssetsTogether(t *testing.T) {
	c := NewCLI(CLIOptions{Root: t.TempDir()}).Source.(*cliConnector)
	h := hub.New()
	u := projected("user", "attachment")
	u.Parts = append(u.Parts, core.Part{"type": "file", "name": "material.txt", "mediaType": "text/plain", "blob": h.PutBlob([]byte("trash together"))})
	p, err := c.Plan(engine.PlanInput{Hub: h, Projection: &planner.Projection{Items: []planner.Item{u}}, Target: engine.DeliveryTarget{Mode: "create", Session: c.NewSessionID("trash"), Cwd: c.root}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	files := c.Files(engine.SessionRef{Session: p.Target.Session, Path: p.Target.Path})
	if !reflect.DeepEqual(files, []string{filepath.Dir(p.Target.Path)}) {
		t.Fatal("trash detached DB from assets", files)
	}
	if got := c.Files(engine.SessionRef{Session: c.NewSessionID("other"), Path: p.Target.Path}); len(got) != 0 {
		t.Fatal("accepted other session", got)
	}
	if got := c.Files(engine.SessionRef{Session: p.Target.Session, Path: filepath.Join(t.TempDir(), "store.db")}); len(got) != 0 {
		t.Fatal("accepted outside path", got)
	}
}

func TestCLIFileAttachmentsRemainCompleteInEveryRole(t *testing.T) {
	for _, role := range []string{"user", "assistant", "tool"} {
		for _, media := range []string{"text/plain", "application/json", "application/octet-stream", "image/png"} {
			t.Run(role+"/"+media, func(t *testing.T) {
				h := hub.New()
				raw := []byte("FILE_NATIVE_9367 中文🐙")
				if media == "application/octet-stream" {
					raw = []byte{0, 255, 1, 2, 3, 4}
				}
				kind := "file"
				if media == "image/png" {
					kind = "image"
					raw, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aL1sAAAAASUVORK5CYII=")
				}
				part := core.Part{"type": kind, "name": "原样.bin", "mediaType": media, "blob": h.PutBlob(raw)}
				items := []planner.Item{projected("user", "selected request")}
				switch role {
				case "user":
					items[0].Parts = append(items[0].Parts, part)
				case "assistant":
					a := projected("assistant", "selected answer")
					a.Parts = append(a.Parts, part)
					items = append(items, a)
				case "tool":
					a := projected("assistant", "")
					a.Parts = []core.Part{{"type": "tool_call", "callId": "call_fixture", "rawName": "read_file", "args": core.JsonObject{}}}
					r := projected("tool", "")
					r.Parts = []core.Part{{"type": "tool_result", "callId": "call_fixture", "outcome": "success", "parts": core.PartsJSON([]core.Part{part})}}
					items = append(items, a, r)
				}
				c := NewCLI(CLIOptions{Root: t.TempDir()}).Source.(*cliConnector)
				p, err := c.Plan(engine.PlanInput{Hub: h, Projection: &planner.Projection{Items: items}, Target: engine.DeliveryTarget{Mode: "create", Session: c.NewSessionID("files"), Cwd: c.root}})
				if err != nil {
					t.Fatal(err)
				}
				if role == "assistant" || media == "application/octet-stream" || role == "tool" && kind == "file" {
					referenced := false
					for _, loss := range p.Losses {
						if loss.Kind == "referenced" {
							referenced = true
						}
					}
					if !referenced {
						t.Fatal("file representation change was not reported")
					}
				}
				if _, err = c.Apply(context.Background(), p); err != nil {
					t.Fatal(err)
				}
				refs, err := c.Discover(context.Background())
				if err != nil || len(refs) != 1 {
					t.Fatal(refs, err)
				}
				found := false
				err = c.Tail(context.Background(), refs[0], nil, func(b core.SourceBatch) error {
					for _, event := range b.Drafts {
						if event.Actor != role {
							continue
						}
						var walk func([]core.Part)
						walk = func(ps []core.Part) {
							for _, p := range ps {
								if core.S(p["type"]) == "tool_result" {
									walk(core.Parts(p["parts"]))
								}
								if core.S(p["type"]) == kind && core.S(core.Obj(p["blob"])["sha256"]) == core.SHA256Hex(raw) {
									found = true
								}
							}
						}
						walk(event.Parts)
					}
					return nil
				})
				if err != nil || !found {
					t.Fatal("attachment lost or moved to another role", found, err)
				}
			})
		}
	}
}

func TestCLITextFilesDoNotGrowAcrossCopies(t *testing.T) {
	for _, ordinary := range []string{"", "[Attached file: note.txt]\nEXACT_FILE_BYTES", "[Attached file: note.txt]\nThis is ordinary user text"} {
		t.Run(ordinary, func(t *testing.T) {
			c := NewCLI(CLIOptions{Root: t.TempDir()}).Source.(*cliConnector)
			h := hub.New()
			raw := []byte("EXACT_FILE_BYTES")
			u := projected("user", "READ_FILE")
			expectedText := []string{"READ_FILE"}
			if ordinary != "" {
				u.Parts = append(u.Parts, core.Part{"type": "text", "text": ordinary})
				expectedText = append(expectedText, ordinary)
			}
			for _, name := range []string{"note.txt", "same-content.txt"} {
				u.Parts = append(u.Parts, core.Part{"type": "file", "name": name, "mediaType": "text/plain", "blob": h.PutBlob(raw)})
			}
			items := []planner.Item{u, projected("assistant", "OK")}
			for round := range 3 {
				p, err := c.Plan(engine.PlanInput{Hub: h, Projection: &planner.Projection{Items: items}, Target: engine.DeliveryTarget{Mode: "create", Session: c.NewSessionID("repeat" + string(rune('0'+round))), Cwd: c.root}})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = c.Apply(context.Background(), p); err != nil {
					t.Fatal(err)
				}
				var drafts []core.DraftEvent
				err = c.Tail(context.Background(), core.NativeRef{Path: p.Target.Path, Session: p.Target.Session}, nil, func(b core.SourceBatch) error {
					drafts = append(drafts, b.Drafts...)
					for _, blob := range b.Blobs {
						h.PutBlob(blob)
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				texts := []string{}
				names := []string{}
				for _, d := range drafts {
					if d.Actor == "user" {
						for _, part := range d.Parts {
							switch core.S(part["type"]) {
							case "text":
								texts = append(texts, core.S(part["text"]))
							case "file":
								names = append(names, core.S(part["name"]))
							}
						}
					}
				}
				if !reflect.DeepEqual(texts, expectedText) || !reflect.DeepEqual(names, []string{"note.txt", "same-content.txt"}) {
					t.Fatalf("copy %d duplicated text or lost same-content files: text=%q names=%q", round+1, texts, names)
				}
				items = nil
				for _, d := range drafts {
					items = append(items, planner.Item{Key: d.NativeID, Role: d.Actor, Parts: d.Parts, Event: &core.Event{ID: d.NativeID, Kind: d.Kind}})
				}
			}
		})
	}
}

func TestExpandedFileMarkerDoesNotRemoveOrdinaryText(t *testing.T) {
	text := "[Attached file: note.txt]\nEXACT_FILE_BYTES"
	parts := []core.Part{{"type": "text", "text": text}}
	if got := normalizeExpandedFiles(parts, text, []string{text}); !reflect.DeepEqual(got, parts) {
		t.Fatal("removed authored text without a generated expansion")
	}
}

func TestToolFallbackIsComparedToCompleteStructuredText(t *testing.T) {
	image := core.JsonObject{"type": "image", "mimeType": "image/png", "data": "aW1hZ2U="}
	first := core.JsonObject{"type": "text", "text": "FIRST"}
	second := core.JsonObject{"type": "text", "text": "SECOND"}
	for _, test := range []struct {
		name, fallback string
		extra          []any
		want           []string
		images         int
	}{
		{"two blocks", "FIRST\nSECOND", []any{first, second}, []string{"FIRST", "SECOND"}, 0},
		{"mixed image", "FIRST\nSECOND", []any{first, image, second}, []string{"FIRST", "SECOND"}, 1},
		{"different fallback", "additional", []any{first, second}, []string{"additional", "FIRST", "SECOND"}, 0},
		{"empty with image", "", []any{image}, []string{}, 1},
		{"empty text block", "", []any{core.JsonObject{"type": "text", "text": ""}}, []string{""}, 0},
		{"empty structured", "kept", []any{}, []string{"kept"}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			parts, err := modelParts([]any{core.JsonObject{"type": "tool-result", "toolCallId": "call_fixture", "result": test.fallback, "experimental_content": test.extra}}, func(b []byte) core.JsonObject { return core.JsonObject{"sha256": core.SHA256Hex(b)} })
			if err != nil {
				t.Fatal(err)
			}
			texts := []string{}
			images := 0
			for _, p := range core.Parts(parts[0]["parts"]) {
				if p["type"] == "text" {
					texts = append(texts, core.S(p["text"]))
				}
				if p["type"] == "image" {
					images++
				}
			}
			if !reflect.DeepEqual(texts, test.want) || images != test.images {
				t.Fatal(texts, images)
			}
		})
	}
}

func TestCLIEmptyAttachmentIsBlobAndMissingIsError(t *testing.T) {
	for _, corrupt := range []string{"null", "missing"} {
		t.Run(corrupt, func(t *testing.T) {
			c := NewCLI(CLIOptions{Root: t.TempDir()}).Source.(*cliConnector)
			h := hub.New()
			defer h.Close()
			u := projected("user", "Keep the empty file")
			u.Parts = append(u.Parts, core.Part{"type": "file", "name": "empty.txt", "mediaType": "text/plain", "blob": h.PutBlob([]byte{})})
			p, err := c.Plan(engine.PlanInput{Hub: h, Projection: &planner.Projection{Items: []planner.Item{u}}, Target: engine.DeliveryTarget{Mode: "create", Session: c.NewSessionID(corrupt), Cwd: c.root}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = c.Apply(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			if out, err := c.Apply(context.Background(), p); err != nil || out["noop"] != true {
				t.Fatal("empty attachment did not verify on replay", out, err)
			}
			db, err := sql.Open("sqlite", shared.FileURL(p.Target.Path).String())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			sha := core.SHA256Hex([]byte{})
			var kind string
			var size int
			if err := db.QueryRow("SELECT typeof(data),length(data) FROM blobs WHERE id=?", sha).Scan(&kind, &size); err != nil || kind != "blob" || size != 0 {
				t.Fatal("zero-byte content was not stored as BLOB", kind, size, err)
			}
			ref := core.NativeRef{Path: p.Target.Path, Session: p.Target.Session}
			found := false
			if err := c.Tail(context.Background(), ref, nil, func(b core.SourceBatch) error {
				for _, draft := range b.Drafts {
					for _, part := range draft.Parts {
						if part["name"] == "empty.txt" && core.S(core.Obj(part["blob"])["sha256"]) == sha {
							found = true
						}
					}
				}
				return nil
			}); err != nil || !found {
				t.Fatal("empty attachment was lost", found, err)
			}
			query := "UPDATE blobs SET data=NULL WHERE id=?"
			if corrupt == "missing" {
				query = "DELETE FROM blobs WHERE id=?"
			}
			if _, err := db.Exec(query, sha); err != nil {
				t.Fatal(err)
			}
			if err := c.Tail(context.Background(), ref, nil, func(core.SourceBatch) error { return nil }); err == nil {
				t.Fatal("missing or NULL content silently became an empty attachment")
			}
		})
	}
}
