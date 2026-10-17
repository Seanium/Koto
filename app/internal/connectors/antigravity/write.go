package antigravity

// Native import format verified against agy 1.3.4's embedded protobuf descriptors and a fresh
// CLI-created session. Only new sessions are written. A finished conversation is a SQLite DB
// PLUS an entry in conversation_summaries.db; without the latter --conversation ignores it.

import (
	"context"
	"database/sql"
	"encoding/binary"

	"encoding/json"
	"errors"
	"fmt"
	"koto/internal/connectors/shared"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/planner"
)

const sessionSchema = `
PRAGMA user_version = 1;
CREATE TABLE trajectory_meta (trajectory_id TEXT PRIMARY KEY, cascade_id TEXT, trajectory_type INTEGER, source INTEGER);
CREATE TABLE steps (idx INTEGER PRIMARY KEY, step_type INTEGER NOT NULL DEFAULT 0, status INTEGER NOT NULL DEFAULT 0, has_subtrajectory NUMERIC NOT NULL DEFAULT false, metadata BLOB, error_details BLOB, permissions BLOB, task_details BLOB, render_info BLOB, step_payload BLOB, step_format INTEGER NOT NULL DEFAULT 0);
CREATE INDEX idx_steps_status ON steps(status);
CREATE INDEX idx_steps_step_type ON steps(step_type);
CREATE TABLE gen_metadata (idx INTEGER PRIMARY KEY, data BLOB, size INTEGER NOT NULL DEFAULT 0);
CREATE TABLE executor_metadata (idx INTEGER PRIMARY KEY, data BLOB);
CREATE TABLE parent_references (idx INTEGER PRIMARY KEY, data BLOB);
CREATE TABLE trajectory_metadata_blob (id TEXT PRIMARY KEY DEFAULT 'main', data BLOB);
CREATE TABLE battle_mode_infos (idx INTEGER PRIMARY KEY, data BLOB);
`
const summarySchema = `CREATE TABLE IF NOT EXISTS conversation_summaries (
 conversation_id TEXT PRIMARY KEY, title TEXT NOT NULL DEFAULT '', preview TEXT NOT NULL DEFAULT '', step_count INTEGER NOT NULL DEFAULT 0,
 last_modified_time DATETIME NOT NULL, workspace_uris TEXT NOT NULL, status TEXT NOT NULL DEFAULT '', source TEXT NOT NULL DEFAULT '',
 project_id TEXT NOT NULL DEFAULT '', agent_name TEXT NOT NULL DEFAULT '', parent_conversation_id TEXT NOT NULL DEFAULT '', nesting_depth INTEGER NOT NULL DEFAULT 0,
 battle_id TEXT NOT NULL DEFAULT '', winning_conversation_id TEXT NOT NULL DEFAULT '', not_fully_idle NUMERIC NOT NULL DEFAULT false, killed NUMERIC NOT NULL DEFAULT false,
 last_user_input_time DATETIME NOT NULL, last_user_input_step_index INTEGER NOT NULL DEFAULT -1, app_data_dir TEXT NOT NULL DEFAULT '', raw_summary BLOB, group_id TEXT NOT NULL DEFAULT '');`

type nativeStep struct {
	Type     int    `json:"type"`
	Metadata []byte `json:"metadata"`
	Payload  []byte `json:"payload"`
}
type nativeImport struct {
	Session       string       `json:"session"`
	Trajectory    string       `json:"trajectory"`
	Cwd           string       `json:"cwd"`
	Title         string       `json:"title"`
	Created       int64        `json:"created"`
	Updated       int64        `json:"updated"`
	LastUser      int64        `json:"lastUser"`
	LastUserIndex int          `json:"lastUserIndex"`
	Metadata      []byte       `json:"metadata"`
	Summary       []byte       `json:"summary"`
	Steps         []nativeStep `json:"steps"`
}

// A deliberately small wire encoder: the importer needs only documented-by-descriptor strings,
// timestamps and finished text steps, not the full private runtime schema or another dependency.
func pbInt(n int, v uint64) []byte {
	return binary.AppendUvarint(binary.AppendUvarint(nil, uint64(n<<3)), v)
}
func pbBytes(n int, b []byte) []byte {
	return append(binary.AppendUvarint(binary.AppendUvarint(nil, uint64(n<<3|2)), uint64(len(b))), b...)
}
func pbString(n int, s string) []byte { return pbBytes(n, []byte(s)) }
func pbJoin(parts ...[]byte) []byte {
	var b []byte
	for _, p := range parts {
		b = append(b, p...)
	}
	return b
}
func pbTime(ms int64) []byte {
	if ms < 0 {
		ms = 0
	}
	return pbJoin(pbInt(1, uint64(ms/1000)), pbInt(2, uint64(ms%1000)*1_000_000))
}
func fileURI(path string) string {
	return shared.FileURL(path).String()
}
func nativeUUID(key string) string            { return core.UUID5(key) }
func (c *agy) NewSessionID(key string) string { return nativeUUID("antigravity\x00" + key) }
func (c *agy) ResumeCommand() string          { return "agy --conversation {id}" }

func (c *agy) Plan(in engine.PlanInput) (*engine.DeliveryPlan, error) {
	t := in.Target
	in, attachments := shared.PrepareAttachments(in, filepath.Join(c.conv, t.Session+".assets"), func(p core.Part, role string) bool { return role != "assistant" })
	proj := in.Projection
	p := &engine.DeliveryPlan{Sink: c.id, Harness: "antigravity", Thread: in.Thread, Head: in.Head, Level: in.Level, Mode: t.Mode, Target: t, Records: []engine.PlanRecord{}, Losses: append([]core.Loss{}, proj.Losses...), Notes: append([]string{}, proj.Notes...), Extra: core.JsonObject{"attachments": attachments}}
	if t.Mode != "create" {
		p.Mode = "reject"
		p.Notes = append(p.Notes, "only new-session copies are supported")
		p.ID = core.SHA256Hex(c.id + t.Session + "reject")
		return p, nil
	}
	cwd := t.Cwd
	if cwd == "" {
		cwd = proj.Hints.Cwd
	}
	if cwd == "" {
		cwd, _ = os.UserHomeDir()
	}
	var err error
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	ni := nativeImport{Session: t.Session, Trajectory: nativeUUID("agy-trajectory\x00" + t.Session), Cwd: cwd, Title: t.Title, LastUserIndex: -1, Steps: []nativeStep{}}
	loss := func(it planner.Item, field, kind string) {
		p.Losses = append(p.Losses, core.Loss{Sink: c.id, Event: it.Event.ID, Field: field, Kind: kind, Note: "Antigravity imports replay history as text"})
	}
	for _, it := range proj.Items {
		var texts []string
		var assets []byte
		for _, part := range it.Parts {
			switch core.S(part["type"]) {
			case "text":
				texts = append(texts, core.S(part["text"]))
			case "reasoning":
				if tx := core.S(part["text"]); tx != "" {
					texts = append(texts, tx)
					loss(it, "reasoning", "textualized")
				} else {
					loss(it, "reasoning", "dropped")
				}
			case "tool_call":
				texts = append(texts, core.TextualizeCall(part))
				loss(it, "tool_call", "textualized")
			case "tool_result":
				texts = append(texts, planner.TextualizeResult(part))
				loss(it, "tool_result", "textualized")
			case "image":
				data, _ := in.Hub.Blob(core.S(core.Obj(part["blob"])["sha256"]))
				assets = append(assets, pbBytes(9, pbJoin(pbString(1, core.S(part["mediaType"])), pbBytes(2, data), pbString(9, core.S(part["name"])), pbString(5, core.S(part["uri"]))))...)
			case "file":
				data, _ := in.Hub.Blob(core.S(core.Obj(part["blob"])["sha256"]))
				assets = append(assets, pbBytes(9, pbJoin(pbString(1, core.S(part["mediaType"])), pbBytes(2, data), pbString(9, core.S(part["name"])), pbString(5, core.S(part["uri"]))))...)
			default:
				loss(it, core.S(part["type"]), "dropped")
			}
		}
		text := strings.Join(texts, "\n")
		if strings.TrimSpace(text) == "" && len(assets) == 0 {
			continue
		}
		ms := int64(it.Event.TimeMs())
		if ms <= 0 {
			ms = ni.Updated + 1
		}
		if ms < ni.Updated {
			ms = ni.Updated
		}
		if len(ni.Steps) == 0 {
			ni.Created = ms
		}
		ni.Updated = ms
		typ, source, field := 15, uint64(2), 20
		var content []byte
		if it.Role == "user" || it.Role == "tool" {
			typ, source, field = 14, 4, 19
			content = pbJoin(pbString(2, text), pbBytes(3, pbString(1, text)), assets)
			ni.LastUser = ms
			ni.LastUserIndex = len(ni.Steps)
			if ni.Title == "" {
				r := []rune(text)
				if len(r) > 80 {
					r = r[:80]
				}
				ni.Title = string(r)
			}
		} else {
			content = pbJoin(pbString(1, text), pbString(6, core.UUID5(t.Session+"\x00"+it.Key)))
		}
		ts := pbTime(ms)
		meta := pbJoin(pbBytes(1, ts), pbInt(3, source), pbBytes(6, ts), pbBytes(8, ts))
		payload := pbJoin(pbInt(1, uint64(typ)), pbInt(4, 3), pbBytes(5, meta), pbBytes(field, content))
		idx := len(ni.Steps)
		ni.Steps = append(ni.Steps, nativeStep{typ, meta, payload})
		p.Records = append(p.Records, engine.PlanRecord{NativeID: "step" + strconv.Itoa(idx), EventID: it.Event.ID, SHA256: stepHash(typ, payload)})
	}
	if len(ni.Steps) == 0 {
		p.Mode = "reject"
		p.Notes = append(p.Notes, "no completed messages to import")
	}
	if ni.Title == "" {
		ni.Title = "koto conversation"
	}
	uri := fileURI(cwd)
	ws := pbString(1, uri)
	ni.Metadata = pbJoin(pbBytes(1, ws), pbBytes(2, pbTime(ni.Created)), pbString(6, t.Session), pbString(7, uri), pbString(18, "default-cli-project"))
	ni.Summary = pbJoin(pbString(1, ni.Title), pbInt(2, uint64(len(ni.Steps))), pbBytes(3, pbTime(ni.Updated)), pbString(4, ni.Trajectory), pbInt(5, 1), pbBytes(7, pbTime(ni.Created)), pbBytes(9, ws), pbBytes(10, pbTime(ni.LastUser)), pbBytes(15, pbString(1, ni.Title)), pbBytes(17, ni.Metadata), pbInt(20, 17), pbInt(22, 4))
	p.Content = core.ToJSON(ni)
	p.ID = core.SHA256Hex(c.id + "\x00" + t.Session + "\x00" + p.Content)
	p.Target.Path = filepath.Join(c.conv, t.Session+".db")
	p.Notes = append(p.Notes, "Antigravity: creates a new native session; tool history is text; existing sessions are never appended to")
	return p, nil
}
func stepHash(typ int, payload []byte) string {
	return core.SHA256Hex(append([]byte(strconv.Itoa(typ)+":"), payload...))
}

var sessionID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func openNative(file string, mode string) (*sql.DB, error) {
	u := shared.FileURL(file)
	q := u.Query()
	q.Set("mode", mode)
	q.Set("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err == nil {
		db.SetMaxOpenConns(1)
	}
	return db, err
}

func (c *agy) Apply(ctx context.Context, p *engine.DeliveryPlan) (core.JsonObject, error) {
	if p.Mode != "create" {
		return nil, fmt.Errorf("Antigravity only creates new sessions")
	}
	if err := shared.ApplyAttachments(p); err != nil {
		return nil, err
	}
	var ni nativeImport
	if err := json.Unmarshal([]byte(p.Content), &ni); err != nil {
		return nil, err
	}
	// A copied session starts now even when its imported message history is old. The CLI uses
	// session creation time for its print deadline and elapsed-time bookkeeping.
	ni.Created = time.Now().UnixMilli()
	ni.Updated = ni.Created
	ni.LastUser = ni.Created
	uri := fileURI(ni.Cwd)
	ws := pbString(1, uri)
	ni.Metadata = pbJoin(pbBytes(1, ws), pbBytes(2, pbTime(ni.Created)), pbString(6, ni.Session), pbString(7, uri), pbString(18, "default-cli-project"))
	ni.Summary = pbJoin(pbString(1, ni.Title), pbInt(2, uint64(len(ni.Steps))), pbBytes(3, pbTime(ni.Updated)), pbString(4, ni.Trajectory), pbInt(5, 1), pbBytes(7, pbTime(ni.Created)), pbBytes(9, ws), pbBytes(10, pbTime(ni.LastUser)), pbBytes(15, pbString(1, ni.Title)), pbBytes(17, ni.Metadata), pbInt(20, 17), pbInt(22, 4))
	if !sessionID.MatchString(ni.Session) || ni.Session != p.Target.Session || len(ni.Steps) == 0 {
		return nil, fmt.Errorf("invalid Antigravity import plan")
	}
	file := filepath.Join(c.conv, ni.Session+".db")
	if filepath.Clean(p.Target.Path) != filepath.Clean(file) {
		return nil, fmt.Errorf("Antigravity target is outside its conversation directory")
	}
	if err := os.MkdirAll(c.conv, 0o700); err != nil {
		return nil, err
	}
	// Build in a private temporary file; publish using a no-replace link. Never open a historical
	// conversation for writing, even if its ID collides. Native identity and exact message payloads make an immediate retry idempotent.
	if _, err := os.Lstat(file); errors.Is(err, os.ErrNotExist) {
		if err := c.publish(ctx, file, p, ni); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if err := verifyImport(ctx, file, p, ni); err != nil {
		return nil, err
	}
	if err := c.indexImport(ctx, ni); err != nil {
		return nil, fmt.Errorf("register Antigravity session: %w", err)
	}
	return core.JsonObject{"session": ni.Session, "path": file, "lastNativeId": "step" + strconv.Itoa(len(ni.Steps)-1), "steps": len(ni.Steps)}, nil
}
func (c *agy) publish(ctx context.Context, file string, p *engine.DeliveryPlan, ni nativeImport) error {
	f, err := os.CreateTemp(c.conv, ".koto-import-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_ = f.Close()
	defer os.Remove(tmp)
	db, err := openNative(tmp, "rw")
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, sessionSchema); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO trajectory_meta VALUES (?,?,4,17)", ni.Trajectory, ni.Session); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO trajectory_metadata_blob VALUES ('main',?)", ni.Metadata); err != nil {
		return err
	}
	for i, s := range ni.Steps {
		if _, err = tx.ExecContext(ctx, "INSERT INTO steps(idx,step_type,status,metadata,step_payload) VALUES (?,?,3,?,?)", i, s.Type, s.Metadata, s.Payload); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if err = db.Close(); err != nil {
		return err
	}
	f, err = os.OpenFile(tmp, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = f.Sync()
	_ = f.Close()
	if err != nil {
		return err
	}
	return os.Link(tmp, file)
}
func verifyImport(ctx context.Context, file string, p *engine.DeliveryPlan, ni nativeImport) error {
	conflict := func() error {
		return &engine.TargetChangedError{Msg: "Antigravity target already exists or was changed; create a new session"}
	}
	st, err := os.Lstat(file)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return conflict()
	}
	// Snapshot even our own file: agy may have opened it after a successful write whose receipt
	// was lost. Never allow a read connection to create WAL/SHM beside a user's native session.
	dir, err := snapshot(file)
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	db, err := openNative(filepath.Join(dir, "c.db"), "ro")
	if err != nil {
		return err
	}
	defer db.Close()
	var trajectory, session string
	if db.QueryRowContext(ctx, "SELECT trajectory_id,cascade_id FROM trajectory_meta").Scan(&trajectory, &session) != nil || trajectory != ni.Trajectory || session != ni.Session {
		return conflict()
	}
	for i, s := range ni.Steps {
		var b []byte
		var typ, status int
		if db.QueryRowContext(ctx, "SELECT step_type,status,step_payload FROM steps WHERE idx=?", i).Scan(&typ, &status, &b) != nil || status != 3 || stepHash(typ, b) != stepHash(s.Type, s.Payload) {
			return conflict()
		}
	}
	return nil
}
func (c *agy) indexImport(ctx context.Context, ni nativeImport) error {
	file := filepath.Join(c.root, "conversation_summaries.db")
	db, err := openNative(file, "rwc")
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, summarySchema); err != nil {
		return err
	}
	// Conflict means somebody already registered this ID. Accept only our trajectory, and do
	// not reset a title/preview/count that native Antigravity has updated since the first import.
	var raw []byte
	err = tx.QueryRowContext(ctx, "SELECT raw_summary FROM conversation_summaries WHERE conversation_id=?", ni.Session).Scan(&raw)
	if err == nil {
		id, _ := core.NewPb(raw).Str(4)
		if id != ni.Trajectory {
			return &engine.TargetChangedError{Msg: "Antigravity index ID collision"}
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	stamp := func(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano) }
	_, err = tx.ExecContext(ctx, `INSERT INTO conversation_summaries(conversation_id,title,preview,step_count,last_modified_time,workspace_uris,status,project_id,last_user_input_time,last_user_input_step_index,app_data_dir,raw_summary) VALUES (?,?,?,?,?,?,'CASCADE_RUN_STATUS_IDLE','default-cli-project',?,?,?,?)`, ni.Session, ni.Title, ni.Title, len(ni.Steps), stamp(ni.Updated), core.ToJSON([]string{fileURI(ni.Cwd)}), stamp(ni.LastUser), ni.LastUserIndex, filepath.Base(c.root), ni.Summary)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Titles live beside the conversations; reading a snapshot avoids touching native WAL files.
func (c *agy) readTitles(ctx context.Context) map[string]string {
	out := map[string]string{}
	file := filepath.Join(c.root, "conversation_summaries.db")
	if _, err := os.Stat(file); err != nil {
		return out
	}
	dir, err := snapshot(file)
	if err != nil {
		return out
	}
	defer os.RemoveAll(dir)
	db, err := openNative(filepath.Join(dir, "c.db"), "ro")
	if err != nil {
		return out
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SELECT conversation_id,title FROM conversation_summaries")
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, title string
		if rows.Scan(&id, &title) == nil {
			out[id] = title
		}
	}
	return out
}
