package opencode

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/planner"
)

type delivery struct {
	Created  int64           `json:"created"`
	Messages []nativeMessage `json:"messages"`
	Model    core.JsonObject `json:"model"`
}

func orderedID(prefix, seed string, n int) string {
	return fmt.Sprintf("%s_%012x%s", prefix, n, core.SHA256Hex(seed)[:14])
}

func (c *connector) Plan(in engine.PlanInput) (*engine.DeliveryPlan, error) {
	if in.Target.Mode != "create" {
		return nil, fmt.Errorf("OpenCode copies must create a new session")
	}
	if !regexp.MustCompile("^ses_[A-Za-z0-9]{26}$").MatchString(in.Target.Session) {
		return nil, fmt.Errorf("invalid OpenCode session ID")
	}
	target := in.Target
	if target.Cwd == "" {
		target.Cwd = in.Projection.Hints.Cwd
	}
	if target.Cwd == "" {
		return nil, fmt.Errorf("OpenCode requires a project directory")
	}
	var err error
	target.Cwd, err = filepath.Abs(target.Cwd)
	if err != nil {
		return nil, err
	}
	target.Path = c.db
	in, attachments := shared.PrepareAttachments(in, c.assetDir(target.Session), func(p core.Part, role string) bool {
		mime := core.S(p["mediaType"])
		return mime == "image/png" || mime == "image/jpeg" || mime == "image/gif" || mime == "image/webp" || mime == "application/pdf" || mime == "text/plain" && role == "user" || core.S(p["uri"]) != ""
	})
	plan := &engine.DeliveryPlan{Sink: c.id, Harness: "opencode", Thread: in.Thread, Head: in.Head, Level: in.Level, Mode: "create", Target: target, Losses: append([]core.Loss{}, in.Projection.Losses...), Notes: append([]string{}, in.Projection.Notes...), Extra: core.JsonObject{"attachments": attachments}}
	model, err := c.targetModel(target.Cwd)
	if err != nil {
		return nil, err
	}
	d := delivery{Created: time.Now().UnixMilli(), Model: model}
	parent := ""
	ordinal := 0
	calls := map[string]core.JsonObject{}
	eventIDs := map[string]string{}
	add := func(role string, it planner.Item) *nativeMessage {
		ordinal++
		id := orderedID("msg", target.Session+it.Key, ordinal)
		eventIDs[id] = it.Event.ID
		ts := d.Created + int64(ordinal)
		model := core.Obj(it.Event.Attrs["model"])
		provider, modelID := core.S(model["provider"]), core.S(model["id"])
		if provider == "" {
			provider = "unknown"
		}
		if modelID == "" {
			modelID = "unknown"
		}
		info := core.JsonObject{"role": role, "time": core.JsonObject{"created": ts}, "agent": "build"}
		if role == "user" {
			// The TUI initializes its next-turn model picker from the last user
			// message, even when session.model is set. Foreign or missing model
			// names must not become an invalid continuation setting.
			info["model"] = core.JsonObject{"providerID": d.Model["providerID"], "modelID": d.Model["id"]}
			parent = id
		} else {
			info["parentID"] = parent
			info["modelID"] = modelID
			info["providerID"] = provider
			info["mode"] = "build"
			info["path"] = core.JsonObject{"cwd": filepath.ToSlash(target.Cwd), "root": filepath.ToSlash(target.Cwd)}
			info["cost"] = 0
			info["tokens"] = core.JsonObject{"input": 0, "output": 0, "reasoning": 0, "cache": core.JsonObject{"read": 0, "write": 0}}
			info["time"].(core.JsonObject)["completed"] = ts
			info["finish"] = "stop"
		}
		d.Messages = append(d.Messages, nativeMessage{ID: id, Info: info, Parts: []core.JsonObject{}})
		return &d.Messages[len(d.Messages)-1]
	}
	addPart := func(m *nativeMessage, p core.JsonObject) {
		ordinal++
		p["id"] = orderedID("prt", target.Session+m.ID, ordinal)
		p["messageID"] = m.ID
		p["sessionID"] = target.Session
		m.Parts = append(m.Parts, p)
	}
	media := func(p core.Part) (core.JsonObject, error) {
		uri := core.S(p["uri"])
		sha := core.S(core.Obj(p["blob"])["sha256"])
		if sha != "" {
			if in.Hub == nil {
				return nil, fmt.Errorf("attachment storage unavailable")
			}
			b, ok := in.Hub.Blob(sha)
			if !ok {
				return nil, fmt.Errorf("attachment %s unavailable", sha)
			}
			uri = "data:" + core.S(p["mediaType"]) + ";base64," + base64.StdEncoding.EncodeToString(b)
		}
		if uri == "" {
			return nil, fmt.Errorf("OpenCode attachment has no content")
		}
		mime := core.S(p["mediaType"])
		if mime == "" {
			mime = "application/octet-stream"
		}
		return core.JsonObject{"type": "file", "mime": mime, "filename": core.S(p["name"]), "url": uri}, nil
	}
	for _, it := range in.Projection.Items {
		if it.Event == nil {
			return nil, fmt.Errorf("missing projected event")
		}
		role := it.Role
		if parent == "" && role != "user" {
			m := add("user", it)
			addPart(m, core.JsonObject{"type": "text", "text": "[Imported conversation excerpt]", "synthetic": true})
			plan.Losses = append(plan.Losses, core.Loss{Event: it.Event.ID, Field: "role", Kind: "approximated", Note: "OpenCode requires a user message before an assistant-only excerpt"})
		}
		var m *nativeMessage
		if role != "tool" {
			m = add(role, it)
		}
		for _, p := range it.Parts {
			typ := core.S(p["type"])
			switch typ {
			case "text", "reasoning":
				if m != nil {
					part := core.JsonObject{"type": typ, "text": core.S(p["text"])}
					if typ == "reasoning" {
						part["time"] = core.JsonObject{"start": d.Created, "end": d.Created}
					}
					addPart(m, part)
				}
			case "image", "file":
				f, e := media(p)
				if e != nil {
					return nil, e
				}
				if m == nil {
					return nil, fmt.Errorf("attachment outside an OpenCode message")
				}
				if role == "assistant" {
					if !strings.HasPrefix(core.S(f["url"]), "data:") {
						return nil, fmt.Errorf("OpenCode assistant attachments require embedded content")
					}
					ordinal++
					f["id"] = orderedID("prt", target.Session+m.ID, ordinal)
					f["sessionID"] = target.Session
					f["messageID"] = m.ID
					callID := "attachment_" + core.SHA256Hex(it.Key + fmt.Sprint(ordinal))[:16]
					addPart(m, core.JsonObject{"type": "tool", "tool": "attachment", "callID": callID, "state": core.JsonObject{"status": "completed", "input": core.JsonObject{}, "output": "Conversation attachment", "title": "Attachment", "metadata": core.JsonObject{}, "time": core.JsonObject{"start": d.Created, "end": d.Created}, "attachments": []any{f}}})
				} else {
					addPart(m, f)
				}
				if core.S(f["mime"]) == "text/plain" && in.Hub != nil {
					if b, ok := in.Hub.Blob(core.S(core.Obj(p["blob"])["sha256"])); ok && utf8.Valid(b) {
						addPart(m, core.JsonObject{"type": "text", "text": "[Attachment: " + core.S(f["filename"]) + "]\n" + string(b), "synthetic": true})
					}
				}
			case "tool_call":
				if m == nil {
					return nil, fmt.Errorf("OpenCode tool call outside assistant")
				}
				name, args := core.S(p["rawName"]), p["args"]
				if it.Event.Provenance.Source.Harness != "opencode" {
					if n, ok := (Vocabulary{}).FromCanonical(core.Obj(p["tool"])); ok {
						name, args = n.Name, n.Args
					}
				}
				if args == nil {
					args = core.JsonObject{}
				}
				call := core.S(p["callId"])
				state := core.JsonObject{"status": "error", "input": args, "error": "No result provided", "time": core.JsonObject{"start": d.Created, "end": d.Created}}
				part := core.JsonObject{"type": "tool", "tool": name, "callID": call, "state": state}
				addPart(m, part)
				calls[call] = part
			case "tool_result":
				call := core.S(p["callId"])
				part := calls[call]
				if part == nil {
					return nil, fmt.Errorf("OpenCode tool result has no matching call: %s", call)
				}
				text := []string{}
				attachments := []any{}
				for _, r := range core.Parts(p["parts"]) {
					switch core.S(r["type"]) {
					case "text":
						text = append(text, core.S(r["text"]))
					case "image", "file":
						f, e := media(r)
						if e != nil {
							return nil, e
						}
						ordinal++
						f["id"] = orderedID("prt", target.Session+call, ordinal)
						if !strings.HasPrefix(core.S(f["url"]), "data:") {
							return nil, fmt.Errorf("OpenCode tool attachments require embedded content")
						}
						f["sessionID"] = target.Session
						f["messageID"] = part["messageID"]
						attachments = append(attachments, f)
					}
				}
				state := core.Obj(part["state"])
				delete(state, "error")
				state["status"] = "completed"
				state["output"] = strings.Join(text, "\n")
				state["title"] = core.S(part["tool"])
				state["metadata"] = core.JsonObject{}
				if len(attachments) > 0 {
					state["attachments"] = attachments
				}
				if core.S(p["outcome"]) != "success" {
					state["status"] = "error"
					state["error"] = state["output"]
					delete(state, "output")
				}
			default:
				return nil, fmt.Errorf("unsupported OpenCode projected part %q", typ)
			}
		}
	}
	if len(d.Messages) == 0 {
		return nil, fmt.Errorf("no content selected")
	}
	for _, m := range d.Messages {
		b, _ := json.Marshal(m)
		plan.Records = append(plan.Records, engine.PlanRecord{NativeID: m.ID, EventID: eventIDs[m.ID], SHA256: core.SHA256Hex(b)})
	}
	content, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	plan.Content = string(content)
	plan.ID = "plan:" + core.SHA256Hex(target.Session+plan.Content)
	return plan, nil
}

func (c *connector) Apply(ctx context.Context, p *engine.DeliveryPlan) (core.JsonObject, error) {
	if p.Mode != "create" || p.Harness != "opencode" || p.Target.Mode != "create" {
		return nil, fmt.Errorf("invalid OpenCode create plan")
	}
	var d delivery
	if err := json.Unmarshal([]byte(p.Content), &d); err != nil {
		return nil, err
	}
	if len(d.Messages) == 0 {
		return nil, fmt.Errorf("empty OpenCode delivery")
	}
	if err := c.initialize(ctx); err != nil {
		return nil, err
	}
	db, err := c.open(ctx, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var existing int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM session WHERE id=?`, p.Target.Session).Scan(&existing)
	if err != nil {
		return nil, err
	}
	if existing > 0 {
		if err := verifyExisting(ctx, tx, p, d); err != nil {
			return nil, err
		}
		if err = shared.ApplyAttachments(p); err != nil {
			return nil, err
		}
		return core.JsonObject{"session": p.Target.Session, "path": c.db, "noop": true}, nil
	}
	cwd := filepath.ToSlash(p.Target.Cwd)
	project := "global"
	root := "/"
	// Reuse the nearest known native project. Outside known projects OpenCode uses its global project.
	rows, err := tx.QueryContext(ctx, `SELECT id,worktree FROM project`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, dir string
		if err := rows.Scan(&id, &dir); err != nil {
			rows.Close()
			return nil, err
		}
		if (cwd == dir || strings.HasPrefix(cwd, strings.TrimSuffix(dir, "/")+"/")) && len(dir) > len(root) {
			project, root = id, dir
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO project(id,worktree,time_created,time_updated,sandboxes) VALUES(?,?,?,?, '[]') ON CONFLICT(id) DO NOTHING`, project, root, d.Created, d.Created); err != nil {
		return nil, err
	}
	title := p.Target.Title
	if title == "" {
		title = "Copied conversation"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO session(id,project_id,slug,directory,title,version,time_created,time_updated,model) VALUES(?,?,?,?,?,?,?,?,?)`, p.Target.Session, project, "copied-conversation", cwd, title, "1.19.3", d.Created, d.Created, string(modelJSON(d.Model)))
	if err != nil {
		return nil, err
	}
	for _, m := range d.Messages {
		b, _ := json.Marshal(m.Info)
		ts, _ := core.Num(core.Obj(m.Info["time"])["created"])
		if _, err = tx.ExecContext(ctx, `INSERT INTO message(id,session_id,time_created,time_updated,data) VALUES(?,?,?,?,?)`, m.ID, p.Target.Session, int64(ts), int64(ts), string(b)); err != nil {
			return nil, err
		}
		for _, part := range m.Parts {
			data := core.JsonObject{}
			for k, v := range part {
				if k != "id" && k != "sessionID" && k != "messageID" {
					data[k] = v
				}
			}
			b, _ := json.Marshal(data)
			if _, err = tx.ExecContext(ctx, `INSERT INTO part(id,message_id,session_id,time_created,time_updated,data) VALUES(?,?,?,?,?,?)`, core.S(part["id"]), m.ID, p.Target.Session, int64(ts), int64(ts), string(b)); err != nil {
				return nil, err
			}
		}
	}
	if err = shared.ApplyAttachments(p); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return core.JsonObject{"session": p.Target.Session, "path": c.db}, nil
}
func verifyExisting(ctx context.Context, tx *sql.Tx, p *engine.DeliveryPlan, d delivery) error {
	conflict := fmt.Errorf("OpenCode session already exists with different content; refusing to overwrite %s", p.Target.Session)
	var count int
	var cwd string
	if err := tx.QueryRowContext(ctx, `SELECT directory FROM session WHERE id=?`, p.Target.Session).Scan(&cwd); err != nil {
		return err
	}
	if cwd != filepath.ToSlash(p.Target.Cwd) {
		return conflict
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM message WHERE session_id=?`, p.Target.Session).Scan(&count); err != nil {
		return err
	}
	if count != len(d.Messages) {
		return conflict
	}
	total := 0
	for _, m := range d.Messages {
		var data string
		if err := tx.QueryRowContext(ctx, `SELECT data FROM message WHERE id=? AND session_id=?`, m.ID, p.Target.Session).Scan(&data); err != nil {
			return conflict
		}
		b, _ := json.Marshal(m.Info)
		if data != string(b) {
			return conflict
		}
		for _, part := range m.Parts {
			total++
			copy := core.JsonObject{}
			for k, v := range part {
				if k != "id" && k != "sessionID" && k != "messageID" {
					copy[k] = v
				}
			}
			b, _ := json.Marshal(copy)
			if err := tx.QueryRowContext(ctx, `SELECT data FROM part WHERE id=? AND message_id=?`, core.S(part["id"]), m.ID).Scan(&data); err != nil || data != string(b) {
				return conflict
			}
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM part WHERE session_id=?`, p.Target.Session).Scan(&count); err != nil {
		return err
	}
	if total != count {
		return conflict
	}
	return nil
}

func modelJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func (c *connector) assetDir(session string) string { return filepath.Join(c.root, "assets", session) }
