package zcode

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"koto/internal/core"
)

// activeMessages follows ZCode's selectActiveConversationBranch contract. The
// append-only store contains abandoned turns; they must not become copied history.
func activeMessages(ids []string, revert core.JsonObject) ([]string, error) {
	target := core.S(revert["targetMessageID"])
	if len(revert) == 0 {
		return ids, nil
	}
	if core.S(revert["kind"]) != "conversation_rewind" || target == "" {
		return nil, fmt.Errorf("unsupported ZCode rewind structure")
	}
	positions := make(map[string]int, len(ids))
	for i, id := range ids {
		positions[id] = i
	}
	index := func(id string) int {
		if i, ok := positions[id]; ok {
			return i
		}
		return -1
	}

	at := index(target)
	var kept []string
	explicit := revert["keptMessageIDs"] != nil
	if explicit {
		for _, v := range core.Arr(revert["keptMessageIDs"]) {
			id := core.S(v)
			if index(id) >= 0 {
				kept = append(kept, id)
			}
		}
	} else if at >= 0 {
		kept = append(kept, ids[:at]...)
	} else {
		kept = append(kept, ids...)
	}
	if cut := core.S(revert["branchCutAfterMessageID"]); cut != "" {
		if i := index(cut); i >= 0 {
			kept = append(kept, ids[i+1:]...)
		}
		return kept, nil
	}
	if !explicit && at < 0 {
		return ids, nil
	}
	if created := core.S(revert["createdMessageID"]); created != "" {
		if i := index(created); i >= 0 {
			kept = append(kept, ids[i:]...)
		}
	}
	return kept, nil
}

func activeMessageOrder(ctx context.Context, tx *sql.Tx, session string) ([]string, error) {
	var raw sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT revert FROM session WHERE id=?`, session).Scan(&raw); err != nil {
		return nil, err
	}
	var revert core.JsonObject
	if raw.Valid && raw.String != "" {
		if err := json.Unmarshal([]byte(raw.String), &revert); err != nil {
			return nil, fmt.Errorf("invalid ZCode rewind metadata: %w", err)
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM message WHERE session_id=? ORDER BY sequence IS NULL,sequence,time_created,rowid`, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return activeMessages(ids, revert)
}

// Only content whose native semantics have been verified is exposed. Unsupported
// structures fail before any batch is yielded, rather than silently losing parts.
func (c *connector) validateNativeMessage(m nativeMessage, indexMode ...bool) error {
	indexOnly := len(indexMode) > 0 && indexMode[0]
	role := core.S(m.Info["role"])
	if role != "user" && role != "assistant" {
		return fmt.Errorf("unsupported ZCode message role %q", role)
	}
	allowed := " id sessionID role time semantics anchor metadata "
	if role == "user" {
		allowed += "format summary agent model modelSelection system tools contextSnapshot synthetic source visibility "
	} else {
		allowed += "error parentID modelId modelID providerId providerID mode planEnabled agent path summary cost tokens reasoningLevel finish structured "
	}
	for k := range m.Info {
		if !strings.Contains(allowed, " "+k+" ") {
			return fmt.Errorf("unsupported ZCode %s message field %q", role, k)
		}
	}
	if m.Info["structured"] != nil || core.S(core.Obj(m.Info["semantics"])["kind"]) == "compact_summary" || (role == "assistant" && m.Info["summary"] == true) {
		return fmt.Errorf("unsupported ZCode structured response or compacted history; refusing an incomplete copy")
	}
	for _, p := range m.Parts {
		if err := validatePartFields(p); err != nil {
			return err
		}
		switch typ := core.S(p["type"]); typ {
		case "text", "reasoning":
		case "file":
			if !indexOnly {
				if err := c.validateAttachment(p); err != nil {
					return err
				}
			}
		case "tool":
			if role != "assistant" || core.S(p["callID"]) == "" || core.S(p["tool"]) == "" {
				return fmt.Errorf("invalid ZCode tool in message %s", m.ID)
			}
			for key := range core.Obj(p["state"]) {
				if !strings.Contains(" status input raw title metadata time output error attachments ", " "+key+" ") {
					return fmt.Errorf("unsupported ZCode tool state field %q", key)
				}
			}
			switch core.S(core.Obj(p["state"])["status"]) {
			case "pending", "running", "completed", "error":
			default:
				return fmt.Errorf("unsupported ZCode tool state in %s", m.ID)
			}
			for _, a := range core.Arr(core.Obj(p["state"])["attachments"]) {
				if core.S(core.Obj(a)["type"]) != "file" {
					return fmt.Errorf("unsupported ZCode tool attachment in %s", m.ID)
				}
				if err := validatePartFields(core.Obj(a)); err != nil {
					return err
				}
				if !indexOnly {
					if err := c.validateAttachment(core.Obj(a)); err != nil {
						return err
					}
				}
			}
			if err := validateToolLayout(core.Obj(p["state"])); err != nil {
				return err
			}
		case "step-start", "step-finish", "snapshot", "patch", "retry", "timeline", "agent":
			// Runtime and display metadata is retained as context, never a user prompt.
		default:
			return fmt.Errorf("ZCode %s contains unsupported native part %q; refusing an incomplete copy", m.ID, typ)
		}
	}
	return nil
}

func validatePartFields(p core.JsonObject) error {
	fields := map[string]string{
		"text": " text synthetic ignored time metadata", "reasoning": " text metadata time",
		"file": " mime filename url source metadata", "tool": " callID tool state declarationIndex metadata",
	}
	extra, check := fields[core.S(p["type"])]
	if !check {
		return nil
	}
	allowed := " id sessionID messageID type" + extra + " "
	for k := range p {
		if !strings.Contains(allowed, " "+k+" ") {
			return fmt.Errorf("unsupported ZCode %s field %q", core.S(p["type"]), k)
		}
	}
	return nil
}
func validateToolLayout(state core.JsonObject) error {
	value, ok := core.Obj(state["metadata"])["modelContentLayout"]
	if !ok {
		return nil
	}
	layout := core.Arr(value)
	if _, valid := value.([]any); !valid {
		return fmt.Errorf("invalid ZCode tool media layout")
	}
	attachments := core.Arr(state["attachments"])
	used := make(map[int]bool)
	for _, v := range layout {
		e := core.Obj(v)
		switch core.S(e["type"]) {
		case "text":
		case "attachment":
			n, ok := core.Num(e["attachmentIndex"])
			if !ok || n < 0 || int(n) >= len(attachments) || n != float64(int(n)) {
				return fmt.Errorf("invalid ZCode tool media layout")
			}
			used[int(n)] = true
		default:
			return fmt.Errorf("unknown ZCode tool media layout")
		}
	}
	if len(layout) > 0 && len(used) != len(attachments) {
		return fmt.Errorf("ZCode tool media layout omits an attachment")
	}
	return nil
}
func (c *connector) validateAttachment(p core.JsonObject) error {
	uri := core.S(p["url"])
	meta := core.Obj(p["metadata"])
	if a := core.S(meta["artifactUri"]); strings.HasPrefix(a, "zcode-artifact://") {
		uri = a
	}
	if strings.HasPrefix(uri, "https://") || strings.HasPrefix(uri, "http://") {
		return nil
	}
	b, ok := c.attachmentBytes(uri)
	if !ok {
		return fmt.Errorf("ZCode attachment %q is unavailable; refusing an incomplete copy", core.S(p["filename"]))
	}
	if expected := core.S(meta["sha256"]); expected != "" && expected != core.SHA256Hex(b) {
		return fmt.Errorf("ZCode attachment %q changed since it was recorded", core.S(p["filename"]))
	}
	return nil
}
