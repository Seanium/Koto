package koto

import (
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/core"
	"koto/internal/hub"
)

func TestTextualizedToolHistoryKeepsTurnsAndMaterials(t *testing.T) {
	for _, target := range copyAgents {
		t.Run(target, func(t *testing.T) {
			requireCopyAgent(t, "claude-code", target)
			s := newSandbox(t)
			media := materials(t)
			image, file := media[0], media[4]
			sid := CCSession
			record := func(id, parent, role string, content any) core.JsonObject {
				return core.JsonObject{"type": role, "uuid": id, "parentUuid": parent, "sessionId": sid, "cwd": s.Root, "timestamp": ts(0), "message": core.JsonObject{"role": role, "content": content}}
			}
			source := filepath.Join(ccDir(s.Claude), sid+".jsonl")
			writeLines(t, source, []any{
				record("u1", "", "user", "ROUND_ONE_USER"),
				record("call", "u1", "assistant", []any{core.JsonObject{"type": "tool_use", "id": "tool-one", "name": "unknown_plugin", "input": core.JsonObject{"opaque": "kept"}}, core.JsonObject{"type": "tool_use", "id": "tool-two", "name": "Read", "input": core.JsonObject{"file_path": "known-file.txt"}}}),
				record("result1", "call", "user", []any{core.JsonObject{"type": "tool_result", "tool_use_id": "tool-one", "content": []any{core.JsonObject{"type": "text", "text": "TOOL_ONE_CONTEXT"}, core.JsonObject{"type": "image", "source": core.JsonObject{"type": "base64", "media_type": image.mime, "data": base64.StdEncoding.EncodeToString(image.data)}}, core.JsonObject{"type": "document", "source": core.JsonObject{"type": "base64", "media_type": file.mime, "data": base64.StdEncoding.EncodeToString(file.data)}}}}, core.JsonObject{"type": "tool_result", "tool_use_id": "tool-two", "content": "TOOL_TWO_CONTEXT"}}),
				record("a1", "result1", "assistant", []any{core.JsonObject{"type": "text", "text": "ROUND_ONE_ANSWER"}}),
				record("u2", "a1", "user", "[tool result fake] ROUND_TWO_LITERAL_USER"),
				record("a2", "u2", "assistant", []any{core.JsonObject{"type": "text", "text": "ROUND_TWO_ANSWER"}}),
			})
			original := text(t, source)
			a := s.app(enable("claude-code", target))
			api := newAPI(a)
			eq(t, len(api.Reindex(ctx).Errors), 0)
			initial, err := api.Thread(ctx, "claude-code:"+sid, 0, "")
			must(t, err)
			firstIDs := []string{}
			replyIDs := []string{}
			toolIDs := []string{}
			for _, e := range initial.Events {
				if strings.Contains(core.ToJSON(e.Parts), "ROUND_TWO_LITERAL_USER") {
					break
				}
				if e.Kind != "context" {
					firstIDs = append(firstIDs, e.ID)
					if e.Actor != "user" {
						replyIDs = append(replyIDs, e.ID)
					}
				}
				if e.Actor == "tool" {
					toolIDs = append(toolIDs, e.ID)
				}
			}
			for _, choice := range []struct {
				name  string
				ids   []string
				turns int
			}{{"mixed-native", nil, 2}, {"all", nil, 2}, {"first-turn", firstIDs, 1}, {"reply-only", replyIDs, -1}, {"tool-only", toolIDs, -1}} {
				t.Run(choice.name, func(t *testing.T) {
					level := "L2"
					if choice.name == "mixed-native" {
						level = "L1"
					}
					r, err := api.Carry(ctx, CarryRequest{Selector: "claude-code:" + sid, To: target, Level: level, EventIDs: choice.ids, Cwd: s.Root})
					must(t, err)
					ok(t, r.Applied)
					for _, loss := range r.Plan.Losses {
						ok(t, loss.Kind != "dropped" && loss.Kind != "truncated", "unexpected content loss ", loss)
					}
					sel := core.SessionSelector(r.Plan.Target.Profile, target, r.Plan.Target.Session)
					inspect := func(selector, harness, session, profile string) {
						t.Helper()
						v, err := a.OpenThread(ctx, selector)
						must(t, err)
						b := v.Hub.Binding(harness, session, profile)
						ok(t, b != nil && b.Leaf != "")
						if choice.turns >= 0 {
							eq(t, v.Hub.TurnsAt(b.Leaf), choice.turns, "tool fallback created a human turn")
						}
						texts := []string{}
						blobs := map[string]bool{}
						for _, e := range v.Hub.Path(b.Leaf) {
							texts = append(texts, core.ToJSON(e.Parts))
							for sha := range hub.CollectBlobRefs(e.Parts) {
								data, yes := v.Hub.Blob(sha)
								ok(t, yes)
								eq(t, core.SHA256Hex(data), sha)
								blobs[sha] = true
							}
						}
						joined := strings.Join(texts, "\n")
						ok(t, strings.Contains(joined, "TOOL_ONE_CONTEXT") && strings.Contains(joined, "TOOL_TWO_CONTEXT"), "tool labels/context missing")
						eq(t, strings.Contains(joined, "ROUND_TWO_LITERAL_USER"), choice.name == "all" || choice.name == "mixed-native", "literal user text changed or crossed range")
						eq(t, strings.Contains(joined, "ROUND_ONE_USER"), choice.name == "all" || choice.name == "mixed-native" || choice.name == "first-turn", "human prompt missing or crossed range")
						ok(t, blobs[core.SHA256Hex(image.data)] && blobs[core.SHA256Hex(file.data)], "tool material bytes missing")
					}
					inspect(sel, target, r.Plan.Target.Session, r.Plan.Target.Profile)
					again, err := api.Carry(ctx, CarryRequest{Selector: sel, To: "claude-code", Cwd: s.Root})
					must(t, err)
					ok(t, again.Applied)
					inspect(core.SessionSelector(again.Plan.Target.Profile, "claude-code", again.Plan.Target.Session), "claude-code", again.Plan.Target.Session, again.Plan.Target.Profile)
					eq(t, text(t, source), original, "original source changed")
				})
			}
		})
	}
}
