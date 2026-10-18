package zcode

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

	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/planner"
)

type delivery struct {
	ModelSelection core.JsonObject `json:"modelSelection,omitempty"`
	Created        int64           `json:"created"`
	Messages       []nativeMessage `json:"messages"`
}

func orderedID(prefix, seed string, n int) string {
	return fmt.Sprintf("%s_%012x%s", prefix, n, core.SHA256Hex(seed)[:14])
}

func (c *connector) Plan(in engine.PlanInput) (*engine.DeliveryPlan, error) {
	if in.Projection == nil {
		return nil, fmt.Errorf("missing ZCode projection")
	}
	if in.Target.Mode != "create" {
		return nil, fmt.Errorf("ZCode copies must create a new session")
	}
	if !regexp.MustCompile("^sess_[A-Za-z0-9._-]+$").MatchString(in.Target.Session) {
		return nil, fmt.Errorf("invalid ZCode session ID")
	}
	target := in.Target
	if target.Cwd == "" {
		target.Cwd = in.Projection.Hints.Cwd
	}
	if target.Cwd == "" {
		return nil, fmt.Errorf("ZCode requires a project directory")
	}
	var err error
	target.Cwd, err = filepath.Abs(target.Cwd)
	if err != nil {
		return nil, err
	}
	target.Path = c.db
	attachments := []any{}
	plan := &engine.DeliveryPlan{Sink: c.id, Harness: "zcode", Thread: in.Thread, Head: in.Head, Level: in.Level, Mode: "create", Target: target, Losses: append([]core.Loss{}, in.Projection.Losses...), Notes: append([]string{}, in.Projection.Notes...), Extra: core.JsonObject{"attachments": attachments}}
	modelSelection, err := c.targetModel()
	if err != nil {
		return nil, err
	}
	d := delivery{Created: time.Now().UnixMilli(), ModelSelection: modelSelection}
	if modelSelection == nil || core.S(core.Obj(modelSelection["options"])["reasoningLevel"]) == "" {
		plan.Notes = append(plan.Notes, "Select a model in ZCode before continuing this copied session.")
	}
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
			// Bind to the target selection; foreign model IDs remain history only.
			info["model"] = core.JsonObject{}
			if d.ModelSelection != nil {
				info["modelSelection"] = d.ModelSelection
			}
			parent = id
		} else {
			info["parentID"] = parent
			info["modelId"] = modelID
			info["modelID"] = modelID
			info["providerId"] = provider
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
	currentEvent := ""
	media := func(p core.Part) (core.JsonObject, error) {
		uri := core.S(p["uri"])
		var data []byte
		sha := core.S(core.Obj(p["blob"])["sha256"])
		if sha != "" {
			if in.Hub == nil {
				return nil, fmt.Errorf("attachment storage unavailable")
			}
			b, ok := in.Hub.Blob(sha)
			if !ok {
				return nil, fmt.Errorf("attachment %s unavailable", sha)
			}
			data = b
			uri = "data:" + core.S(p["mediaType"]) + ";base64," + base64.StdEncoding.EncodeToString(b)
		}
		if data == nil {
			if b, ok := c.attachmentBytes(uri); ok {
				data = b
				uri = "data:" + core.S(p["mediaType"]) + ";base64," + base64.StdEncoding.EncodeToString(b)
			} else if strings.HasPrefix(uri, "https://") || strings.HasPrefix(uri, "http://") {
				plan.Losses = append(plan.Losses, core.Loss{Event: currentEvent, Field: "attachment", Kind: "referenced", Note: "Remote attachment preserved as a URL; its bytes are not stored locally"})
			} else {
				return nil, fmt.Errorf("ZCode attachment content unavailable")
			}
		}
		if uri == "" {
			return nil, fmt.Errorf("ZCode attachment has no content")
		}
		mime := core.S(p["mediaType"])
		if mime == "" {
			mime = "application/octet-stream"
		}
		f := core.JsonObject{"type": "file", "mime": mime, "filename": core.S(p["name"]), "url": uri}
		if data != nil {
			f["metadata"] = core.JsonObject{"storageKind": "inline", "sizeBytes": len(data), "sha256": core.SHA256Hex(data), "recoverability": "provider_ready"}
			if !strings.HasPrefix(mime, "image/") && !strings.HasPrefix(mime, "video/") && mime != "application/pdf" {
				path := filepath.Join(c.assetDir(target.Session), core.SHA256Hex(data), shared.AttachmentName(core.S(p["name"])))
				attachments = append(attachments, core.JsonObject{"path": path, "data": data})
				f["url"] = shared.FileURL(path).String()
				f["source"] = core.JsonObject{"type": "file", "path": path, "text": core.JsonObject{"value": path, "start": 0, "end": len(path)}}
				f["metadata"] = core.JsonObject{"storageKind": "local_ref", "sizeBytes": len(data), "sha256": core.SHA256Hex(data), "recoverability": "rebuildable", "originalUrl": path}
			}
		}
		return f, nil
	}
	for _, it := range in.Projection.Items {
		if it.Event == nil {
			return nil, fmt.Errorf("missing projected event")
		}
		currentEvent = it.Event.ID
		role := it.Role
		if role != "user" && role != "assistant" && role != "tool" {
			return nil, fmt.Errorf("unsupported ZCode projected role %q", role)
		}
		if it.Event.Kind == "context" {
			return nil, fmt.Errorf("system context cannot be copied")
		}
		if parent == "" && role != "user" {
			m := add("user", it)
			m.Info["synthetic"] = true
			m.Info["source"] = "shared_context"
			addPart(m, core.JsonObject{"type": "text", "text": "[Imported conversation excerpt]", "synthetic": true})
			plan.Losses = append(plan.Losses, core.Loss{Event: it.Event.ID, Field: "role", Kind: "approximated", Note: "ZCode requires a user message before an assistant-only excerpt"})
		}
		var m *nativeMessage
		if role != "tool" {
			m = add(role, it)
		}
		for _, p := range it.Parts {
			typ := core.S(p["type"])
			switch typ {
			case "text", "reasoning":
				if typ == "reasoning" && role != "assistant" {
					return nil, fmt.Errorf("ZCode reasoning outside assistant")
				}
				if m == nil {
					return nil, fmt.Errorf("ZCode text outside user/assistant message")
				}
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
					return nil, fmt.Errorf("attachment outside an ZCode message")
				}
				addPart(m, f)
			case "tool_call":
				if m == nil || role != "assistant" {
					return nil, fmt.Errorf("ZCode tool call outside assistant")
				}
				name, args := core.S(p["rawName"]), p["args"]
				if it.Event.Provenance.Source.Harness != "zcode" {
					if n, ok := (Vocabulary{}).FromCanonical(core.Obj(p["tool"])); ok {
						name, args = n.Name, n.Args
					}
				}
				if args == nil {
					args = core.JsonObject{}
				}
				call := core.S(p["callId"])
				if call == "" || calls[call] != nil {
					return nil, fmt.Errorf("missing or duplicate ZCode tool call ID")
				}
				state := core.JsonObject{"status": "error", "input": args, "error": "No result provided", "time": core.JsonObject{"start": d.Created, "end": d.Created}}
				part := core.JsonObject{"type": "tool", "tool": name, "callID": call, "state": state}
				addPart(m, part)
				calls[call] = part
			case "tool_result":
				if role != "tool" {
					return nil, fmt.Errorf("ZCode tool result outside tool response")
				}
				call := core.S(p["callId"])
				part := calls[call]
				if part == nil {
					return nil, fmt.Errorf("ZCode tool result has no matching call: %s", call)
				}
				text := []string{}
				attachments := []any{}
				layout := []any{}
				for _, r := range core.Parts(p["parts"]) {
					switch core.S(r["type"]) {
					case "text":
						text = append(text, core.S(r["text"]))
						layout = append(layout, core.JsonObject{"type": "text", "text": core.S(r["text"])})
					case "image", "file":
						f, e := media(r)
						if e != nil {
							return nil, e
						}
						ordinal++
						f["id"] = orderedID("prt", target.Session+call, ordinal)

						f["sessionID"] = target.Session
						f["messageID"] = part["messageID"]
						layout = append(layout, core.JsonObject{"type": "attachment", "attachmentIndex": len(attachments)})
						attachments = append(attachments, f)
					default:
						return nil, fmt.Errorf("unsupported ZCode tool result part %q", core.S(r["type"]))
					}
				}
				state := core.Obj(part["state"])
				if state["_result"] == true {
					return nil, fmt.Errorf("duplicate ZCode tool result: %s", call)
				}
				state["_result"] = true
				delete(state, "error")
				state["status"] = "completed"
				state["output"] = strings.Join(text, "\n")
				state["title"] = core.S(part["tool"])
				state["metadata"] = core.JsonObject{"modelContentLayout": layout}
				if len(attachments) > 0 {
					state["attachments"] = attachments
				}
				if core.S(p["outcome"]) != "success" {
					state["status"] = "error"
					state["error"] = state["output"]
					delete(state, "output")
				}
			default:
				return nil, fmt.Errorf("unsupported ZCode projected part %q", typ)
			}
		}
	}
	for id, p := range calls {
		state := core.Obj(p["state"])
		if state["_result"] != true {
			return nil, fmt.Errorf("ZCode tool call %s has no selected result", id)
		}
		delete(state, "_result")
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
	plan.Extra["attachments"] = attachments
	plan.Content = string(content)
	plan.ID = "plan:" + core.SHA256Hex(target.Session+plan.Content)
	return plan, nil
}

func (c *connector) Apply(ctx context.Context, p *engine.DeliveryPlan) (core.JsonObject, error) {
	if p.Mode != "create" || p.Harness != "zcode" || p.Target.Mode != "create" {
		return nil, fmt.Errorf("invalid ZCode create plan")
	}
	var d delivery
	if err := json.Unmarshal([]byte(p.Content), &d); err != nil {
		return nil, err
	}
	if len(d.Messages) == 0 {
		return nil, fmt.Errorf("empty ZCode delivery")
	}
	if err := c.initialize(ctx); err != nil {
		return nil, err
	}
	db, err := c.open(ctx, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	hasIndex, err := c.attachIndex(ctx, db)
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	committed := false
	cleanup, err := stageAttachments(p)
	if err != nil {
		return nil, err
	}
	defer func() {
		if !committed {
			cleanup()
		}
	}()
	var existing int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM session WHERE id=?`, p.Target.Session).Scan(&existing)
	if err != nil {
		return nil, err
	}
	if existing > 0 {
		if err := verifyExisting(ctx, tx, p, d); err != nil {
			return nil, err
		}
		if hasIndex {
			if err = c.indexSession(ctx, tx, p, d); err != nil {
				return nil, err
			}
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		committed = true
		return core.JsonObject{"session": p.Target.Session, "path": c.db, "noop": true}, nil
	}
	cwd := p.Target.Cwd
	title := p.Target.Title
	if title == "" {
		title = "Copied conversation"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO session(id,project_id,slug,directory,title,version,time_created,time_updated,task_type,title_source) VALUES(?,?,?,?,?,?,?,?,?,?)`, p.Target.Session, projectID(cwd), "copied-conversation", cwd, title, "0.16.9", d.Created, d.Created, "interactive", "custom")
	if err != nil {
		return nil, err
	}
	if d.ModelSelection != nil {
		entry, _ := json.Marshal(core.JsonObject{"modelSelection": d.ModelSelection})
		if _, err = tx.ExecContext(ctx, `INSERT INTO session_entry(id,session_id,type,time_created,time_updated,data) VALUES(?,?,'runtime/model_selection',?,?,?)`, p.Target.Session+":runtime-model-selection", p.Target.Session, d.Created, d.Created, string(entry)); err != nil {
			return nil, err
		}
	}
	messageSequence := 0
	for _, m := range d.Messages {
		b, _ := json.Marshal(m.Info)
		ts, _ := core.Num(core.Obj(m.Info["time"])["created"])
		if _, err = tx.ExecContext(ctx, `INSERT INTO message(id,session_id,time_created,time_updated,data,sequence) VALUES(?,?,?,?,?,?)`, m.ID, p.Target.Session, int64(ts), int64(ts), string(b), messageSequence); err != nil {
			return nil, err
		}
		messageSequence++
		for partSequence, part := range m.Parts {
			data := core.JsonObject{}
			for k, v := range part {
				if k != "id" && k != "sessionID" && k != "messageID" {
					data[k] = v
				}
			}
			b, _ := json.Marshal(data)
			if _, err = tx.ExecContext(ctx, `INSERT INTO part(id,message_id,session_id,time_created,time_updated,data,sequence) VALUES(?,?,?,?,?,?,?)`, core.S(part["id"]), m.ID, p.Target.Session, int64(ts), int64(ts), string(b), partSequence); err != nil {
				return nil, err
			}
		}
	}
	if hasIndex {
		if err = c.indexSession(ctx, tx, p, d); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return core.JsonObject{"session": p.Target.Session, "path": c.db}, nil
}
func verifyExisting(ctx context.Context, tx *sql.Tx, p *engine.DeliveryPlan, d delivery) error {
	conflict := fmt.Errorf("ZCode session already exists with different content; refusing to overwrite %s", p.Target.Session)
	var count int
	var cwd, title string
	var archived sql.NullInt64
	var revert sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT directory,title,time_archived,revert FROM session WHERE id=?`, p.Target.Session).Scan(&cwd, &title, &archived, &revert); err != nil {
		return err
	}
	wantTitle := p.Target.Title
	if wantTitle == "" {
		wantTitle = "Copied conversation"
	}
	if cwd != p.Target.Cwd || title != wantTitle || archived.Valid || (revert.Valid && revert.String != "" && revert.String != "null") {
		return conflict
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM message WHERE session_id=?`, p.Target.Session).Scan(&count); err != nil {
		return err
	}
	if count != len(d.Messages) {
		return conflict
	}
	total := 0
	for messageSequence, m := range d.Messages {
		var data string
		var sequence sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT data,sequence FROM message WHERE id=? AND session_id=?`, m.ID, p.Target.Session).Scan(&data, &sequence); err != nil || !sequence.Valid || sequence.Int64 != int64(messageSequence) {
			return conflict
		}
		b, _ := json.Marshal(m.Info)
		if data != string(b) {
			return conflict
		}
		for partSequence, part := range m.Parts {
			total++
			copy := core.JsonObject{}
			for k, v := range part {
				if k != "id" && k != "sessionID" && k != "messageID" {
					copy[k] = v
				}
			}
			b, _ := json.Marshal(copy)
			if err := tx.QueryRowContext(ctx, `SELECT data,sequence FROM part WHERE id=? AND message_id=?`, core.S(part["id"]), m.ID).Scan(&data, &sequence); err != nil || data != string(b) || !sequence.Valid || sequence.Int64 != int64(partSequence) {
				return conflict
			}
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM part WHERE session_id=?`, p.Target.Session).Scan(&count); err != nil {
		return err
	}
	if d.ModelSelection != nil {
		var raw string
		b, _ := json.Marshal(core.JsonObject{"modelSelection": d.ModelSelection})
		if err := tx.QueryRowContext(ctx, `SELECT data FROM session_entry WHERE session_id=? AND type='runtime/model_selection' ORDER BY time_created DESC LIMIT 1`, p.Target.Session).Scan(&raw); err != nil || raw != string(b) {
			return conflict
		}
	}
	if total != count {
		return conflict
	}
	return nil
}

func (c *connector) assetDir(session string) string {
	return filepath.Join(c.root, "cli", "artifacts", session)
}
