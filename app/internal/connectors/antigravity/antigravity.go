// Package antigravity reads Antigravity CLI (agy) conversations and creates new native sessions.
//
// Storage root: ~/.gemini/antigravity-cli (the desktop app uses ~/.gemini/antigravity with the same
// layout → another instance). Each conversation is one SQLite file conversations/<cascade-id>.db;
// the steps table holds one protobuf step_payload per step. The .proto is not public: the field
// numbers are checked against agy 1.3.4's embedded descriptors and fresh native sessions.
//
//	step_type 14  user input       19.2 = text
//	step_type 15  model turn       20.1 text, 20.3 thinking, 20.6 generation id, 20.7[] calls {1 id, 2 name, 3 args}
//	step_type 132 tool execution   5.4 = call {1 id, 2 name, 3 args}; 140.2.1 = output text
//	step_type 101 system message   114.1 = text
//	every step                     5.1 = timestamp {1 secs, 2 nanos}
//	gen_metadata.data              1.4.7 = generation id, 1.19 = model name
//
// Steps can be updated in place (a tool step gains its result later), so each read re-hashes all
// steps: unchanged ones are no-ops, changed ones become superseding events. The DB is copied (with
// -wal/-shm) before reading. The sink in write.go only creates new sessions and never appends.
package antigravity

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"

	_ "modernc.org/sqlite"
)

const Version = "antigravity-connector@0.2.0 (agy 1.3.4)"

// ToCanonical maps agy tools.
func ToCanonical(name string, args any) map[string]any {
	a := core.Obj(args)
	or := func(k1, k2 string) string {
		if s, ok := a[k1].(string); ok {
			return s
		}
		return core.S(a[k2])
	}
	switch name {
	case "run_command":
		t := map[string]any{"op": "shell", "command": core.S(a["CommandLine"])}
		if c := core.S(a["Cwd"]); c != "" {
			t["cwd"] = c
		}
		return t
	case "view_file":
		return map[string]any{"op": "read", "path": or("AbsolutePath", "File")}
	case "write_to_file":
		t := map[string]any{"op": "write", "path": core.S(a["TargetFile"])}
		if c, ok := a["CodeContent"].(string); ok {
			t["content"] = c
		}
		return t
	case "search_web":
		return map[string]any{"op": "web", "query": or("query", "Query")}
	case "read_url_content":
		return map[string]any{"op": "web", "url": or("Url", "url")}
	}
	return map[string]any{"op": "raw"}
}

// Vocabulary also checks whether a canonical form kept every source argument.
type Vocabulary struct{}

func (Vocabulary) ToCanonical(name string, args any) map[string]any { return ToCanonical(name, args) }
func (Vocabulary) FromCanonical(t map[string]any) (core.NativeToolCall, bool) {
	switch core.S(t["op"]) {
	case "shell":
		a := map[string]any{"CommandLine": core.S(t["command"])}
		if c := core.S(t["cwd"]); c != "" {
			a["Cwd"] = c
		}
		return core.NativeToolCall{Name: "run_command", Args: a}, true
	case "read":
		if _, ok := t["range"]; ok {
			return core.NativeToolCall{}, false
		}
		return core.NativeToolCall{Name: "view_file", Args: map[string]any{"AbsolutePath": core.S(t["path"])}}, true
	case "write":
		c, ok := t["content"].(string)
		if !ok {
			return core.NativeToolCall{}, false
		}
		return core.NativeToolCall{Name: "write_to_file", Args: map[string]any{"TargetFile": core.S(t["path"]), "CodeContent": c}}, true
	case "web":
		if u := core.S(t["url"]); u != "" {
			return core.NativeToolCall{Name: "read_url_content", Args: map[string]any{"Url": u}}, true
		}
		return core.NativeToolCall{Name: "search_web", Args: map[string]any{"query": core.S(t["query"])}}, true
	}
	return core.NativeToolCall{}, false
}

var Capabilities = core.Capabilities{Read: core.ReadCaps{Images: true, Reasoning: "plain"},
	Write:        &core.WriteCaps{Mode: "native-file", Images: true, Reasoning: "none", Tools: "text"},
	WriteProfile: "agy-media@1", FormatVersions: []string{"agy-steps (1.3.4)"}}

func parseJSON(s string, ok bool) any {
	if !ok {
		return map[string]any{}
	}
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return s
	}
	return v
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func snapshot(db string) (string, error) {
	dir, err := os.MkdirTemp("", "koto-agy-")
	if err != nil {
		return "", err
	}
	for _, ext := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(db + ext); err == nil {
			if err := copyFile(db+ext, filepath.Join(dir, "c.db"+ext)); err != nil {
				os.RemoveAll(dir)
				return "", err
			}
		}
	}
	return dir, nil
}

// Options for the connector.
type Options struct {
	Root string // default ~/.gemini/antigravity-cli
	ID   string
}

type agy struct{ id, root, conv string }

// New makes an Antigravity connector.
func New(o Options) *engine.Connector {
	if o.ID == "" {
		o.ID = "antigravity"
	}
	c := &agy{id: o.ID, root: o.Root, conv: filepath.Join(o.Root, "conversations")}
	return &engine.Connector{ID: c.id, Root: c.root, Harness: "antigravity", Version: Version, Capabilities: Capabilities, Vocabulary: Vocabulary{}, Source: c, Sink: c}
}

func (c *agy) Discover(ctx context.Context) ([]core.NativeRef, error) {
	ents, err := os.ReadDir(c.conv)
	if err != nil {
		return nil, nil
	}
	var out []core.NativeRef
	titles := c.readTitles(ctx)
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		p := filepath.Join(c.conv, e.Name())
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		size, mt := st.Size(), engine.MtimeMs(st)
		// size/mtime include the WAL so live writes are noticed
		if w, err := os.Stat(p + "-wal"); err == nil {
			size += w.Size()
			mt = math.Max(mt, engine.MtimeMs(w))
		}
		id := strings.TrimSuffix(e.Name(), ".db")
		out = append(out, core.NativeRef{Harness: "antigravity", Connector: c.id, Session: id, Path: p, Size: size, MtimeMs: mt, Meta: core.JsonObject{"title": titles[id]}})
	}
	return out, nil
}

func (c *agy) WatchRoots() []string { return []string{c.conv} }

func (c *agy) Tail(ctx context.Context, ref core.NativeRef, from *core.SourceOffset, yield func(core.SourceBatch) error) error {
	witness, stable, err := engine.SourceWitness(ref)
	if err != nil {
		return err
	}
	dir, err := snapshot(ref.Path)
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "c.db")+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	models := map[string]string{}
	if rows, err := db.Query("SELECT data FROM gen_metadata"); err == nil {
		for rows.Next() {
			var b []byte
			if rows.Scan(&b) == nil {
				g := core.NewPb(b)
				gid, ok1 := g.Msg(1, 4).Str(7)
				model, ok2 := g.Msg(1).Str(19)
				if ok1 && ok2 && gid != "" && model != "" {
					models[gid] = model
				}
			}
		}
		rows.Close()
	}
	var cwd any = nil
	var m []byte
	if db.QueryRow("SELECT data FROM trajectory_metadata_blob WHERE id = 'main'").Scan(&m) == nil {
		if uri, ok := core.NewPb(m).Str(7); ok && strings.HasPrefix(uri, "file://") {
			if u, err := url.Parse(uri); err == nil {
				if d, ok := shared.FileURLPath(u); ok {
					cwd = d
				}
			}
		}
	}
	type step struct {
		idx, typ, status int
		payload          []byte
	}
	var all []step
	rows, err := db.Query("SELECT idx, step_type, status, step_payload FROM steps ORDER BY idx")
	if err != nil {
		return err
	}
	for rows.Next() {
		var s step
		if err := rows.Scan(&s.idx, &s.typ, &s.status, &s.payload); err != nil {
			rows.Close()
			return err
		}
		all = append(all, s)
	}
	rows.Close()
	// Only finished steps: descriptor values are 3 done, 6 cancelled and 7 error.
	steps := all
	for i, s := range all {
		if s.status != 3 && s.status != 6 && s.status != 7 {
			steps = all[:i]
			break
		}
	}
	var records []core.BatchRecord
	var drafts []core.DraftEvent
	var blobs [][]byte
	asset := func(data []byte) core.JsonObject {
		blobs = append(blobs, data)
		return core.JsonObject{"sha256": core.SHA256Hex(data), "bytes": len(data)}
	}
	prefix := core.ZeroPrefix
	for index, r := range steps {
		raw := r.payload
		sha := core.SHA256Hex(append([]byte(strconv.Itoa(r.typ)+":"), raw...))
		prefix = core.ChainPrefix(prefix, sha)
		nativeID := "step" + strconv.Itoa(r.idx)
		records = append(records, core.BatchRecord{NativeID: nativeID, SHA256: sha, Bytes: len(raw), Index: index})
		p := core.NewPb(raw)
		ts := p.Msg(5, 1)
		var t *float64
		if secs, ok := ts.Int(1); ok && secs != 0 {
			nanos, _ := ts.Int(2)
			f := float64(secs)*1000 + math.Floor(float64(nanos)/1e6)
			t = &f
		}
		attrs := func(extra core.JsonObject) core.JsonObject {
			a := core.JsonObject{}
			if cwd != nil {
				a["cwd"] = cwd
			}
			for k, v := range extra {
				a[k] = v
			}
			return a
		}
		textPart := func(s string) []core.Part {
			if s == "" {
				return []core.Part{}
			}
			return shared.DecodeAttachmentText(s, strings.TrimSuffix(ref.Path, ".db")+".assets", asset)
		}
		switch r.typ {
		case 14:
			input := p.Msg(19)
			text, _ := input.Str(2)
			parts := textPart(text)
			for _, im := range input.Msgs(5) {
				data, _ := im.Str(1)
				mime, _ := im.Str(2)
				name, _ := im.Str(3)
				uri, _ := im.Str(4)
				q := core.Part{"type": "image", "mediaType": mime, "name": name}
				if data != "" {
					q["blob"] = asset(core.Base64(data))
				} else if uri != "" {
					q["uri"] = uri
				}
				parts = append(parts, q)
			}
			for _, m := range input.Msgs(9) {
				mime, _ := m.Str(1)
				name, _ := m.Str(9)
				uri, _ := m.Str(5)
				kind := "file"
				if strings.HasPrefix(mime, "image/") {
					kind = "image"
				}
				q := core.Part{"type": kind, "mediaType": mime, "name": name}
				if data := m.All(2); len(data) > 0 {
					q["blob"] = asset(data[0].Bytes)
				} else if uri != "" {
					q["uri"] = uri
				}
				parts = append(parts, q)
			}
			drafts = append(drafts, core.DraftEvent{NativeID: nativeID, Time: t, Attrs: attrs(nil), Kind: "message", Actor: "user", Parts: parts})
		case 15:
			mm := p.Msg(20)
			parts := []core.Part{}
			if th, _ := mm.Str(3); th != "" {
				parts = append(parts, core.Part{"type": "reasoning", "text": th})
			}
			if tx, _ := mm.Str(1); tx != "" {
				parts = append(parts, textPart(tx)...)
			}
			for _, cl := range mm.Msgs(7) {
				name, _ := cl.Str(2)
				as, ok := cl.Str(3)
				args := parseJSON(as, ok)
				id, _ := cl.Str(1)
				parts = append(parts, core.Part{"type": "tool_call", "callId": id, "rawName": name, "args": args, "tool": ToCanonical(name, args)})
			}
			var model any = nil
			if gen, _ := mm.Str(6); gen != "" {
				if md, ok := models[gen]; ok {
					model = md
				}
			}
			drafts = append(drafts, core.DraftEvent{NativeID: nativeID, Time: t, Kind: "message", Actor: "assistant", Parts: parts, Attrs: attrs(core.JsonObject{"model": map[string]any{"provider": nil, "api": "antigravity", "id": model}})})
		case 132:
			call := p.Msg(5, 4)
			out, ok := p.Msg(140, 2).Str(1)
			if !ok {
				out = p.Msg(140).LongestString(6)
			}
			id, _ := call.Str(1)
			outcome := "success"
			if r.status == 7 {
				outcome = "error"
			} else if r.status == 6 {
				outcome = "cancelled"
			}
			var tool any = nil
			if n, ok := call.Str(2); ok {
				tool = n
			}
			drafts = append(drafts, core.DraftEvent{NativeID: nativeID, Time: t, Kind: "tool_result", Actor: "tool",
				Parts: []core.Part{{"type": "tool_result", "callId": id, "outcome": outcome, "parts": core.PartsJSON(textPart(out))}}, Attrs: attrs(core.JsonObject{"toolName": tool})})
		case 101:
			text, _ := p.Msg(114).Str(1)
			drafts = append(drafts, core.DraftEvent{NativeID: nativeID, Time: t, Attrs: attrs(nil), Kind: "context", Actor: "system", Parts: textPart(text)})
		}
	}
	changed := from == nil || from.PrefixHash != prefix
	if !changed {
		drafts, records = nil, nil
	}
	last := int64(-1)
	if n := len(steps); n > 0 {
		last = int64(steps[n-1].idx)
	}
	return yield(core.SourceBatch{Ref: ref, Drafts: drafts, Records: records, Blobs: blobs,
		Offset: core.SourceOffset{Position: core.BytePosition{Kind: "seq", Seq: last}, Records: len(steps), PrefixHash: prefix, FormatVersion: "agy-steps", State: core.JsonObject{"fileSize": float64(ref.Size), "mtimeMs": ref.MtimeMs}, Witness: witness, WitnessStable: stable},
		Meta:   core.JsonObject{"cwd": cwd, "frontend": "antigravity-cli"}})
}
