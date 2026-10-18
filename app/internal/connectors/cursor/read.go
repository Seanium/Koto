package cursor

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
	"mime"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

// Checkpoints hold typed turns and the model's expanded history. Verify their
// boundary mapping before exporting; pruning UI bubbles alone leaks old turns.
func (c *connector) Tail(ctx context.Context, ref core.NativeRef, from *core.SourceOffset, yield func(core.SourceBatch) error) error {
	if !sessionID.MatchString(ref.Session) {
		return fmt.Errorf("invalid Cursor session ID")
	}
	db, err := c.open(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	raw, err := readKV(ctx, tx, "composerData:"+ref.Session)
	if err != nil {
		return err
	}
	var meta core.JsonObject
	if err = json.Unmarshal(raw, &meta); err != nil {
		return err
	}
	if core.S(meta["composerId"]) != ref.Session {
		return fmt.Errorf("Cursor session ID mismatch")
	}
	return tailConversation(ctx, tx, ref, from, raw, meta, format, true, yield)
}

func tailConversation(ctx context.Context, tx querier, ref core.NativeRef, from *core.SourceOffset, raw []byte, meta core.JsonObject, version string, withUI bool, yield func(core.SourceBatch) error) error {
	indexOnly := engine.IsIndexOnly(ctx)
	revision := core.SHA256Hex(raw)
	if from != nil && core.S(from.State["revision"]) == revision {
		return nil
	}
	root, err := readState(core.S(meta["conversationState"]))
	if err != nil {
		return err
	}
	for _, n := range []int{6, 11, 13} {
		if root.has(n) {
			return fmt.Errorf("Cursor compacted history requires an unsupported checkpoint form (field %d)", n)
		}
	}
	// Subagent state/thread/run maps (16, 24, 30, 31) are side records. The
	// parent's next request uses rootPromptMessagesJson, including the Task
	// result, but not the child's individual reads or hidden conversation.
	// Export that parent context without following or requiring child blobs.
	hashes := root.all(1)
	// The local runtime may prepend or resize its system prefix when an
	// imported conversation is first resumed. Earlier checkpoints retain
	// their original prefix; conversation hashes must still match exactly.
	leadingSystems := func(hashes [][]byte) (int, error) {
		for i, h := range hashes {
			b, e := readBlob(ctx, tx, h)
			if e != nil {
				return 0, e
			}
			var m struct {
				Role string `json:"role"`
			}
			if e = json.Unmarshal(b, &m); e != nil {
				return 0, e
			}
			if m.Role != "system" {
				return i, nil
			}
		}
		return len(hashes), nil
	}
	systemCount, err := leadingSystems(hashes)
	if err != nil {
		return err
	}
	users := map[int]wireMessage{}
	firstUser := len(hashes)
	lastStart := -1
	for _, hash := range root.all(8) {
		blob, err := readBlob(ctx, tx, hash)
		if err != nil {
			return err
		}
		turn, err := parseWire(blob)
		if err != nil {
			return err
		}
		if !turn.has(1) || turn.has(2) {
			return fmt.Errorf("unsupported Cursor non-agent turn")
		}
		a, err := turn.message(1)
		if err != nil {
			return err
		}
		blob, err = readBlob(ctx, tx, a.bytes(1))
		if err != nil {
			return err
		}
		user, err := parseWire(blob)
		if err != nil {
			return err
		}
		if !user.has(10) {
			return fmt.Errorf("Cursor user message has no model-history checkpoint")
		}
		previous, err := readBlob(ctx, tx, user.bytes(10))
		if err != nil {
			return err
		}
		state, err := parseWire(previous)
		if err != nil {
			return err
		}
		prefix := state.all(1)
		oldSystems, err := leadingSystems(prefix)
		if err != nil {
			return err
		}
		shift := systemCount - oldSystems
		start := len(prefix) + shift
		if start >= len(hashes) || start <= lastStart {
			return fmt.Errorf("Cursor turn boundaries do not match model history")
		}
		for i, h := range prefix {
			if i >= oldSystems && i+shift >= firstUser && !bytes.Equal(h, hashes[i+shift]) {
				return fmt.Errorf("Cursor edited or compacted model history cannot be mapped losslessly")
			}
		}
		users[start] = user
		lastStart = start
		firstUser = min(firstUser, start)
	}
	if len(users) == 0 && len(hashes) > 0 {
		return fmt.Errorf("Cursor model history has no matching user turns")
	}
	prefix := core.ZeroPrefix
	count := 0
	batchMeta := core.JsonObject{}
	for k, v := range ref.Meta {
		batchMeta[k] = v
	}
	identity := core.ReadIdentity(ref.Meta)
	// The stored selection is the session's last model, not an account or proof
	// that every earlier turn used this provider. No current auth/config is read.
	if model := core.S(core.Obj(meta["modelConfig"])["modelName"]); model != "" {
		identity.Providers = append(identity.Providers, core.ProviderIdentity{Model: model, Scope: "session", Evidence: "Cursor composerData.modelConfig.modelName (last selection)"})
	}
	batchMeta["identity"] = core.IdentityMeta(identity)
	batch := core.SourceBatch{Ref: ref, Meta: batchMeta}
	flush := func(final bool) error {
		if len(batch.Records) == 0 && !final {
			return nil
		}
		batch.Offset = core.SourceOffset{Position: core.BytePosition{Kind: "seq", Seq: int64(count)}, Records: count, PrefixHash: prefix, FormatVersion: version}
		if final {
			batch.Offset.State = core.JsonObject{"revision": revision}
		}
		err := yield(batch)
		batch = core.SourceBatch{Ref: ref}
		return err
	}
	put := func(b []byte) core.JsonObject {
		batch.Blobs = append(batch.Blobs, b)
		return core.JsonObject{"sha256": core.SHA256Hex(b), "bytes": len(b)}
	}
	currentUser := ""
	assetDir := ""
	if version == cliFormat && ref.Path != "" {
		assetDir = filepath.Join(filepath.Dir(ref.Path), "assets")
	} else if withUI && ref.Path != "" {
		wid := core.S(core.Obj(meta["workspaceIdentifier"])["id"])
		if wid != "" && filepath.Base(wid) == wid && wid != "." && wid != ".." && !strings.ContainsAny(wid, `/\`) {
			assetDir = filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(ref.Path))), "User", "workspaceStorage", wid, "koto", ref.Session)
		}
	}
	var attachments func([]core.Part) []core.Part
	attachments = func(parts []core.Part) []core.Part {
		out := []core.Part{}
		for _, p := range parts {
			if core.S(p["type"]) == "text" && assetDir != "" {
				var decoded []core.Part
				if indexOnly {
					decoded = shared.DecodeAttachmentTextIndex(core.S(p["text"]), assetDir)
				} else {
					decoded = shared.DecodeAttachmentText(core.S(p["text"]), assetDir, put)
				}
				for _, part := range decoded {
					if core.S(part["type"]) == "file" && strings.HasPrefix(core.S(part["mediaType"]), "image/") {
						part["type"] = "image"
					}
				}
				out = append(out, decoded...)
				continue
			}
			if core.S(p["type"]) == "tool_result" {
				p["parts"] = core.PartsJSON(attachments(core.Parts(p["parts"])))
			}
			out = append(out, p)
		}
		return out
	}
	for i, hash := range hashes {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := readBlob(ctx, tx, hash)
		if err != nil {
			return err
		}
		var role string
		var parts []core.Part
		if indexOnly {
			role, parts, err = indexModelMessage(data)
		} else {
			var msg core.JsonObject
			if err = json.Unmarshal(data, &msg); err == nil {
				role = core.S(msg["role"])
				parts, err = modelParts(msg["content"], put)
			}
		}
		if err != nil {
			return fmt.Errorf("Cursor message %d: %w", i+1, err)
		}
		if role != "user" && role != "assistant" && role != "tool" && role != "system" {
			return fmt.Errorf("unsupported Cursor role %q", role)
		}
		parts = attachments(parts)
		kind := "message"
		attrs := core.JsonObject{"modelMessageIndex": i}
		var ts *float64
		if i < firstUser {
			kind = "context"
			role = "system"
		} else if user, ok := users[i]; ok {
			if role != "user" {
				return fmt.Errorf("Cursor turn does not begin with a user message")
			}
			currentUser = user.text(2)
			attrs["nativeMessageID"] = currentUser
			if n := user.integer(25); n > 0 {
				x := float64(n)
				ts = &x
			}
			text := user.text(1)
			for _, p := range parts {
				if core.S(p["type"]) == "text" && core.S(p["text"]) == "<user_query>\n"+text+"\n</user_query>" {
					p["text"] = text
				}
			}
			if indexOnly {
				err = indexSelectedFiles(user, &parts)
			} else {
				err = addSelectedFiles(ctx, tx, user, &parts, put)
			}
			if err != nil {
				return err
			}
			if withUI {
				if err = addFileReferences(ctx, tx, ref.Session, currentUser, &parts); err != nil {
					return err
				}
			}
		}
		attrs["turnID"] = currentUser
		if role == "assistant" {
			attrs["stopReason"] = "in_progress"
			_, nextTurn := users[i+1]
			if nextTurn || i == len(hashes)-1 && core.S(meta["status"]) == "completed" && len(root.all(4)) == 0 {
				attrs["stopReason"] = "stop"
			}
		}
		id := fmt.Sprintf("model:%d:%s", i, hex.EncodeToString(hash))
		sha := core.SHA256Hex(data)
		prefix = core.ChainPrefix(prefix, sha)
		var recordRaw []byte
		if !indexOnly {
			recordRaw = data
		}
		batch.Records = append(batch.Records, core.BatchRecord{NativeID: id, Index: count, SHA256: sha, Bytes: len(data), Raw: recordRaw})
		count++
		batch.Drafts = append(batch.Drafts, core.DraftEvent{NativeID: id, Kind: kind, Actor: role, Parts: parts, Attrs: attrs, Time: ts})
		if len(batch.Records) >= 32 {
			if err = flush(false); err != nil {
				return err
			}
		}
	}
	return flush(true)
}
func decodeMedia(v any) ([]byte, string, error) {
	if s, ok := v.(string); ok {
		if strings.HasPrefix(s, "data:") {
			i := strings.Index(s, ",")
			if i < 0 || !strings.HasSuffix(s[:i], ";base64") {
				return nil, "", fmt.Errorf("unsupported Cursor attachment data URL")
			}
			b, err := base64.StdEncoding.DecodeString(s[i+1:])
			return b, "", err
		}
		if strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") {
			return nil, s, nil
		}
		b, err := base64.StdEncoding.DecodeString(s)
		return b, "", err
	}
	o := core.Obj(v)
	if core.S(o["__type"]) == "Uint8Array" {
		b, err := hex.DecodeString(core.S(o["hex"]))
		return b, "", err
	}
	return nil, "", fmt.Errorf("unsupported Cursor attachment encoding")
}
func modelParts(v any, put func([]byte) core.JsonObject) ([]core.Part, error) {
	if s, ok := v.(string); ok {
		return []core.Part{{"type": "text", "text": s}}, nil
	}
	a, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("unsupported Cursor message content")
	}
	out := []core.Part{}
	for _, v := range a {
		p := core.Obj(v)
		typ := core.S(p["type"])
		switch typ {
		case "text", "reasoning":
			out = append(out, core.Part{"type": typ, "text": core.S(p["text"])})
		case "image", "file":
			field := "image"
			if typ == "file" || p["image"] == nil && p["data"] != nil {
				field = "data"
			}
			b, uri, err := decodeMedia(p[field])
			if err != nil {
				return nil, err
			}
			m := core.Part{"type": typ, "mediaType": core.S(p["mimeType"]), "name": core.S(p["filename"])}
			if uri != "" {
				m["uri"] = uri
			} else {
				m["blob"] = put(b)
			}
			out = append(out, m)
		case "tool-call":
			if core.S(p["toolCallId"]) == "" || core.S(p["toolName"]) == "" {
				return nil, fmt.Errorf("Cursor tool call is missing its identity")
			}
			out = append(out, core.Part{"type": "tool_call", "callId": p["toolCallId"], "rawName": p["toolName"], "args": p["args"], "tool": Vocabulary{}.ToCanonical(core.S(p["toolName"]), p["args"])})
		case "tool-result":
			if core.S(p["toolCallId"]) == "" {
				return nil, fmt.Errorf("Cursor tool result is missing its identity")
			}
			result := p["result"]
			s, ok := result.(string)
			if !ok {
				b, err := json.Marshal(result)
				if err != nil {
					return nil, err
				}
				s = string(b)
			}
			ps := []core.Part{{"type": "text", "text": s}}
			if ex := p["experimental_content"]; ex != nil {
				more, err := modelParts(ex, put)
				if err != nil {
					return nil, err
				}
				if toolResultText(more) == s {
					// result is the provider's complete text fallback for the
					// structured blocks, not an additional result block.
					ps = more
				} else {
					ps = append(ps, more...)
				}
			}
			outcome := "success"
			if p["isError"] == true {
				outcome = "error"
			}
			out = append(out, core.Part{"type": "tool_result", "callId": p["toolCallId"], "outcome": outcome, "parts": core.PartsJSON(ps)})
		default:
			return nil, fmt.Errorf("unsupported Cursor content type %q", typ)
		}
	}
	return out, nil
}
func addSelectedFiles(ctx context.Context, q querier, user wireMessage, parts *[]core.Part, put func([]byte) core.JsonObject) error {
	selected, err := user.message(3)
	if err != nil {
		return err
	}
	if selected.has(23) {
		return fmt.Errorf("Cursor video attachments are not yet supported")
	}
	var expansions []string
	add := func(kind, name, media string, data []byte) {
		sha := core.SHA256Hex(data)
		for _, part := range *parts {
			if core.S(part["type"]) == kind && core.S(core.Obj(part["blob"])["sha256"]) == sha {
				if core.S(part["name"]) == "" {
					part["name"] = name
					return
				}
				if core.S(part["name"]) == name {
					return
				}
			}
		}
		*parts = append(*parts, core.Part{"type": kind, "name": name, "mediaType": media, "blob": put(data)})
	}
	for _, item := range selected.all(1) {
		f, err := parseWire(item)
		if err != nil {
			return err
		}
		var data []byte
		switch {
		case f.has(1):
			data, err = readBlob(ctx, q, f.bytes(1))
		case f.has(8):
			data = f.bytes(8)
		default:
			return fmt.Errorf("Cursor image attachment uses an unsupported reference")
		}
		if err != nil {
			return err
		}
		add("image", filepath.Base(f.text(3)), f.text(7), data)
	}
	for _, item := range selected.all(4) {
		f, err := parseWire(item)
		if err != nil {
			return err
		}
		add("file", filepath.Base(f.text(2)), "text/plain", f.bytes(1))
	}
	for _, item := range selected.all(25) {
		f, err := parseWire(item)
		if err != nil {
			return err
		}
		var data []byte
		switch {
		case f.has(1):
			data, err = readBlob(ctx, q, f.bytes(1))
		case f.has(8):
			data = f.bytes(8)
		default:
			return fmt.Errorf("Cursor document attachment uses an unsupported reference")
		}
		if err != nil {
			return err
		}
		add("file", f.text(3), f.text(4), data)
		if inlineFile(data, f.text(4)) {
			expansions = append(expansions, "[Attached file: "+f.text(3)+"]\n"+string(data))
		}
	}
	*parts = normalizeExpandedFiles(*parts, user.text(1), expansions)
	return nil
}

// Remove one exact generated expansion per selected file only when doing so
// reconstructs the separately stored original user text. This preserves plain
// text that merely resembles an attachment marker, even an identical block.
func normalizeExpandedFiles(parts []core.Part, authored string, expansions []string) []core.Part {
	remove := map[int]bool{}
	for _, expansion := range expansions {
		for i := len(parts) - 1; i >= 0; i-- {
			if !remove[i] && core.S(parts[i]["type"]) == "text" && core.S(parts[i]["text"]) == expansion {
				remove[i] = true
				break
			}
		}
	}
	if len(remove) == 0 {
		return parts
	}
	var text []string
	for i, part := range parts {
		if !remove[i] && core.S(part["type"]) == "text" {
			text = append(text, core.S(part["text"]))
		}
	}
	if strings.Join(text, "\n") != authored {
		return parts
	}
	out := make([]core.Part, 0, len(parts)-len(remove))
	for i, part := range parts {
		if !remove[i] {
			out = append(out, part)
		}
	}
	return out
}

// The IDE file picker can store references in UI context without embedding their
// bytes in the model checkpoint. Preserve those explicit selections as files.
func addFileReferences(ctx context.Context, q querier, session, user string, parts *[]core.Part) error {
	raw, err := readKV(ctx, q, "bubbleId:"+session+":"+user)
	if err != nil {
		return err
	}
	var bubble core.JsonObject
	if err = json.Unmarshal(raw, &bubble); err != nil {
		return err
	}
	for _, v := range core.Arr(core.Obj(bubble["context"])["fileSelections"]) {
		uri := core.Obj(core.Obj(v)["uri"])
		u := &url.URL{Scheme: core.S(uri["scheme"]), Host: core.S(uri["authority"]), Path: core.S(uri["path"])}
		// Keep explicit references portable; this reader does not open them.
		// A Windows host can read a macOS fixture (or vice versa), so local
		// filepath.IsAbs must only be used later when resolving actual bytes.
		if u.Scheme != "file" || !path.IsAbs(u.Path) || strings.ContainsAny(u.Host, `:/\`) || strings.ContainsRune(u.Path, '\x00') {
			return fmt.Errorf("unsupported Cursor selected-file URI")
		}
		media := mime.TypeByExtension(path.Ext(u.Path))
		if media == "" {
			media = "application/octet-stream"
		}
		*parts = append(*parts, core.Part{"type": "file", "name": path.Base(u.Path), "mediaType": strings.Split(media, ";")[0], "uri": u.String()})
	}
	return nil
}
