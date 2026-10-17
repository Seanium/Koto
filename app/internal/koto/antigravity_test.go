package koto

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"

	_ "modernc.org/sqlite"
)

// pbf is a protobuf field for fixtures: an int (varint), a string, or nested fields.
type pbf struct {
	n int
	v any
}

func varint(n uint64) []byte {
	var b []byte
	for {
		c := byte(n & 0x7f)
		n >>= 7
		if n != 0 {
			c |= 0x80
		}
		b = append(b, c)
		if n == 0 {
			return b
		}
	}
}

func pbEnc(fields []pbf) []byte {
	var out []byte
	for _, f := range fields {
		switch v := f.v.(type) {
		case int:
			out = append(append(out, varint(uint64(f.n<<3))...), varint(uint64(v))...)
		case string:
			out = append(append(append(out, varint(uint64(f.n<<3|2))...), varint(uint64(len(v)))...), v...)
		case []pbf:
			body := pbEnc(v)
			out = append(append(append(out, varint(uint64(f.n<<3|2))...), varint(uint64(len(body)))...), body...)
		}
	}
	return out
}

func agyTS(s int) pbf { return pbf{5, []pbf{{1, []pbf{{1, s}, {2, 0}}}}} }

type agyStep struct {
	typ, status int
	payload     []pbf
}

func openDB(t *testing.T, file string) *sql.DB {
	db, err := sql.Open("sqlite", "file:"+file)
	must(t, err)
	db.SetMaxOpenConns(1)
	return db
}

func writeAgy(t *testing.T, root, id string, steps []agyStep) string {
	must(t, os.MkdirAll(filepath.Join(root, "conversations"), 0o755))
	file := filepath.Join(root, "conversations", id+".db")
	db := openDB(t, file)
	defer db.Close()
	_, err := db.Exec("CREATE TABLE steps (idx integer PRIMARY KEY, step_type integer, status integer, step_payload blob); CREATE TABLE gen_metadata (idx integer PRIMARY KEY, data blob); CREATE TABLE trajectory_metadata_blob (id text PRIMARY KEY, data blob)")
	must(t, err)
	for i, s := range steps {
		st := s.status
		if st == 0 {
			st = 3
		}
		_, err := db.Exec("INSERT INTO steps VALUES (?, ?, ?, ?)", i, s.typ, st, pbEnc(s.payload))
		must(t, err)
	}
	_, err = db.Exec("INSERT INTO gen_metadata VALUES (0, ?)", pbEnc([]pbf{{1, []pbf{{4, []pbf{{7, "bot-1"}}}, {19, "gemini-x"}}}}))
	must(t, err)
	_, err = db.Exec("INSERT INTO trajectory_metadata_blob VALUES ('main', ?)", pbEnc([]pbf{{7, shared.FileURL(filepath.Join(root, "work", "proj")).String()}}))
	must(t, err)
	return file
}

func userStep(n int, text string, status int) agyStep {
	return agyStep{14, status, []pbf{agyTS(n), {19, []pbf{{2, text}}}}}
}
func modelStep(n int, text string) agyStep {
	return agyStep{15, 0, []pbf{agyTS(n), {20, []pbf{{1, text}, {6, "bot-1"}}}}}
}

func agyTexts(t *testing.T, a *App, session string) []string {
	var out []string
	for _, e := range a.Hub.Path(binding(t, a.Hub, "antigravity", session).Leaf) {
		for _, p := range e.Parts {
			if p["type"] == "text" {
				out = append(out, core.S(p["text"]))
			}
		}
	}
	return out
}

func TestAntigravityDecode(t *testing.T) {
	s := newSandbox(t)
	steps := []agyStep{
		{14, 0, []pbf{agyTS(100), {19, []pbf{{2, "list files"}}}}},
		{15, 0, []pbf{agyTS(101), {20, []pbf{{3, "thinking about it"}, {6, "bot-1"}, {7, []pbf{{1, "call_1"}, {2, "run_command"}, {3, `{"CommandLine":"ls","Cwd":"/work/proj"}`}}}}}}},
		{132, 0, []pbf{{5, []pbf{{1, []pbf{{1, 102}}}, {4, []pbf{{1, "call_1"}, {2, "run_command"}}}}}, {140, []pbf{{2, []pbf{{1, "a.txt\nb.txt"}}}}}}},
		{15, 0, []pbf{agyTS(103), {20, []pbf{{1, "Two files."}, {6, "bot-1"}}}}},
		{14, 1, []pbf{agyTS(104), {19, []pbf{{2, "still typing"}}}}},
	}
	file := writeAgy(t, s.Agy, "conv-1", steps)
	a := s.app(enable("antigravity", "pi"))
	scan(t, a)
	b := binding(t, a.Hub, "antigravity", "conv-1")
	p := a.Hub.Path(b.Leaf)
	jsonEq(t, kindsActors(p), []string{"message:user", "message:assistant", "tool_result:tool", "message:assistant"})
	jsonEq(t, p[1].Attrs["model"], map[string]any{"provider": nil, "api": "antigravity", "id": "gemini-x"})
	var call core.Part
	for _, x := range p[1].Parts {
		if x["type"] == "tool_call" {
			call = x
		}
	}
	tool := core.Obj(call["tool"])
	ok(t, tool["op"] == "shell" && tool["command"] == "ls")
	eq(t, b.Meta["cwd"], any(filepath.Join(s.Agy, "work", "proj")))
	r := carry(t, a, engine.CarryOptions{Selector: "antigravity:conv-1", To: "pi"})
	eq(t, len(r.Plan.Losses), 0)
	// rescan without changes: nothing read; the source DB never written by us
	st, _ := os.Stat(file)
	eq(t, scan(t, a).Added, 0)
	st2, _ := os.Stat(file)
	ok(t, st.ModTime().Equal(st2.ModTime()))
}

func TestAntigravityLiveWAL(t *testing.T) {
	s := newSandbox(t)
	file := writeAgy(t, s.Agy, "live", []agyStep{userStep(1, "hello", 0), modelStep(2, "hi")})
	// agy keeps the DB open in WAL mode; new steps sit in the -wal file, not checkpointed
	w := openDB(t, file)
	defer w.Close()
	_, err := w.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0")
	must(t, err)
	a := s.app(enable("antigravity"))
	scan(t, a)
	jsonEq(t, agyTexts(t, a, "live"), []string{"hello", "hi"})
	_, err = w.Exec("INSERT INTO steps VALUES (?, ?, ?, ?)", 2, 14, 3, pbEnc(userStep(3, "more", 0).payload))
	must(t, err)
	_, err = w.Exec("INSERT INTO steps VALUES (?, ?, ?, ?)", 3, 15, 3, pbEnc(modelStep(4, "sure").payload))
	must(t, err)
	st, err := os.Stat(file + "-wal")
	ok(t, err == nil && st.Size() > 0, "rows live only in the WAL")
	stat := func() string {
		var parts []string
		for _, x := range []string{"", "-wal", "-shm"} {
			if st, err := os.Stat(file + x); err == nil {
				parts = append(parts, fmt.Sprintf("%d:%d", st.Size(), st.ModTime().UnixNano()))
			} else {
				parts = append(parts, "-")
			}
		}
		return strings.Join(parts, ",")
	}
	before := stat()
	r := scan(t, a)
	eq(t, len(r.Errors), 0)
	jsonEq(t, agyTexts(t, a, "live"), []string{"hello", "hi", "more", "sure"})
	eq(t, stat(), before, "db, -wal and -shm untouched by the read")
}

func TestAntigravityRunningEditedFailed(t *testing.T) {
	s := newSandbox(t)
	callStep := func(status int, out string) agyStep {
		p := []pbf{{5, []pbf{{1, []pbf{{1, 3}}}, {4, []pbf{{1, "c1"}, {2, "run_command"}}}}}}
		if out != "" {
			p = append(p, pbf{140, []pbf{{2, []pbf{{1, out}}}}})
		}
		return agyStep{132, status, p}
	}
	file := writeAgy(t, s.Agy, "conv", []agyStep{
		userStep(1, "run it", 0),
		{15, 0, []pbf{agyTS(2), {20, []pbf{{6, "bot-1"}, {7, []pbf{{1, "c1"}, {2, "run_command"}, {3, `{"CommandLine":"false"}`}}}}}}},
		callStep(1, ""), // running: no result yet
		modelStep(4, "after"),
	})
	db := openDB(t, file)
	defer db.Close()
	a := s.app(enable("antigravity"))
	scan(t, a)
	kinds := func() []string {
		var out []string
		for _, e := range a.Hub.Path(binding(t, a.Hub, "antigravity", "conv").Leaf) {
			out = append(out, e.Kind)
		}
		return out
	}
	jsonEq(t, kinds(), []string{"message", "message"}, "stops before the running tool step")
	// the tool step finishes, failed
	_, err := db.Exec("UPDATE steps SET status = 7, step_payload = ? WHERE idx = 2", pbEnc(callStep(7, "exit 1").payload))
	must(t, err)
	scan(t, a)
	jsonEq(t, kinds(), []string{"message", "message", "tool_result", "message"})
	p := a.Hub.Path(binding(t, a.Hub, "antigravity", "conv").Leaf)
	res := p[2].Parts[0]
	ok(t, res["type"] == "tool_result" && res["outcome"] == "error" && res["callId"] == "c1")
	// agy rewrites an earlier step in place (the user edited the prompt): the path follows the edit
	n := a.Hub.EventCount()
	_, err = db.Exec("UPDATE steps SET step_payload = ? WHERE idx = 0", pbEnc(userStep(1, "run it, please", 0).payload))
	must(t, err)
	scan(t, a)
	texts := agyTexts(t, a, "conv")
	eq(t, texts[0], "run it, please")
	c := 0
	for _, x := range texts {
		if strings.HasPrefix(x, "run it") {
			c++
		}
	}
	eq(t, c, 1, "the old text is superseded, not duplicated")
	n2 := a.Hub.EventCount()
	ok(t, n2 > n)
	for i := 0; i < 3; i++ {
		eq(t, scan(t, a).Added, 0)
	}
	eq(t, a.Hub.EventCount(), n2)
}

func TestAntigravityDamagedDatabases(t *testing.T) {
	s := newSandbox(t)
	writeAgy(t, s.Agy, "good", []agyStep{userStep(1, "fine", 0), modelStep(2, "ok")})
	conv := filepath.Join(s.Agy, "conversations")
	must(t, os.WriteFile(filepath.Join(conv, "empty.db"), nil, 0o644))
	must(t, os.WriteFile(filepath.Join(conv, "junk.db"), []byte("not a sqlite file at all"), 0o644))
	other := openDB(t, filepath.Join(conv, "foreign.db"))
	_, _ = other.Exec("CREATE TABLE t (x)")
	other.Close()
	must(t, os.WriteFile(filepath.Join(conv, "notes.txt"), []byte("ignored"), 0o644))
	writeAgy(t, s.Agy, "odd", []agyStep{userStep(1, "start", 0)})
	odd := openDB(t, filepath.Join(conv, "odd.db"))
	_, _ = odd.Exec("INSERT INTO steps VALUES (?, ?, ?, ?)", 1, 15, 3, []byte{0xff, 0xff, 0xff, 0x0f, 0x12})
	_, _ = odd.Exec("INSERT INTO steps VALUES (?, ?, ?, ?)", 2, 14, 3, nil)
	_, _ = odd.Exec("INSERT INTO steps VALUES (?, ?, ?, ?)", 3, 999, 3, pbEnc([]pbf{{1, "x"}}))
	_, _ = odd.Exec("INSERT INTO steps VALUES (?, ?, ?, ?)", 4, 15, 3, pbEnc(modelStep(5, "still here").payload))
	odd.Close()
	a := s.app(enable("antigravity"))
	r := scan(t, a)
	jsonEq(t, agyTexts(t, a, "good"), []string{"fine", "ok"})
	bad := map[string]bool{}
	for _, e := range r.Errors {
		bad[e.Session] = true
	}
	ok(t, !bad["good"])
	ok(t, a.Hub.Binding("antigravity", "notes") == nil, "non-.db files ignored")
	for _, x := range []string{"empty", "junk", "foreign"} {
		b := a.Hub.Binding("antigravity", x)
		ok(t, bad[x] || b == nil || b.Leaf == "", x, ": reported, not ingested as a conversation")
	}
	if !bad["odd"] {
		ts := strings.Join(agyTexts(t, a, "odd"), "|")
		ok(t, strings.Contains(ts, "start") && strings.Contains(ts, "still here"))
	}
}

func TestAntigravityNewSession(t *testing.T) {
	s := newSandbox(t)
	writeAgy(t, s.Agy, "conv", []agyStep{userStep(1, "q", 0), modelStep(2, "a")})
	a := s.app(enable("antigravity", "pi"))
	scan(t, a)
	copied := carry(t, a, engine.CarryOptions{Selector: "antigravity:conv", To: "antigravity"})
	ok(t, copied.Applied)
	scan(t, a)
	jsonEq(t, agyTexts(t, a, copied.Plan.Target.Session), []string{"q", "a"})
	r := carry(t, a, engine.CarryOptions{Selector: "antigravity:conv", To: "pi"})
	ok(t, r.Applied)
}
