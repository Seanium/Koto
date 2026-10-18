package cursor

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"koto/internal/core"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type fixture struct {
	ID       string                      `json:"id"`
	Metadata core.JsonObject             `json:"metadata"`
	Header   core.JsonObject             `json:"header"`
	Bubbles  []core.JsonObject           `json:"bubbles"`
	Blobs    []struct{ ID, Data string } `json:"blobs"`
}

func setup(t *testing.T) (*connector, fixture) {
	t.Helper()
	b, err := os.ReadFile("testdata/native-3.25.json")
	if err != nil {
		t.Fatal(err)
	}
	var f fixture
	if err = json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	c := New(Options{Root: t.TempDir()}).Source.(*connector)
	if err = os.MkdirAll(filepath.Dir(c.db), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", c.db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE cursorDiskKV(key TEXT PRIMARY KEY,value BLOB);CREATE TABLE composerHeaders(composerId TEXT PRIMARY KEY,workspaceId TEXT,createdAt INTEGER,lastUpdatedAt INTEGER,isArchived INTEGER,isSubagent INTEGER,recency INTEGER,checkpointAt INTEGER,subagentTypeName TEXT,value BLOB)`); err != nil {
		t.Fatal(err)
	}
	insert := func(k string, v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO cursorDiskKV VALUES(?,?)", k, b); err != nil {
			t.Fatal(err)
		}
	}
	insert("composerData:"+f.ID, f.Metadata)
	for _, b := range f.Bubbles {
		insert("bubbleId:"+f.ID+":"+core.S(b["bubbleId"]), b)
	}
	h, _ := json.Marshal(f.Header)
	if _, err = db.Exec("INSERT INTO composerHeaders(composerId,lastUpdatedAt,isArchived,isSubagent,value) VALUES(?,?,0,0,?)", f.ID, 1700000000020, h); err != nil {
		t.Fatal(err)
	}
	for _, x := range f.Blobs {
		b, err := base64.StdEncoding.DecodeString(x.Data)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO cursorDiskKV VALUES(?,?)", "agentKv:blob:"+x.ID, b); err != nil {
			t.Fatal(err)
		}
	}
	return c, f
}
func readAll(t *testing.T, c *connector, id string) ([]core.DraftEvent, [][]byte, core.SourceOffset, error) {
	t.Helper()
	events := []core.DraftEvent{}
	blobs := [][]byte{}
	var offset core.SourceOffset
	err := c.Tail(context.Background(), core.NativeRef{Session: id}, nil, func(b core.SourceBatch) error {
		events = append(events, b.Drafts...)
		blobs = append(blobs, b.Blobs...)
		offset = b.Offset
		return nil
	})
	return events, blobs, offset, err
}
func TestNativeReadPreservesContentAndSource(t *testing.T) {
	c, f := setup(t)
	before, err := os.ReadFile(c.db)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := c.Discover(context.Background())
	if err != nil || len(refs) != 1 || core.S(refs[0].Meta["cwd"]) != "/example/koto" {
		t.Fatalf("discover: %+v %v", refs, err)
	}
	ev, blobs, off, err := readAll(t, c, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev) != 8 {
		t.Fatalf("got %d records", len(ev))
	}
	if ev[0].Kind != "context" || ev[1].Kind != "context" || ev[2].Actor != "user" || core.S(ev[2].Parts[0]["text"]) != "Remember MARKER_FIRST." {
		t.Fatal("native system/user boundary lost")
	}
	if core.S(ev[3].Attrs["stopReason"]) != "stop" || core.S(ev[7].Attrs["stopReason"]) != "stop" {
		t.Fatal("completed turn boundaries lost")
	}
	types := map[string]int{}
	for _, p := range ev[4].Parts {
		types[core.S(p["type"])]++
	}
	if types["image"] != 1 || types["file"] != 3 {
		t.Fatalf("attachments: %v", types)
	}
	raw, _ := json.Marshal(ev)
	for _, text := range []string{"中文🐙", "TOOL_MARKER", "tool_call", "tool_result"} {
		if !strings.Contains(string(raw), text) {
			t.Fatalf("lost %q", text)
		}
	}
	if len(blobs) != 3 {
		t.Fatalf("embedded blobs: %d", len(blobs))
	}
	calls := 0
	err = c.Tail(context.Background(), refs[0], &off, func(core.SourceBatch) error { calls++; return nil })
	if err != nil || calls != 0 {
		t.Fatalf("unchanged read emitted %d batches, %v", calls, err)
	}
	after, _ := os.ReadFile(c.db)
	if core.SHA256Hex(before) != core.SHA256Hex(after) {
		t.Fatal("read changed native database")
	}
}

func TestIDEFileReferencesPreservePortableURIs(t *testing.T) {
	for _, tc := range []struct {
		name, scheme, host, file string
		valid                    bool
	}{
		{"posix", "file", "", "/example/材料 [1].txt", true},
		{"windows-drive", "file", "", "/C:/example/材料 [1].txt", true},
		{"windows-share", "file", "server", "/share/材料 [1].txt", true},
		{"not-file", "https", "example.com", "/材料 [1].txt", false},
		{"relative", "file", "", "relative/材料.txt", false},
		{"invalid-host", "file", "server:8080", "/材料.txt", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, f := setup(t)
			changed := false
			mutate(t, c, func(db *sql.DB) {
				for _, b := range f.Bubbles {
					context := core.Obj(b["context"])
					if len(core.Arr(context["fileSelections"])) == 0 {
						continue
					}
					context["fileSelections"] = []any{core.JsonObject{"uri": core.JsonObject{"scheme": tc.scheme, "authority": tc.host, "path": tc.file}}}
					data, err := json.Marshal(b)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = db.Exec("UPDATE cursorDiskKV SET value=? WHERE key=?", data, "bubbleId:"+f.ID+":"+core.S(b["bubbleId"])); err != nil {
						t.Fatal(err)
					}
					changed = true
				}
			})
			if !changed {
				t.Fatal("fixture has no selected file reference")
			}
			events, _, _, err := readAll(t, c, f.ID)
			if !tc.valid {
				if err == nil || !strings.Contains(err.Error(), "selected-file URI") {
					t.Fatalf("invalid reference accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := (&url.URL{Scheme: tc.scheme, Host: tc.host, Path: tc.file}).String()
			for _, event := range events {
				for _, part := range event.Parts {
					if core.S(part["uri"]) == want && core.S(part["name"]) == "材料 [1].txt" {
						return
					}
				}
			}
			t.Fatalf("portable reference lost: %s", want)
		})
	}
}

func mutate(t *testing.T, c *connector, fn func(*sql.DB)) {
	t.Helper()
	db, err := sql.Open("sqlite", c.db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fn(db)
}
func TestMissingAndCorruptBlobsReject(t *testing.T) {
	for _, mode := range []string{"missing", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			c, f := setup(t)
			state, _ := readState(core.S(f.Metadata["conversationState"]))
			hash := state.all(1)[2]
			key := "agentKv:blob:" + hex.EncodeToString(hash)
			mutate(t, c, func(db *sql.DB) {
				var err error
				if mode == "missing" {
					_, err = db.Exec("DELETE FROM cursorDiskKV WHERE key=?", key)
				} else {
					_, err = db.Exec("UPDATE cursorDiskKV SET value=? WHERE key=?", []byte("changed"), key)
				}
				if err != nil {
					t.Fatal(err)
				}
			})
			_, _, _, err := readAll(t, c, f.ID)
			if err == nil {
				t.Fatal("malformed native content silently accepted")
			}
		})
	}
}
func TestCompactionAndUnmappedTurnReject(t *testing.T) {
	for _, field := range []int{6, 11, 13} {
		t.Run(strconv.Itoa(field), func(t *testing.T) {
			c, f := setup(t)
			encoded := core.S(f.Metadata["conversationState"])
			b, _ := base64.StdEncoding.DecodeString(encoded[1:])
			b = binary.AppendUvarint(b, uint64(field<<3|2))
			b = append(b, 1, 0)
			f.Metadata["conversationState"] = "~" + base64.StdEncoding.EncodeToString(b)
			m, _ := json.Marshal(f.Metadata)
			mutate(t, c, func(db *sql.DB) {
				_, err := db.Exec("UPDATE cursorDiskKV SET value=? WHERE key=?", m, "composerData:"+f.ID)
				if err != nil {
					t.Fatal(err)
				}
			})
			_, _, _, err := readAll(t, c, f.ID)
			if err == nil || !strings.Contains(err.Error(), "unsupported checkpoint") {
				t.Fatalf("compaction accepted: %v", err)
			}
		})
	}
}

func TestSubagentSideRecordsDoNotExpandParentHistory(t *testing.T) {
	for _, field := range []int{16, 24, 30, 31} {
		t.Run(strconv.Itoa(field), func(t *testing.T) {
			c, f := setup(t)
			before, _, _, err := readAll(t, c, f.ID)
			if err != nil {
				t.Fatal(err)
			}
			state, err := readState(core.S(f.Metadata["conversationState"]))
			if err != nil {
				t.Fatal(err)
			}
			// Side state is deliberately unavailable/opaque: it is not part of
			// the parent's model request and must not block or expand its copy.
			state = append(state, wireBytes(field, []byte("CHILD_ONLY_PAYLOAD_not_parent_context")))
			f.Metadata["conversationState"] = "~" + base64.StdEncoding.EncodeToString(state.encode())
			m, _ := json.Marshal(f.Metadata)
			mutate(t, c, func(db *sql.DB) {
				if _, err := db.Exec("UPDATE cursorDiskKV SET value=? WHERE key=?", m, "composerData:"+f.ID); err != nil {
					t.Fatal(err)
				}
			})
			after, _, _, err := readAll(t, c, f.ID)
			if err != nil {
				t.Fatal(err)
			}
			left, _ := json.Marshal(before)
			right, _ := json.Marshal(after)
			if !bytes.Equal(left, right) || bytes.Contains(right, []byte("CHILD_ONLY_PAYLOAD")) {
				t.Fatal("child side state altered parent history")
			}
		})
	}
}
func TestUnknownModelPartReject(t *testing.T) {
	_, err := modelParts([]any{core.JsonObject{"type": "future-attachment", "data": "important"}}, func([]byte) core.JsonObject { return nil })
	if err == nil {
		t.Fatal("unknown content silently discarded")
	}
}
func TestEmptyRootDoesNotCreateDatabase(t *testing.T) {
	c := New(Options{Root: t.TempDir()}).Source.(*connector)
	refs, err := c.Discover(context.Background())
	if err != nil || len(refs) != 0 {
		t.Fatalf("%v %v", refs, err)
	}
	if _, err = os.Stat(c.db); !os.IsNotExist(err) {
		t.Fatal("discovery created database")
	}
}
func TestIDEWriterHasSeparateNativeProfile(t *testing.T) {
	c := New(Options{Root: t.TempDir()})
	if c.Sink == nil || c.Trash != nil || c.Capabilities.Write == nil || c.Variant != "ide" || c.Capabilities.WriteProfile != "cursor-ide-write@1" {
		t.Fatal("ordinary IDE native writer must remain distinct from CLI storage")
	}
}
func TestLiveIDEReadOnly(t *testing.T) {
	root, id := os.Getenv("KOTO_TEST_CURSOR_ROOT"), os.Getenv("KOTO_TEST_CURSOR_ID")
	if root == "" || id == "" {
		t.Skip("requires an explicit isolated synthetic Cursor session")
	}
	c := New(Options{Root: root}).Source.(*connector)
	ev, blobs, _, err := readAll(t, c, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("read %d events and %d embedded blobs from the official app's synthetic session", len(ev), len(blobs))
}
func FuzzWireRejectTruncation(f *testing.F) {
	f.Add([]byte{10, 2, 'o', 'k'})
	f.Add([]byte{10, 255})
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = parseWire(b) })
}
