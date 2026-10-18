package zcode

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
)

type nativeMessage struct {
	ID    string            `json:"id"`
	Info  core.JsonObject   `json:"info"`
	Parts []core.JsonObject `json:"parts"`
}

func (c *connector) Tail(ctx context.Context, ref core.NativeRef, from *core.SourceOffset, yield func(core.SourceBatch) error) error {
	indexOnly := engine.IsIndexOnly(ctx)
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
	var revert sql.NullString
	if err = tx.QueryRowContext(ctx, `SELECT revert FROM session WHERE id=?`, ref.Session).Scan(&revert); err != nil {
		return err
	}
	revision += "/" + core.SHA256Hex(revert.String)
	if from != nil && core.S(from.State["revision"]) == revision {
		return nil
	}
	order, err := activeMessageOrder(ctx, tx, ref.Session)
	if err != nil {
		return err
	}
	prefix := core.ZeroPrefix
	index := 0
	batch := core.SourceBatch{Ref: ref, Meta: ref.Meta}
	flush := func(final bool) error {
		if !final && len(batch.Records) == 0 {
			return nil
		}
		batch.Offset = core.SourceOffset{Position: core.BytePosition{Kind: "seq", Seq: int64(index)}, Records: index, PrefixHash: prefix, FormatVersion: "zcode-sqlite-v1"}
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
		rawBytes, rawSize := b, len(b)
		if indexOnly {
			rawBytes = nil
			if kind != "message" || actor != "user" {
				parts = nil
			}
		}
		batch.Records = append(batch.Records, core.BatchRecord{NativeID: id, Index: index, Raw: rawBytes, SHA256: sha, Bytes: rawSize})
		index++
		batch.Drafts = append(batch.Drafts, core.DraftEvent{NativeID: id, Kind: kind, Actor: actor, Parts: parts, Attrs: attrs, Time: &ts})
	}
	emit := func(m nativeMessage) error {
		ts, _ := core.Num(core.Obj(m.Info["time"])["created"])
		role := core.S(m.Info["role"])
		model := core.Obj(m.Info["model"])
		provider, modelID := core.S(model["providerID"]), core.S(model["modelID"])
		if selection := core.Obj(m.Info["modelSelection"]); len(selection) > 0 {
			provider, modelID = core.S(selection["providerId"]), core.S(selection["modelId"])
		}
		if role == "assistant" {
			provider = core.S(m.Info["providerID"])
			modelID = core.S(m.Info["modelID"])
			if v := core.S(m.Info["providerId"]); v != "" {
				provider = v
			}
			if v := core.S(m.Info["modelId"]); v != "" {
				modelID = v
			}
		}
		attrs := core.JsonObject{"model": core.JsonObject{"provider": provider, "id": modelID}, "nativeMessageID": m.ID}
		if role == "assistant" {
			attrs["stopReason"] = "in_progress"
		}
		put := func(b []byte) core.JsonObject {
			if indexOnly {
				return nil
			}
			batch.Blobs = append(batch.Blobs, b)
			return core.JsonObject{"sha256": core.SHA256Hex(b), "bytes": len(b)}
		}
		if system := core.S(m.Info["system"]); system != "" {
			appendRecord(m.ID+":system", "context", "system", []core.Part{{"type": "text", "text": system}}, nil, system, ts)
		}
		if snapshot := core.Obj(m.Info["contextSnapshot"]); len(snapshot) > 0 {
			appendRecord(m.ID+":environment", "context", "system", []core.Part{{"type": "opaque", "nativeType": "contextSnapshot", "value": snapshot}}, nil, snapshot, ts)
		}
		decodeText := func(text string) []core.Part {
			if indexOnly {
				return shared.DecodeAttachmentTextIndex(text, c.assetDir(ref.Session))
			}
			return shared.DecodeAttachmentText(text, c.assetDir(ref.Session), put)
		}
		decode := func(p core.JsonObject) ([]core.Part, error) {
			if core.S(p["type"]) == "text" && p["ignored"] != true {
				return decodeText(core.S(p["text"])), nil
			}
			return c.decodePart(p, put, indexOnly)
		}
		if runtimeContext(m.Info) {
			appendRecord(m.ID, "context", "system", []core.Part{{"type": "opaque", "nativeType": "runtime_message", "value": core.JsonObject{"id": m.ID, "info": m.Info, "parts": m.Parts}}}, attrs, m, ts)
			if len(batch.Records) >= 64 {
				return flush(false)
			}
			return nil
		}
		if role == "user" {
			ps := []core.Part{}
			for _, p := range m.Parts {
				if syntheticContextPart(m.Info, p) {
					parts, err := decode(p)
					if err != nil {
						return err
					}
					appendRecord(core.S(p["id"]), "context", "system", parts, nil, p, ts)
					continue
				}
				if metadataPart(p) {
					appendRecord(core.S(p["id"]), "context", "harness", []core.Part{{"type": "opaque", "nativeType": p["type"], "value": p}}, nil, p, ts)
					continue
				}
				parts, err := decode(p)
				if err != nil {
					return err
				}
				ps = append(ps, parts...)
			}
			if len(ps) > 0 {
				appendRecord(m.ID, "message", "user", ps, attrs, m, ts)
			}
		} else if role == "assistant" {
			var superseded []core.JsonObject
			m.Parts, superseded = orderedToolParts(m.Parts)
			for _, p := range superseded {
				appendRecord(core.S(p["id"]), "context", "harness", []core.Part{{"type": "opaque", "nativeType": "superseded_tool", "value": p}}, nil, p, ts)
			}
			for partIndex, p := range m.Parts {
				if metadataPart(p) {
					appendRecord(core.S(p["id"]), "context", "harness", []core.Part{{"type": "opaque", "nativeType": p["type"], "value": p}}, nil, p, ts)
					continue
				}
				partAttrs := core.JsonObject{}
				for k, v := range attrs {
					partAttrs[k] = v
				}
				partAttrs["stopReason"] = messageStop(m.Info, m.Parts, partIndex)
				id := core.S(p["id"])
				if core.S(p["type"]) == "tool" {
					state := core.Obj(p["state"])
					name, call := core.S(p["tool"]), core.S(p["callID"])
					appendRecord(id+":call", "message", "assistant", []core.Part{{"type": "tool_call", "callId": call, "rawName": name, "args": state["input"], "tool": Vocabulary{}.ToCanonical(name, state["input"])}}, partAttrs, core.JsonObject{"info": m.Info, "part": p, "stage": "call"}, ts)
					status := core.S(state["status"])
					if status == "completed" || status == "error" {
						outcome, text := "success", core.S(state["output"])
						if status == "error" {
							outcome = "error"
							text = core.S(state["error"])
						}
						ps := decodeText(text)
						attachments := core.Arr(state["attachments"])
						layout := core.Arr(core.Obj(state["metadata"])["modelContentLayout"])
						if len(layout) > 0 && len(attachments) > 0 {
							ps = nil
							for _, v := range layout {
								entry := core.Obj(v)
								if core.S(entry["type"]) == "text" {
									ps = append(ps, core.Part{"type": "text", "text": core.S(entry["text"])})
								} else {
									n, ok := core.Num(entry["attachmentIndex"])
									if !ok || n < 0 || int(n) >= len(attachments) || n != float64(int(n)) {
										return fmt.Errorf("invalid ZCode tool media layout")
									}
									parts, err := c.decodePart(core.Obj(attachments[int(n)]), put, indexOnly)
									if err != nil {
										return err
									}
									ps = append(ps, parts...)
								}
							}
						} else {
							for _, a := range attachments {
								parts, err := c.decodePart(core.Obj(a), put, indexOnly)
								if err != nil {
									return err
								}
								ps = append(ps, parts...)
							}
						}

						appendRecord(id+":result", "message", "tool", []core.Part{{"type": "tool_result", "callId": call, "outcome": outcome, "parts": core.PartsJSON(ps)}}, core.JsonObject{"toolName": name}, core.JsonObject{"info": m.Info, "part": p, "stage": "result"}, ts)
					}
				} else {
					ps, err := c.decodePart(p, put, indexOnly)
					if err != nil {
						return err
					}
					if len(ps) > 0 {
						appendRecord(id, "message", "assistant", ps, partAttrs, core.JsonObject{"info": m.Info, "part": p}, ts)
					}
				}
			}
		} else {
			return fmt.Errorf("unsupported ZCode message role %q", role)
		}
		if len(batch.Records) >= 64 {
			return flush(false)
		}
		return nil
	}
	// Preflight every active message before publishing a batch. Read each native
	// message separately so large sessions do not live twice in memory.
	stmt, err := tx.PrepareContext(ctx, `SELECT m.data,p.id,p.data FROM message m LEFT JOIN part p ON p.message_id=m.id WHERE m.id=? AND m.session_id=? ORDER BY p.sequence IS NULL,p.sequence,p.time_created,p.id`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, id := range order {
		m, err := readMessage(ctx, stmt, ref.Session, id)
		if err != nil {
			return err
		}
		if err = c.validateNativeMessage(m, indexOnly); err != nil {
			return err
		}
	}
	for _, id := range order {
		m, err := readMessage(ctx, stmt, ref.Session, id)
		if err != nil {
			return err
		}
		if err = emit(m); err != nil {
			return err
		}
	}
	return flush(true)
}

func readMessage(ctx context.Context, stmt *sql.Stmt, session, id string) (nativeMessage, error) {
	m := nativeMessage{ID: id}
	rows, err := stmt.QueryContext(ctx, id, session)
	if err != nil {
		return m, err
	}
	defer rows.Close()
	for rows.Next() {
		var data string
		var pid, pdata sql.NullString
		if err = rows.Scan(&data, &pid, &pdata); err != nil {
			return m, err
		}
		if m.Info == nil {
			if err = json.Unmarshal([]byte(data), &m.Info); err != nil {
				return m, fmt.Errorf("ZCode message %s: %w", id, err)
			}
		}
		if pid.Valid {
			var p core.JsonObject
			if err = json.Unmarshal([]byte(pdata.String), &p); err != nil {
				return m, fmt.Errorf("ZCode part %s: %w", pid.String, err)
			}
			p["id"] = pid.String
			m.Parts = append(m.Parts, p)
		}
	}
	if err = rows.Err(); err != nil {
		return m, err
	}
	if m.Info == nil {
		return m, fmt.Errorf("ZCode message %s disappeared", id)
	}
	return m, nil
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
