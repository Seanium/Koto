package cursor

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
)

// IDE delivery is rebuilt from the selected IR, including the model checkpoint.
// No source composer metadata or rootPromptMessagesJson is carried implicitly.
type ideDelivery struct {
	Metadata core.JsonObject   `json:"metadata"`
	Header   core.JsonObject   `json:"header"`
	Bubbles  []core.JsonObject `json:"bubbles"`
	Blobs    map[string][]byte `json:"blobs"`
	Assets   map[string][]byte `json:"assets"`
}

func (c *connector) NewSessionID(key string) string { return core.UUID5("cursor:" + key) }
func (c *connector) ResumeCommand() string          { return "" } // IDE sessions are not CLI store.db sessions.
func wireText(n int, s string) wireField            { return wireBytes(n, []byte(s)) }
func wireInt(n int, v int64) wireField              { return wireField{number: n, kind: 0, integer: uint64(v)} }
func (d *ideDelivery) put(b []byte) []byte {
	// A verified zero-byte attachment is an empty BLOB, not SQL NULL.
	// Hub storage may return nil for an existing empty content hash.
	if b == nil {
		b = []byte{}
	}
	h := core.SHA256Hex(b)
	d.Blobs[h] = b
	v, _ := hex.DecodeString(h)
	return v
}
func (d *ideDelivery) putJSON(v any) ([]byte, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	return d.put(b), nil
}
func emptyContext() core.JsonObject {
	c := core.JsonObject{"mentions": core.JsonObject{}}
	for _, k := range []string{"composers", "selectedCommits", "selectedPullRequests", "selectedImages", "selectedDocuments", "selectedVideos", "folderSelections", "fileSelections", "terminalFiles", "selections", "terminalSelections", "selectedDocs", "externalLinks", "cursorRules", "cursorCommands", "gitPRDiffSelections", "subagentSelections", "browserSelections", "extraContext"} {
		c[k] = []any{}
	}
	return c
}
func bubble(id, role, text string, ts int64) core.JsonObject {
	typ := 2
	if role == "user" {
		typ = 1
	}
	b := core.JsonObject{"_v": 3, "type": typ, "bubbleId": id, "text": text, "createdAt": time.UnixMilli(ts).UTC().Format(time.RFC3339Nano), "conversationState": "~", "tokenCount": core.JsonObject{"inputTokens": 0, "outputTokens": 0}, "isAgentic": true, "unifiedMode": 2, "context": emptyContext()}
	for _, k := range []string{"approximateLintErrors", "lints", "codebaseContextChunks", "commits", "pullRequests", "attachedCodeChunks", "assistantSuggestedDiffs", "gitDiffs", "interpreterResults", "images", "attachedFolders", "attachedFoldersNew", "userResponsesToSuggestedCodeBlocks", "suggestedCodeBlocks", "diffsForCompressingFiles", "relevantFiles", "toolResults", "notepads", "capabilities", "multiFileLinterErrors", "diffHistories", "recentLocationsHistory", "recentlyViewedFiles", "fileDiffTrajectories", "docsReferences", "webReferences", "aiWebSearchResults", "attachedFoldersListDirResults", "humanChanges", "summarizedComposers", "cursorRules", "cursorCommands", "pastChats", "contextPieces", "editTrailContexts", "allThinkingBlocks", "diffsSinceLastApply", "deletedFiles", "supportedTools", "attachedFileCodeChunksMetadataOnly", "consoleLogs", "uiElementPicked", "knowledgeItems", "documentationSelections", "externalLinks", "projectLayouts", "capabilityContexts", "todos", "mcpDescriptors", "workspaceUris", "codeBlocks"} {
		b[k] = []any{}
	}
	return b
}
func (c *connector) Plan(in engine.PlanInput) (*engine.DeliveryPlan, error) {
	if in.Projection == nil || in.Target.Mode != "create" || !sessionID.MatchString(in.Target.Session) {
		return nil, fmt.Errorf("invalid Cursor create request")
	}
	if err := checkTargetProfile(in.Target, c.id, c.root); err != nil {
		return nil, err
	}
	target := in.Target
	if target.Cwd == "" {
		target.Cwd = in.Projection.Hints.Cwd
	}
	if target.Cwd == "" && c.assetBase != "" {
		return nil, fmt.Errorf("Cursor requires a project directory")
	}
	var err error
	if target.Cwd != "" {
		target.Cwd, err = filepath.Abs(target.Cwd)
		if err != nil {
			return nil, err
		}
	}
	target.Path = c.db
	if target.Title == "" {
		target.Title = "Copied conversation"
	}
	d := ideDelivery{Blobs: map[string][]byte{}, Assets: map[string][]byte{}, Bubbles: []core.JsonObject{}}
	now := time.Now().UnixMilli()
	// Ordinary Cursor's "Start from scratch" conversations use this native
	// empty-window identity, with no folder URI or invented project directory.
	workspaceID := "empty-window"
	workspace := core.JsonObject{"id": workspaceID}
	if target.Cwd != "" {
		workspaceURI := shared.FileURL(target.Cwd)
		workspaceID = core.SHA256Hex(workspaceURI.String())[:32]
		if c.assetBase == "" {
			workspaceID, err = folderWorkspaceID(target.Cwd)
			if err != nil {
				return nil, fmt.Errorf("Cursor target workspace: %w", err)
			}
		}
		workspace = core.JsonObject{"id": workspaceID, "uri": core.JsonObject{"$mid": 1, "scheme": "file", "authority": workspaceURI.Host, "fsPath": target.Cwd, "path": workspaceURI.Path, "external": workspaceURI.String()}}
	}
	// Reuse the target's existing workspace identity, never a source agent's ID.
	db, e := c.open(context.Background())
	if e == nil {
		defer db.Close()
		rows, e := db.Query("SELECT value FROM composerHeaders ORDER BY lastUpdatedAt DESC")
		if e == nil {
			for rows.Next() {
				var raw []byte
				if rows.Scan(&raw) != nil {
					break
				}
				var h core.JsonObject
				if json.Unmarshal(raw, &h) != nil {
					continue
				}
				w := core.Obj(h["workspaceIdentifier"])
				id := core.S(w["id"])
				if target.Cwd != "" && core.S(core.Obj(w["uri"])["fsPath"]) == target.Cwd && id != "" && filepath.Base(id) == id && id != "." && id != ".." && !strings.ContainsAny(id, `/\`) {
					workspace = w
					workspaceID = id
					break
				}
			}
			rows.Close()
		}
	}
	if c.assetBase == "" {
		assetDir := filepath.Join(c.root, "User", "workspaceStorage", workspaceID, "koto", target.Session)
		var attachments []any
		in, attachments, err = c.prepareAttachments(in, assetDir)
		if err != nil {
			return nil, err
		}
		for _, v := range attachments {
			a := core.Obj(v)
			d.Assets[core.S(a["path"])] = a["data"].([]byte)
		}
	}
	plan := &engine.DeliveryPlan{Sink: c.id, Harness: "cursor", Thread: in.Thread, Head: in.Head, Level: in.Level, Mode: "create", Target: target, Losses: append([]core.Loss{}, in.Projection.Losses...), Notes: append([]string{}, in.Projection.Notes...)}
	// The runtime initializes its start timestamp and IANA timezone together.
	// Supplying only field 26 makes getOrInitializeConversationStartedDate fail
	// before a model request, even though the protobuf itself decodes correctly.
	root := wireMessage{wireInt(10, 1)}
	if target.Cwd != "" {
		root = append(root, wireText(9, shared.FileURL(target.Cwd).String()), wireText(22, "cli"))
	} else {
		// Let the native empty window initialize its execution directory when
		// continued. It is separate from a project's storage identity.
		root = append(root, wireText(22, "ide"))
	}
	var turn wireMessage
	uid := ""
	stepIndex := 0
	calls := map[string]*importedTool{}
	finish := func() {
		if uid != "" {
			root = append(root, wireBytes(8, d.put(wireMessage{wireBytes(1, turn.encode())}.encode())), wireText(38, uid))
		}
	}
	start := func(text, key string, ts int64, selected wireMessage, context core.JsonObject) {
		finish()
		uid = core.UUID5(target.Session + ":" + key)
		stepIndex = 0
		rich, _ := json.Marshal(core.JsonObject{"type": "doc", "content": []any{core.JsonObject{"type": "paragraph", "content": []any{core.JsonObject{"type": "text", "text": text}}}}})
		user := wireMessage{wireText(1, text), wireText(2, uid), wireBytes(3, selected.encode()), wireInt(4, 1), wireText(8, string(rich)), wireBytes(10, d.put(root.encode())), wireText(17, uid), wireInt(25, ts), wireInt(26, ts)}
		turn = wireMessage{wireBytes(1, d.put(user.encode())), wireText(10, uid), wireInt(5, 0)}
		b := bubble(uid, "user", text, ts)
		b["context"] = context
		b["richText"] = string(rich)
		d.Bubbles = append(d.Bubbles, b)
	}
	for i, it := range in.Projection.Items {
		if it.Event == nil || it.Event.Kind == "context" {
			return nil, fmt.Errorf("system context cannot be copied")
		}
		role := it.Role
		if role != "user" && role != "assistant" && role != "tool" {
			return nil, fmt.Errorf("unsupported Cursor role %q", role)
		}
		ts := now + int64(i)
		if it.Event.Time != nil {
			ts = int64(*it.Event.Time)
		}
		if role != "user" && uid == "" {
			start("[Imported conversation excerpt]", "excerpt", ts, nil, emptyContext())
			h, e := d.putJSON(core.JsonObject{"role": "user", "content": []any{core.JsonObject{"type": "text", "text": "[Imported conversation excerpt]"}}})
			if e != nil {
				return nil, e
			}
			root = append(root, wireBytes(1, h))
			plan.Losses = append(plan.Losses, core.Loss{Event: it.Event.ID, Field: "role", Kind: "approximated", Note: "Cursor requires a user turn before an assistant-only excerpt"})
		}
		model := []any{}
		text := []string{}
		selected := wireMessage{}
		bc := emptyContext()
		for n, p := range it.Parts {
			switch core.S(p["type"]) {
			case "text", "reasoning":
				typ := core.S(p["type"])
				if typ == "reasoning" && role != "assistant" {
					return nil, fmt.Errorf("reasoning outside Cursor assistant message")
				}
				native := core.JsonObject{"type": typ, "text": core.S(p["text"])}
				if sealed := core.Obj(p["sealed"]); typ == "reasoning" && len(sealed) != 0 {
					if core.S(sealed["by"]) != modelAPI {
						return nil, fmt.Errorf("Cursor cannot replay reasoning from another wire API")
					}
					raw, _, err := c.material(in, core.Part{"blob": sealed["blob"]})
					if err != nil {
						return nil, err
					}
					native = nil
					if err = json.Unmarshal(raw, &native); err != nil {
						return nil, err
					}
					switch core.S(native["type"]) {
					case "redacted-reasoning":
						if _, ok := native["data"].(string); !ok {
							return nil, fmt.Errorf("invalid Cursor redacted reasoning")
						}
					case "reasoning":
						if core.S(native["text"]) != core.S(p["text"]) {
							return nil, fmt.Errorf("Cursor reasoning text differs from its native record")
						}
					default:
						return nil, fmt.Errorf("invalid Cursor reasoning record")
					}
				}
				model = append(model, native)
				if core.S(native["type"]) == "redacted-reasoning" {
					continue
				}
				text = append(text, core.S(p["text"]))
				if role == "assistant" {
					which := 1
					fields := wireMessage{wireText(1, core.S(p["text"])), wireInt(2, ts), wireInt(3, ts)}
					if typ == "reasoning" {
						which = 3
						fields = wireMessage{wireText(1, core.S(p["text"])), wireInt(3, ts), wireInt(4, ts)}
					}
					turn = append(turn, wireBytes(2, d.put(wireMessage{wireBytes(which, fields.encode())}.encode())))
					b := bubble(fmt.Sprintf("%s/s:%d", uid, stepIndex), "assistant", core.S(p["text"]), ts)
					if typ == "reasoning" {
						b["thinking"] = core.JsonObject{"text": p["text"]}
						b["text"] = ""
					}
					stepIndex++
					d.Bubbles = append(d.Bubbles, b)
				}

			case "tool_call":
				if role != "assistant" || uid == "" {
					return nil, fmt.Errorf("Cursor tool call outside assistant turn")
				}
				callID, name := core.S(p["callId"]), core.S(p["rawName"])
				if callID == "" || name == "" || calls[callID] != nil {
					return nil, fmt.Errorf("invalid or duplicate Cursor tool identity")
				}
				args := core.Obj(p["args"])
				if args == nil {
					if p["args"] != nil {
						return nil, fmt.Errorf("Cursor tool arguments must be an object")
					}
					args = core.JsonObject{}
				}
				nativeID := nativeToolID(target.Session, callID)
				calls[callID] = &importedTool{name: name, id: nativeID, args: args, field: len(turn), bubble: len(d.Bubbles), ts: ts}
				turn = append(turn, wireBytes(2, nil))
				stepIndex++
				b := bubble(uid+"/t:"+callID, "assistant", "", ts)
				d.Bubbles = append(d.Bubbles, b)
				model = append(model, core.JsonObject{"type": "tool-call", "toolName": name, "toolCallId": nativeID, "args": args})
			case "tool_result":
				if role != "tool" {
					return nil, fmt.Errorf("Cursor tool result outside tool message")
				}
				callID := core.S(p["callId"])
				call := calls[callID]
				if call == nil || call.done {
					return nil, fmt.Errorf("Cursor tool result has no unique selected call")
				}
				resultParts := core.Parts(p["parts"])
				tool, e := toolWire(call, resultParts, core.S(p["outcome"]), func(p core.Part) ([]byte, string, error) { return c.material(in, p) })
				if e != nil {
					return nil, e
				}
				turn[call.field] = wireBytes(2, d.put(wireMessage{wireBytes(2, tool.encode())}.encode()))
				call.done = true
				b := d.Bubbles[call.bubble]
				argsJSON, _ := json.Marshal(call.args)
				b["capabilityType"] = 15
				b["toolFormerData"] = core.JsonObject{"toolCallId": call.id, "toolIndex": call.field, "modelCallId": call.id, "status": "completed", "name": call.name, "rawArgs": string(argsJSON), "tool": 49, "params": "{}", "result": "{}", "toolCallBinary": base64.StdEncoding.EncodeToString(tool.encode())}
				extra := []any{}
				for _, rp := range resultParts {
					switch core.S(rp["type"]) {
					case "text":
						extra = append(extra, core.JsonObject{"type": "text", "text": core.S(rp["text"])})
					case "image":
						raw, media, e := c.material(in, rp)
						if e != nil {
							return nil, e
						}
						extra = append(extra, core.JsonObject{"type": "image", "mimeType": media, "data": base64.StdEncoding.EncodeToString(raw)})
					}
				}
				model = append(model, core.JsonObject{"type": "tool-result", "toolCallId": call.id, "toolName": call.name, "result": toolResultText(resultParts), "experimental_content": extra, "isError": core.S(p["outcome"]) == "error"})
			case "image", "file":
				raw, media, e := c.material(in, p)
				if e != nil {
					return nil, e
				}
				name := shared.AttachmentName(core.S(p["name"]))
				asset := filepath.Join(c.root, "User", "workspaceStorage", workspaceID, "koto", target.Session, core.SHA256Hex(raw), name)
				if c.assetBase != "" {
					asset = filepath.Join(c.assetBase, "assets", core.SHA256Hex(raw), name)
				}
				d.Assets[asset] = raw
				hash := d.put(raw)
				aid := core.UUID5(target.Session + ":" + it.Key + fmt.Sprint(n))
				typ := core.S(p["type"])
				if typ == "image" {
					model = append(model, core.JsonObject{"type": "image", "mimeType": media, "image": core.JsonObject{"__type": "Uint8Array", "hex": hex.EncodeToString(raw)}})
					img := wireMessage{wireBytes(1, hash), wireText(2, aid), wireText(3, asset), wireText(7, media)}
					if cfg, _, e := image.DecodeConfig(bytes.NewReader(raw)); e == nil {
						img = append(img, wireBytes(4, wireMessage{wireInt(1, int64(cfg.Width)), wireInt(2, int64(cfg.Height))}.encode()))
					}
					selected = append(selected, wireBytes(1, img.encode()))
					bc["selectedImages"] = append(core.Arr(bc["selectedImages"]), core.JsonObject{"uuid": aid, "path": asset, "mimeType": media})
				} else {
					if inlineFile(raw, media) {
						// Native file bytes remain in selectedDocuments. Text files are
						// replayed inline, as Cursor does for workspace file context;
						// its provider adapter only accepts PDF as a file content part.
						model = append(model, core.JsonObject{"type": "text", "text": "[Attached file: " + name + "]\n" + string(raw)})
						if len(raw) == 0 {
							// Empty inline context has no observable bytes. Keep
							// its native selected-document blob and give the
							// model a checked target-local file link to inspect.
							u := strings.NewReplacer("(", "%28", ")", "%29").Replace(shared.FileURL(asset).String())
							label := strings.NewReplacer("[", "（", "]", "）").Replace(name)
							model = append(model, core.JsonObject{"type": "text", "text": "[附件：" + label + "](" + u + ")"})
						}
					} else {
						model = append(model, core.JsonObject{"type": "file", "mimeType": media, "filename": name, "data": core.JsonObject{"__type": "Uint8Array", "hex": hex.EncodeToString(raw)}})
					}
					selected = append(selected, wireBytes(25, wireMessage{wireBytes(1, hash), wireText(2, aid), wireText(3, name), wireText(4, media), wireText(7, asset)}.encode()))
					bc["selectedDocuments"] = append(core.Arr(bc["selectedDocuments"]), core.JsonObject{"uuid": aid, "path": asset, "filename": name, "mimeType": media})
				}
			default:
				return nil, fmt.Errorf("Cursor native writer has not validated content type %q", core.S(p["type"]))
			}
		}
		if role == "user" {
			for _, call := range calls {
				if !call.done {
					return nil, fmt.Errorf("Cursor selected tool call has no result before next turn")
				}
			}
			displayText := strings.Join(text, "\n")
			if original, ok := it.Event.Attrs["displayText"].(string); ok && it.Event.Actor == "user" && it.Synthetic == "" {
				displayText = original
			}
			start(displayText, it.Key, ts, selected, bc)
		}
		msg := core.JsonObject{"role": role, "content": model}
		if role == "assistant" {
			msg["id"] = "msg-" + core.UUID5(target.Session+it.Key)
		}
		h, e := d.putJSON(msg)
		if e != nil {
			return nil, e
		}
		root = append(root, wireBytes(1, h))

		plan.Records = append(plan.Records, engine.PlanRecord{NativeID: fmt.Sprintf("model:%d:%s", i, hex.EncodeToString(h)), EventID: it.Event.ID, SHA256: hex.EncodeToString(h)})
	}
	for _, call := range calls {
		if !call.done {
			return nil, fmt.Errorf("Cursor selected tool call has no result")
		}
	}
	if uid == "" {
		return nil, fmt.Errorf("no content selected")
	}
	finish()
	headers := []any{}
	for _, b := range d.Bubbles {
		headers = append(headers, core.JsonObject{"bubbleId": b["bubbleId"], "type": b["type"], "createdAt": b["createdAt"], "grouping": core.JsonObject{"isRenderable": true, "hasText": true, "toolDisplayComputed": true}})
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return nil, err
	}
	d.Metadata = core.JsonObject{"_v": 18, "composerId": target.Session, "name": target.Title, "status": "completed", "createdAt": now, "lastUpdatedAt": now, "conversationCheckpointLastUpdatedAt": now, "hasLoaded": true, "text": "", "richText": "", "fullConversationHeadersOnly": headers, "conversationMap": core.JsonObject{}, "context": emptyContext(), "generatingBubbleIds": []any{}, "capabilities": []any{core.JsonObject{"type": 15, "data": core.JsonObject{"bubbleDataMap": "{}"}}}, "codeBlockData": core.JsonObject{}, "originalFileStates": core.JsonObject{}, "newlyCreatedFiles": []any{}, "newlyCreatedFolders": []any{}, "unifiedMode": "agent", "forceMode": "edit", "subComposerIds": []any{}, "subagentComposerIds": []any{}, "capabilityContexts": []any{}, "todos": []any{}, "isDraft": false, "isNAL": true, "isAgentic": true, "workspaceIdentifier": workspace, "blobEncryptionKey": base64.StdEncoding.EncodeToString(key), "conversationState": "~" + base64.StdEncoding.EncodeToString(root.encode())}
	d.Header = core.JsonObject{"type": "head", "composerId": target.Session, "name": target.Title, "createdAt": now, "lastUpdatedAt": now, "conversationCheckpointLastUpdatedAt": now, "workspaceIdentifier": workspace, "isDraft": false, "unifiedMode": "agent", "forceMode": "edit", "agentLocation": core.JsonObject{"type": "local", "environment": workspace, "status": "active"}, "agentLocationHistory": []any{}}
	b, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	plan.Content = string(b)
	plan.ID = "plan:" + core.SHA256Hex(plan.Content)
	return plan, nil
}

func inlineFile(raw []byte, media string) bool {
	return utf8.Valid(raw) && (strings.HasPrefix(media, "text/") || media == "application/json" || media == "application/xml" || media == "application/javascript" || strings.HasSuffix(media, "+json") || strings.HasSuffix(media, "+xml"))
}

func (c *connector) material(in engine.PlanInput, p core.Part) ([]byte, string, error) {
	media := core.S(p["mediaType"])
	finish := func(b []byte, media string) ([]byte, string, error) {
		if len(b) > maxBlobBytes {
			return nil, "", fmt.Errorf("Cursor attachment exceeds %d bytes", maxBlobBytes)
		}
		if media == "" {
			media = http.DetectContentType(b)
		}
		return b, media, nil
	}
	if sha := core.S(core.Obj(p["blob"])["sha256"]); sha != "" {
		if in.Hub == nil {
			return nil, "", fmt.Errorf("Cursor attachment storage unavailable")
		}
		b, ok := in.Hub.Blob(sha)
		if !ok || core.SHA256Hex(b) != sha {
			return nil, "", fmt.Errorf("Cursor attachment integrity mismatch")
		}
		return finish(b, media)
	}
	uri := core.S(p["uri"])
	if strings.HasPrefix(uri, "data:") {
		b, _, e := decodeMedia(uri)
		if e != nil {
			return nil, "", e
		}
		if media == "" {
			header, _, _ := strings.Cut(strings.TrimPrefix(uri, "data:"), ",")
			media, _, e = mime.ParseMediaType(strings.TrimSuffix(header, ";base64"))
			if e != nil {
				return nil, "", fmt.Errorf("invalid Cursor attachment media type: %w", e)
			}
		}
		return finish(b, media)
	}
	return nil, "", fmt.Errorf("Cursor attachment must have local verified bytes")
}
func (c *connector) Apply(ctx context.Context, p *engine.DeliveryPlan) (core.JsonObject, error) {
	if p == nil || p.Mode != "create" || p.Target.Mode != "create" || p.Harness != "cursor" || p.Sink != c.id || !sessionID.MatchString(p.Target.Session) || p.Target.Path != c.db || p.ID != "plan:"+core.SHA256Hex(p.Content) {
		return nil, fmt.Errorf("invalid Cursor delivery")
	}
	if e := checkTargetProfile(p.Target, c.id, c.root); e != nil {
		return nil, e
	}
	var d ideDelivery
	if e := json.Unmarshal([]byte(p.Content), &d); e != nil {
		return nil, e
	}
	if core.S(d.Metadata["composerId"]) != p.Target.Session || core.S(d.Header["composerId"]) != p.Target.Session || len(d.Bubbles) == 0 {
		return nil, fmt.Errorf("Cursor delivery identity mismatch")
	}
	for sha, b := range d.Blobs {
		if len(b) > maxBlobBytes || core.SHA256Hex(b) != sha {
			return nil, fmt.Errorf("invalid Cursor delivery blob")
		}
	}
	if _, e := os.Stat(c.db); e != nil {
		return nil, fmt.Errorf("open Cursor once before importing: %w", e)
	}
	db, e := sql.Open("sqlite", shared.FileURL(c.db).String())
	if e != nil {
		return nil, e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, e = db.ExecContext(ctx, "PRAGMA busy_timeout=5000"); e != nil {
		return nil, e
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	meta, e := json.Marshal(d.Metadata)
	if e != nil {
		return nil, e
	}
	var existing []byte
	e = tx.QueryRowContext(ctx, "SELECT value FROM cursorDiskKV WHERE key=?", "composerData:"+p.Target.Session).Scan(&existing)
	if e == nil {
		if string(existing) != string(meta) {
			return nil, fmt.Errorf("Cursor session already exists with different content")
		}
		if e = c.verifyDelivery(ctx, tx, p, d); e != nil {
			return nil, e
		}
		return core.JsonObject{"session": p.Target.Session, "path": c.db, "noop": true}, nil
	}
	if e != sql.ErrNoRows {
		return nil, e
	}
	files, e := os.OpenRoot(c.root)
	if e != nil {
		return nil, e
	}
	defer files.Close()
	created := []string{}
	committed := false
	defer func() {
		if !committed {
			for _, f := range created {
				files.Remove(f)
			}
		}
	}()
	for file, b := range d.Assets {
		base := c.root
		rel, er := filepath.Rel(base, file)
		if er != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || !strings.Contains(rel, string(filepath.Separator)+"koto"+string(filepath.Separator)+p.Target.Session+string(filepath.Separator)) {
			return nil, fmt.Errorf("Cursor attachment escapes target session")
		}
		if er = files.MkdirAll(filepath.Dir(rel), 0700); er != nil {
			return nil, er
		}
		f, er := files.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(er) {
			old, ee := files.ReadFile(rel)
			if ee != nil || core.SHA256Hex(old) != core.SHA256Hex(b) {
				return nil, fmt.Errorf("Cursor attachment path conflicts")
			}
			continue
		}
		if er != nil {
			return nil, er
		}
		created = append(created, rel)
		_, er = f.Write(b)
		ce := f.Close()
		if er != nil {
			return nil, er
		}
		if ce != nil {
			return nil, ce
		}
	}
	for sha, b := range d.Blobs {
		if b == nil || core.SHA256Hex(b) != sha {
			return nil, fmt.Errorf("Cursor delivery blob hash mismatch")
		}
		var old []byte
		e = tx.QueryRowContext(ctx, "SELECT value FROM cursorDiskKV WHERE key=?", "agentKv:blob:"+sha).Scan(&old)
		if e == nil {
			if core.SHA256Hex(old) != sha {
				return nil, fmt.Errorf("Cursor existing blob integrity mismatch")
			}
			continue
		}
		if e != sql.ErrNoRows {
			return nil, e
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO cursorDiskKV(key,value)VALUES(?,?)", "agentKv:blob:"+sha, b); e != nil {
			return nil, e
		}
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO cursorDiskKV(key,value)VALUES(?,?)", "composerData:"+p.Target.Session, string(meta)); e != nil {
		return nil, e
	}
	for _, b := range d.Bubbles {
		raw, er := json.Marshal(b)
		if er != nil {
			return nil, er
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO cursorDiskKV(key,value)VALUES(?,?)", "bubbleId:"+p.Target.Session+":"+core.S(b["bubbleId"]), string(raw)); e != nil {
			return nil, e
		}
	}
	header, e := json.Marshal(d.Header)
	if e != nil {
		return nil, e
	}
	now, _ := core.Num(d.Header["createdAt"])
	if _, e = tx.ExecContext(ctx, `INSERT INTO composerHeaders(composerId,workspaceId,createdAt,lastUpdatedAt,isArchived,isSubagent,recency,checkpointAt,subagentTypeName,value) VALUES(?,?,?,?,0,0,?,?,?,?)`, p.Target.Session, core.S(core.Obj(d.Header["workspaceIdentifier"])["id"]), int64(now), int64(now), int64(now), int64(now), "", string(header)); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	committed = true
	return core.JsonObject{"session": p.Target.Session, "path": c.db}, nil
}

func checkTargetProfile(target engine.DeliveryTarget, id, root string) error {
	if target.Profile != "" && target.Profile != id || target.Root != "" && filepath.Clean(target.Root) != filepath.Clean(root) {
		return fmt.Errorf("Cursor target storage profile changed")
	}
	return nil
}

func (c *connector) verifyDelivery(ctx context.Context, tx *sql.Tx, p *engine.DeliveryPlan, d ideDelivery) error {
	conflict := fmt.Errorf("Cursor existing session content or attachments changed; refusing to overwrite")
	wantHeader, e := json.Marshal(d.Header)
	if e != nil {
		return e
	}
	var gotHeader []byte
	var archived int
	if e = tx.QueryRowContext(ctx, "SELECT value,coalesce(isArchived,0) FROM composerHeaders WHERE composerId=?", p.Target.Session).Scan(&gotHeader, &archived); e != nil || archived != 0 || !bytes.Equal(wantHeader, gotHeader) {
		return conflict
	}
	var count int
	if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM cursorDiskKV WHERE key LIKE ?", "bubbleId:"+p.Target.Session+":%").Scan(&count); e != nil {
		return e
	}
	if count != len(d.Bubbles) {
		return conflict
	}
	for _, bubble := range d.Bubbles {
		want, e := json.Marshal(bubble)
		if e != nil {
			return e
		}
		got, e := readKV(ctx, tx, "bubbleId:"+p.Target.Session+":"+core.S(bubble["bubbleId"]))
		if e != nil || !bytes.Equal(want, got) {
			return conflict
		}
	}
	for sha, b := range d.Blobs {
		h, e := hex.DecodeString(sha)
		if e != nil {
			return e
		}
		got, e := readBlob(ctx, tx, h)
		if e != nil || !bytes.Equal(got, b) {
			return conflict
		}
	}
	root, e := os.OpenRoot(c.root)
	if e != nil {
		return e
	}
	defer root.Close()
	for file, want := range d.Assets {
		rel, e := filepath.Rel(c.root, file)
		if e != nil {
			return e
		}
		got, e := root.ReadFile(rel)
		if e != nil || !bytes.Equal(got, want) {
			return conflict
		}
	}
	return nil
}
