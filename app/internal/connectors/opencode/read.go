package opencode

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"koto/internal/connectors/shared"
	"koto/internal/core"
)

type nativeMessage struct {
	ID    string            `json:"id"`
	Info  core.JsonObject   `json:"info"`
	Parts []core.JsonObject `json:"parts"`
}

func (c *connector) Tail(ctx context.Context, ref core.NativeRef, from *core.SourceOffset, yield func(core.SourceBatch) error) error {
	db, err := c.open(ctx, false)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var v2 int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='session_message'`).Scan(&v2); err != nil {
		return err
	}
	if v2 > 0 {
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM session_message WHERE session_id=?`, ref.Session).Scan(&v2); err != nil {
			return err
		}
		if v2 > 0 {
			return fmt.Errorf("OpenCode v2 message storage is not yet supported for session %s", ref.Session)
		}
	}
	var revision string
	err = tx.QueryRowContext(ctx, `SELECT printf('%d/%d/%d/%d/%d/%d/%d',time_updated,
 (SELECT count(*) FROM message WHERE session_id=session.id),
 (SELECT coalesce(max(time_updated),0) FROM message WHERE session_id=session.id),
 (SELECT coalesce(sum(length(data)),0) FROM message WHERE session_id=session.id),
 (SELECT count(*) FROM part WHERE session_id=session.id),
 (SELECT coalesce(max(time_updated),0) FROM part WHERE session_id=session.id),
 (SELECT coalesce(sum(length(data)),0) FROM part WHERE session_id=session.id)) FROM session WHERE id=?`, ref.Session).Scan(&revision)
	if err != nil {
		return err
	}
	if from != nil && core.S(from.State["revision"]) == revision {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT m.id,m.data,p.id,p.data FROM message m LEFT JOIN part p ON p.message_id=m.id WHERE m.session_id=? ORDER BY m.time_created,m.id,p.id`, ref.Session)
	if err != nil {
		return err
	}
	defer rows.Close()
	prefix := core.ZeroPrefix
	index := 0
	batch := core.SourceBatch{Ref: ref, Meta: ref.Meta}
	flush := func(final bool) error {
		if !final && len(batch.Records) == 0 {
			return nil
		}
		batch.Offset = core.SourceOffset{Position: core.BytePosition{Kind: "seq", Seq: int64(index)}, Records: index, PrefixHash: prefix, FormatVersion: "opencode-sqlite-v1"}
		if final {
			batch.Offset.State = core.JsonObject{"revision": revision}
		}
		err := yield(batch)
		batch = core.SourceBatch{Ref: ref}
		return err
	}
	appendRecord := func(id, kind, actor string, parts []core.Part, attrs core.JsonObject, raw any, ts float64) {
		b, _ := json.Marshal(core.JsonObject{"record": raw, "attrs": attrs})
		sha := core.SHA256Hex(b)
		prefix = core.ChainPrefix(prefix, sha)
		batch.Records = append(batch.Records, core.BatchRecord{NativeID: id, Index: index, Raw: b, SHA256: sha, Bytes: len(b)})
		index++
		batch.Drafts = append(batch.Drafts, core.DraftEvent{NativeID: id, Kind: kind, Actor: actor, Parts: parts, Attrs: attrs, Time: &ts})
	}
	emit := func(m nativeMessage) error {
		ts, _ := core.Num(core.Obj(m.Info["time"])["created"])
		role := core.S(m.Info["role"])
		model := core.Obj(m.Info["model"])
		provider, modelID := core.S(model["providerID"]), core.S(model["modelID"])
		if role == "assistant" {
			provider = core.S(m.Info["providerID"])
			modelID = core.S(m.Info["modelID"])
		}
		attrs := core.JsonObject{"model": core.JsonObject{"provider": provider, "id": modelID}, "nativeMessageID": m.ID}
		if role == "assistant" {
			attrs["stopReason"] = "in_progress"
		}
		put := func(b []byte) core.JsonObject {
			batch.Blobs = append(batch.Blobs, b)
			return core.JsonObject{"sha256": core.SHA256Hex(b), "bytes": len(b)}
		}
		if system := core.S(m.Info["system"]); system != "" {
			appendRecord(m.ID+":system", "context", "system", []core.Part{{"type": "text", "text": system}}, nil, system, ts)
		}
		decode := func(p core.JsonObject) []core.Part {
			if core.S(p["type"]) == "text" && p["ignored"] != true {
				return shared.DecodeAttachmentText(core.S(p["text"]), c.assetDir(ref.Session), put)
			}
			return decodePart(p, put)
		}
		if role == "user" {
			ps := []core.Part{}
			for _, p := range m.Parts {
				ps = append(ps, decode(p)...)
			}
			if len(ps) > 0 {
				appendRecord(m.ID, "message", "user", ps, attrs, m, ts)
			}
		} else if role == "assistant" {
			for partIndex, p := range m.Parts {
				partAttrs := core.JsonObject{}
				for k, v := range attrs {
					partAttrs[k] = v
				}
				partAttrs["stopReason"] = messageStop(m.Info, m.Parts, partIndex)
				id := core.S(p["id"])
				if core.S(p["type"]) == "tool" {
					state := core.Obj(p["state"])
					name, call := core.S(p["tool"]), core.S(p["callID"])
					appendRecord(id+":call", "message", "assistant", []core.Part{{"type": "tool_call", "callId": call, "rawName": name, "args": state["input"], "tool": Vocabulary{}.ToCanonical(name, state["input"])}}, partAttrs, core.JsonObject{"part": p, "stage": "call"}, ts)
					status := core.S(state["status"])
					if status == "completed" || status == "error" {
						outcome, text := "success", core.S(state["output"])
						if status == "error" {
							outcome = "error"
							text = core.S(state["error"])
						}
						ps := shared.DecodeAttachmentText(text, c.assetDir(ref.Session), put)
						for _, a := range core.Arr(state["attachments"]) {
							ps = append(ps, decodePart(core.Obj(a), put)...)
						}
						appendRecord(id+":result", "message", "tool", []core.Part{{"type": "tool_result", "callId": call, "outcome": outcome, "parts": core.PartsJSON(ps)}}, core.JsonObject{"toolName": name}, core.JsonObject{"part": p, "stage": "result"}, ts)
					}
				} else {
					ps := decodePart(p, put)
					if len(ps) > 0 {
						appendRecord(id, "message", "assistant", ps, partAttrs, p, ts)
					}
				}
			}
		} else {
			return fmt.Errorf("unsupported OpenCode message role %q", role)
		}
		if len(batch.Records) >= 64 {
			return flush(false)
		}
		return nil
	}
	var current nativeMessage
	for rows.Next() {
		var id, data string
		var pid, pdata sql.NullString
		if err := rows.Scan(&id, &data, &pid, &pdata); err != nil {
			return err
		}
		if current.ID != id {
			if current.ID != "" {
				if err := emit(current); err != nil {
					return err
				}
			}
			current = nativeMessage{ID: id}
			if err := json.Unmarshal([]byte(data), &current.Info); err != nil {
				return fmt.Errorf("OpenCode message %s: %w", id, err)
			}
		}
		if pid.Valid {
			var p core.JsonObject
			if err := json.Unmarshal([]byte(pdata.String), &p); err != nil {
				return fmt.Errorf("OpenCode part %s: %w", pid.String, err)
			}
			p["id"] = pid.String
			current.Parts = append(current.Parts, p)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if current.ID != "" {
		if err := emit(current); err != nil {
			return err
		}
	}
	return flush(true)
}

func decodePart(p core.JsonObject, put func([]byte) core.JsonObject) []core.Part {
	switch core.S(p["type"]) {
	case "text":
		if p["ignored"] == true {
			return nil
		}
		return []core.Part{{"type": "text", "text": core.S(p["text"])}}
	case "reasoning":
		return []core.Part{{"type": "reasoning", "text": core.S(p["text"])}}
	case "file":
		mime := core.S(p["mime"])
		typ := "file"
		if strings.HasPrefix(mime, "image/") {
			typ = "image"
		}
		out := core.Part{"type": typ, "mediaType": mime, "name": core.S(p["filename"])}
		uri := core.S(p["url"])
		if strings.HasPrefix(uri, "data:") {
			if comma := strings.IndexByte(uri, ','); comma > 0 {
				var b []byte
				var err error
				if strings.HasSuffix(uri[:comma], ";base64") {
					b, err = base64.StdEncoding.DecodeString(uri[comma+1:])
				} else {
					var s string
					s, err = url.PathUnescape(uri[comma+1:])
					b = []byte(s)
				}
				if err == nil {
					out["blob"] = put(b)
				} else {
					out["uri"] = uri
				}
			}
		} else {
			out["uri"] = uri
		}
		return []core.Part{out}
	case "compaction":
		return []core.Part{{"type": "text", "text": "[OpenCode context compaction]"}}
	case "subtask":
		return []core.Part{{"type": "text", "text": core.S(p["prompt"])}}
	case "step-start", "step-finish", "snapshot", "patch", "retry", "agent":
		return nil
	default:
		return []core.Part{{"type": "opaque", "nativeType": p["type"], "value": p}}
	}
}

func messageStop(info core.JsonObject, parts []core.JsonObject, at int) string {
	last := -1
	hasTools := false
	for i, p := range parts {
		switch core.S(p["type"]) {
		case "tool":
			hasTools = true
			last = i
		case "text", "reasoning", "file", "subtask":
			if p["ignored"] != true {
				last = i
			}
		}
	}
	if at != last {
		return "in_progress"
	}
	if e := core.Obj(info["error"]); len(e) > 0 {
		if strings.Contains(core.S(e["name"]), "Aborted") {
			return "aborted"
		}
		return "error"
	}
	if completed, _ := core.Num(core.Obj(info["time"])["completed"]); completed <= 0 {
		return "in_progress"
	}
	switch core.S(info["finish"]) {
	case "stop", "end_turn":
		if hasTools {
			return "toolUse"
		}
		return "stop"
	case "tool-calls":
		return "toolUse"
	case "length":
		return "length"
	case "content-filter":
		return "error"
	}
	return "in_progress"
}
