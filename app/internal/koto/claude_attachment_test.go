package koto

import (
	"context"
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/core"
	"koto/internal/engine"
)

type priorClaudeAttachmentReader struct{ engine.Source }

func (s priorClaudeAttachmentReader) Tail(ctx context.Context, ref core.NativeRef, offset *core.SourceOffset, yield func(core.SourceBatch) error) error {
	return s.Source.Tail(ctx, ref, offset, func(batch core.SourceBatch) error {
		for i := range batch.Drafts {
			if batch.Drafts[i].Kind == "attachment" {
				batch.Drafts[i].Kind = "context"
				batch.Drafts[i].Actor = "harness"
			}
		}
		return yield(batch)
	})
}

func TestClaudeAttachmentDecoderSilentlyRefreshesCachedDetail(t *testing.T) {
	s := newSandbox(t)
	file := filepath.Join(ccDir(s.Claude), CCA+".jsonl")
	rows := []any{
		core.JsonObject{"uuid": "u", "type": "user", "parentUuid": nil, "sessionId": CCA, "cwd": CWD, "message": core.JsonObject{"role": "user", "content": "question"}},
		core.JsonObject{"uuid": "f", "type": "attachment", "parentUuid": "u", "attachment": core.JsonObject{"type": "file", "filename": "note.txt", "content": core.JsonObject{"type": "text", "file": core.JsonObject{"content": "saved bytes", "startLine": 1, "numLines": 1, "totalLines": 1}}}},
		core.JsonObject{"uuid": "a", "type": "assistant", "parentUuid": "f", "message": core.JsonObject{"role": "assistant", "content": "answer"}},
	}
	writeLines(t, file, rows)
	before := text(t, file)
	a := s.app(enable("claude-code"))
	api := newAPI(a)
	c := a.Registry.Get("claude-code")
	currentSource, currentVersion := c.Source, c.Version
	c.Source = priorClaudeAttachmentReader{currentSource}
	c.Version = "claude-code-connector@0.2.0"
	eq(t, len(api.Reindex(ctx).Errors), 0)
	old, err := api.Thread(ctx, "claude-code:"+CCA, 0, "")
	must(t, err)
	eq(t, old.Events[1].Kind, "context")
	c.Source, c.Version = currentSource, currentVersion
	// The normal background-scan path detects the changed parser fingerprint;
	// no explicit RebuildIndex call or native file mutation is needed.
	eq(t, len(api.Reindex(ctx).Errors), 0)
	fresh, err := api.Thread(ctx, "claude-code:"+CCA, 0, "")
	must(t, err)
	eq(t, fresh.Events[1].Kind, "attachment", "cached old detail must not survive a parser change")
	eq(t, core.S(core.Obj(fresh.Events[1].Parts[0]["blob"])["sha256"]), core.SHA256Hex("saved bytes"))
	eq(t, text(t, file), before)
}

func TestClaudeNativeQuestionAttachments(t *testing.T) {
	for _, target := range copyAgents {
		t.Run(target, func(t *testing.T) {
			requireCopyAgent(t, "claude-code", target)
			s := newSandbox(t)
			file := filepath.Join(ccDir(s.Claude), CCA+".jsonl")
			bytes := []byte("附件口令：海风-7286\n计数：37\n仅供本次 koto 原生复制验收。\n")
			image := core.Base64("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl6FZ8AAAAASUVORK5CYII=")
			rows := []any{}
			for i, body := range []string{"FIRST_EXCLUDED", "first answer", "SECOND_SELECTED", "", "", "", "second answer"} {
				role := "user"
				if i == 1 || i == 6 {
					role = "assistant"
				}
				var content any = body
				if i == 2 {
					content = []any{core.JsonObject{"type": "text", "text": body}, core.JsonObject{"type": "image", "source": core.JsonObject{"type": "base64", "media_type": "image/png", "data": base64.StdEncoding.EncodeToString(image)}}}
				}
				var parent any
				if i > 0 {
					parent = "event-" + itoa(i-1)
				}
				r := core.JsonObject{"uuid": "event-" + itoa(i), "parentUuid": parent, "sessionId": CCA, "cwd": CWD, "type": role, "message": core.JsonObject{"role": role, "content": content}, "timestamp": ts(100 + i)}
				if i == 3 || i == 4 {
					name, data, lines := "说明 中文🐙.txt", string(bytes), 4
					if i == 4 {
						name, data, lines = "empty.txt", "", 0
					}
					r["type"] = "attachment"
					delete(r, "message")
					r["attachment"] = core.JsonObject{"type": "file", "filename": filepath.Join(s.Root, name), "content": core.JsonObject{"type": "text", "file": core.JsonObject{"filePath": filepath.Join(s.Root, name), "content": data, "startLine": 1, "numLines": lines, "totalLines": lines}}}
					r["renderedRole"] = "system"
					r["rendered"] = []any{core.JsonObject{"content": "<system-reminder>native Read snapshot rendering</system-reminder>"}}
				}
				if i == 5 {
					r["type"] = "attachment"
					delete(r, "message")
					r["attachment"] = core.JsonObject{"type": "directory"}
					r["rendered"] = []any{core.JsonObject{"content": "SECRET_HARNESS_CONTEXT"}}
				}
				rows = append(rows, r)
			}
			writeLines(t, file, rows)
			before := text(t, file)
			a := s.app(enable("claude-code", target))
			api := newAPI(a)
			api.Reindex(ctx)
			d, err := api.Thread(ctx, "claude-code:"+CCA, 0, "")
			must(t, err)
			eq(t, d.CompleteTurns, 2, "attachments do not create user turns")
			eq(t, len(d.Events), 7)
			for _, tc := range []struct {
				name              string
				indices           []int
				exclude, material bool
			}{
				{"all", nil, false, true},
				{"second-turn", []int{2, 3, 4, 5, 6}, false, true},
				{"question-only", []int{2}, false, true},
				{"reply-only", []int{6}, false, false},
				{"first-turn", []int{0, 1}, false, false},
				{"exclude-question", []int{2}, true, false},
				{"exclude-file", []int{3}, true, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					req := CarryRequest{From: d.Head, To: target, Cwd: s.Root}
					if tc.indices != nil {
						ids := []string{}
						for _, i := range tc.indices {
							ids = append(ids, d.Events[i].ID)
						}
						if tc.exclude {
							req.ExcludeEventIDs = ids
						} else {
							req.EventIDs = ids
						}
					}
					r, err := api.Carry(ctx, req)
					must(t, err)
					ok(t, r.Applied)
					out, err := api.Thread(ctx, target+":"+r.Plan.Target.Session, 0, "")
					must(t, err)
					questions := 0
					for _, event := range out.Events {
						if event.Kind == "message" && event.Actor == "user" {
							questions++
						}
					}
					if tc.name == "question-only" || tc.name == "second-turn" {
						eq(t, questions, 1, "native attachment does not create another human prompt")
					}
					body := core.ToJSON(out.Events)
					ok(t, !strings.Contains(body, "SECRET_HARNESS_CONTEXT"), "unknown attachments remain filtered context")
					v, err := a.OpenThread(ctx, target+":"+r.Plan.Target.Session)
					must(t, err)
					found := map[string]int{}
					for _, ev := range out.Events {
						for _, p := range ev.Parts {
							sha := core.S(core.Obj(p["blob"])["sha256"])
							if sha != "" {
								data, exists := v.Hub.Blob(sha)
								ok(t, exists)
								eq(t, core.SHA256Hex(data), sha)
								found[sha]++
							}
						}
					}
					wantText := tc.material && tc.name != "exclude-file"
					eq(t, found[core.SHA256Hex(bytes)] > 0, wantText, "snapshot copied with question")
					eq(t, found[core.SHA256Hex([]byte{})] > 0, tc.material, "empty snapshot remains a real file")
					eq(t, found[core.SHA256Hex(image)] > 0, tc.material, "inline image still present")
					if tc.name == "second-turn" || tc.name == "question-only" {
						ok(t, !strings.Contains(body, "FIRST_EXCLUDED"))
					}
					if tc.name == "all" {
						eq(t, out.CompleteTurns, 2)
					}
					if tc.name == "question-only" {
						recopyIDs := []string{}
						for _, event := range out.Events {
							recopyIDs = append(recopyIDs, event.ID)
						}
						recopy, err := api.Carry(ctx, CarryRequest{From: target + ":" + r.Plan.Target.Session, To: "claude-code", Cwd: s.Root, EventIDs: recopyIDs})
						must(t, err)
						ok(t, recopy.Applied, "recopy applied: ", recopy.Plan)
						assertMaterialBytes(t, a, "claude-code", recopy.Plan.Target.Session, []material{{name: "text", mime: "text/plain", data: bytes}, {name: "empty", mime: "text/plain", data: []byte{}}, {name: "image", mime: "image/png", data: image, image: true}})
					}
					eq(t, text(t, file), before, "native source unchanged")
				})
			}
		})
	}
}
