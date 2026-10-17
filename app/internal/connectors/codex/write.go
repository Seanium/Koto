package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
)

func (c *codexConn) NewSessionID(key string) string { return core.UUID5("codex\x00" + key) }
func (c *codexConn) ResumeCommand() string          { return "codex resume {id}" }
func (c *codexConn) ResumeID(session string) string { return ThreadOf(session) }
func isDir(p string) bool                           { st, e := os.Stat(p); return e == nil && st.IsDir() }

// Codex archives only the rollout; its adjacent assets stay at their original path.
func assetDirectory(file string) string {
	file = strings.TrimSuffix(file, ".zst")
	name := filepath.Base(file)
	if filepath.Base(filepath.Dir(file)) == "archived_sessions" && rolloutRe.MatchString(name) {
		stamp := strings.TrimPrefix(name, "rollout-")
		file = filepath.Join(filepath.Dir(filepath.Dir(file)), "sessions", stamp[:4], stamp[5:7], stamp[8:10], name)
	}
	return strings.TrimSuffix(file, ".jsonl") + ".assets"
}

// A native rollout retains multimodal response items AND the display events. The official
// external-agent importer loses image/audio/tool blocks; inject_items omits visible UI turns.
// Only new rollouts are published; thread/read validates the new session without loading its runtime or tools.
func (c *codexConn) Plan(in engine.PlanInput) (*engine.DeliveryPlan, error) {
	t := in.Target
	cwd := t.Cwd
	if cwd == "" {
		cwd = in.Projection.Hints.Cwd
	}
	if cwd == "" {
		cwd, _ = os.UserHomeDir()
	}
	now := time.Now().UTC()
	file := filepath.Join(c.home, "sessions", now.Format("2006/01/02"), "rollout-"+now.Format("2006-01-02T15-04-05")+"-"+t.Session+".jsonl")
	in, attachments := shared.PrepareAttachments(in, strings.TrimSuffix(file, ".jsonl")+".assets", func(p core.Part, role string) bool {
		return role != "assistant" && strings.HasPrefix(core.S(p["mediaType"]), "audio/")
	})
	pr := in.Projection
	p := &engine.DeliveryPlan{Sink: c.id, Harness: "codex", Thread: in.Thread, Head: in.Head, Level: in.Level, Mode: "create", Target: t, Losses: pr.Losses, Notes: append([]string{}, pr.Notes...), Extra: core.JsonObject{"attachments": attachments, "cwd": cwd}}
	p.Target.Path = file
	if !isDir(cwd) {
		p.Mode = "reject"
		p.Notes = append(p.Notes, fmt.Sprintf("cwd %s does not exist; choose an existing project folder", cwd))
	}
	var lines []shared.NativeLine
	add := func(typ string, payload core.JsonObject, event string, ms float64) {
		if ms <= 0 {
			ms = float64(now.UnixMilli())
		}
		ordinal := len(lines)
		line := core.ToJSON(core.JsonObject{"timestamp": shared.ISO(ms), "ordinal": ordinal, "type": typ, "payload": payload})
		lines = append(lines, shared.NativeLine{NativeID: fmt.Sprintf("o%d", ordinal), EventID: event, Line: line})
	}
	add("session_meta", core.JsonObject{"id": t.Session, "timestamp": now.Format(time.RFC3339Nano), "cwd": cwd, "originator": "koto", "cli_version": "0.0.0", "source": "cli"}, in.Head, 0)
	contents := func(ps []core.Part, role string) []any {
		out := []any{}
		for _, v := range ps {
			switch core.S(v["type"]) {
			case "text":
				typ := "input_text"
				if role == "assistant" {
					typ = "output_text"
				}
				out = append(out, core.JsonObject{"type": typ, "text": v["text"]})
			case "image":
				u := core.S(v["uri"])
				if u == "" {
					b, _ := shared.BlobBase64(in.Hub, core.S(core.Obj(v["blob"])["sha256"]))
					u = "data:" + core.S(v["mediaType"]) + ";base64," + b
				}
				out = append(out, core.JsonObject{"type": "input_image", "image_url": u})
			case "file":
				u := core.S(v["uri"])
				if u == "" {
					b, _ := shared.BlobBase64(in.Hub, core.S(core.Obj(v["blob"])["sha256"]))
					u = "data:" + core.S(v["mediaType"]) + ";base64," + b
				}
				out = append(out, core.JsonObject{"type": "input_audio", "audio_url": u})
			}
		}
		return out
	}
	turn := ""
	turnN := 0
	closeTurn := func() {
		if turn != "" {
			add("event_msg", core.JsonObject{"type": "task_complete", "turn_id": turn}, in.Head, 0)
		}
	}
	for _, it := range pr.Items {
		ms := it.Event.TimeMs()
		if it.Role == "user" || turn == "" {
			closeTurn()
			turnN++
			turn = fmt.Sprintf("copy-%d", turnN)
			add("event_msg", core.JsonObject{"type": "task_started", "turn_id": turn}, it.Event.ID, ms)
		}
		cs := contents(it.Parts, it.Role)
		if len(cs) > 0 {
			text := []string{}
			images := []string{}
			audio := []string{}
			for _, x := range cs {
				v := core.Obj(x)
				if v["text"] != nil {
					text = append(text, core.S(v["text"]))
				}
				if v["image_url"] != nil {
					images = append(images, core.S(v["image_url"]))
				}
				if v["audio_url"] != nil {
					audio = append(audio, core.S(v["audio_url"]))
				}
			}
			if it.Role == "assistant" {
				add("event_msg", core.JsonObject{"type": "agent_message", "message": strings.Join(text, "\n")}, it.Event.ID, ms)
			} else {
				add("event_msg", core.JsonObject{"type": "user_message", "message": strings.Join(text, "\n"), "images": images, "audio": audio, "local_images": []string{}, "local_audio": []string{}, "text_elements": []any{}}, it.Event.ID, ms)
			}
			role := it.Role
			if role == "tool" {
				role = "user"
			}
			add("response_item", core.JsonObject{"type": "message", "role": role, "content": cs}, it.Event.ID, ms)
		}
		for _, v := range it.Parts {
			switch core.S(v["type"]) {
			case "tool_call":
				args := v["args"]
				a, ok := args.(string)
				if !ok {
					a = core.Stringify(args)
				}
				add("response_item", core.JsonObject{"type": "function_call", "call_id": v["callId"], "name": v["rawName"], "arguments": a}, it.Event.ID, ms)
			case "tool_result":
				add("response_item", core.JsonObject{"type": "function_call_output", "call_id": v["callId"], "output": contents(core.Parts(v["parts"]), "tool")}, it.Event.ID, ms)
			case "reasoning":
				summary := []any{}
				if tx := core.S(v["text"]); tx != "" {
					summary = append(summary, core.JsonObject{"type": "summary_text", "text": tx})
				}
				r := core.JsonObject{"type": "reasoning", "summary": summary}
				if sealed := core.Obj(v["sealed"]); sealed["blob"] != nil {
					if b, ok := in.Hub.Blob(core.S(core.Obj(sealed["blob"])["sha256"])); ok {
						r["encrypted_content"] = string(b)
					}
				}
				add("response_item", r, it.Event.ID, ms)
			}
		}
	}
	closeTurn()
	p.Content, p.Records = shared.LinesToContent(lines)
	p.ID = shared.PlanID(c.id, t.Session, in.Head, p.Mode, core.SHA256Hex(p.Content))
	return p, nil
}
func (c *codexConn) Apply(ctx context.Context, p *engine.DeliveryPlan) (core.JsonObject, error) {
	if err := shared.ApplyAttachments(p); err != nil {
		return nil, err
	}
	receipt, err := shared.ApplyFilePlan(p)
	if err != nil {
		return nil, err
	}
	b, err := c.ensureServer().Call("thread/read", core.JsonObject{"threadId": p.Target.Session, "includeTurns": false})
	if err != nil {
		return nil, fmt.Errorf("new Codex session could not be registered (%s): %w", p.Target.Path, err)
	}
	var r core.JsonObject
	if json.Unmarshal(b, &r) != nil || core.S(core.Obj(r["thread"])["id"]) != p.Target.Session {
		return nil, fmt.Errorf("Codex did not accept the new session")
	}
	if p.Target.Title != "" {
		if _, err := c.ensureServer().Call("thread/name/set", core.JsonObject{"threadId": p.Target.Session, "name": p.Target.Title}); err != nil {
			return nil, err
		}
	}
	receipt["threadId"], receipt["cwd"] = p.Target.Session, p.Extra["cwd"]
	c.files.Set(p.Target.Session, p.Target.Path)
	return receipt, nil
}
