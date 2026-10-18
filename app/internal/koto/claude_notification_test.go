package koto

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/core"
)

// This is the shape emitted by Claude Code 2.1.300 after an async Agent call:
// the launch result is separate from the eventual parent-visible task notification.
func writeClaudeNotificationFixture(t *testing.T, root, session string) (string, []string, string) {
	t.Helper()
	file := filepath.Join(ccDir(root), session+".jsonl")
	child := filepath.Join(ccDir(root), session, "subagents", "agent-private.jsonl")
	hidden := "CHILD_PRIVATE_ONLY_NEVER_DELIVERED"
	writeLines(t, child, []any{map[string]any{"type": "assistant", "uuid": "private", "isSidechain": true, "message": map[string]any{"role": "assistant", "content": hidden}}})
	contents := []string{
		"Ask the background agent for the result.",
		"Launching the background agent.",
		"Agent launched. Its output_file is " + child,
		"<task-notification>\n<task-id>native-task-1</task-id>\n<output-file>" + child + "</output-file>\n<result>PARENT_VISIBLE_FIRST</result>\n</task-notification>",
		"The background task has finished.",
		"A second human question.",
		"<task-notification>\n<task-id>native-task-2</task-id>\n<result>PARENT_VISIBLE_LATE</result>\n</task-notification>",
		"The second answer.",
	}
	rows := []any{}
	for i, content := range contents {
		role := "user"
		var body any = content
		if i == 1 || i == 4 || i == 7 {
			role = "assistant"
			body = []any{map[string]any{"type": "text", "text": content}}
		}
		if i == 1 {
			body = []any{map[string]any{"type": "tool_use", "id": "launch", "name": "Agent", "input": map[string]any{"prompt": "Read only inside the child context"}}}
		}
		if i == 2 {
			body = []any{map[string]any{"type": "tool_result", "tool_use_id": "launch", "content": content}}
		}
		var parent any
		if i > 0 {
			parent = "event-" + itoa(i-1)
		}
		row := map[string]any{"uuid": "event-" + itoa(i), "parentUuid": parent, "sessionId": session, "cwd": CWD, "type": role, "isSidechain": false, "message": map[string]any{"role": role, "content": body}, "timestamp": ts(100 + i)}
		if role == "assistant" {
			core.Obj(row["message"])["stop_reason"] = "end_turn"
		}
		if i == 3 || i == 6 {
			row["origin"] = map[string]any{"kind": "task-notification", "producer": "session-task"}
			row["promptSource"] = "system"
			row["turnOrigin"] = "task_notification"
		}
		rows = append(rows, row)
	}
	writeLines(t, file, rows)
	return file, contents, child
}

func TestClaudeTaskNotificationCopyTargets(t *testing.T) {
	for _, target := range copyAgents {
		t.Run(target, func(t *testing.T) {
			requireCopyAgent(t, "claude-code", target)
			s := newSandbox(t)
			file, contents, child := writeClaudeNotificationFixture(t, s.Claude, CCA)
			before, childBefore := text(t, file), text(t, child)
			a := s.app(enable("claude-code", target))
			api := newAPI(a)
			api.Reindex(ctx)
			d, err := api.Thread(ctx, "claude-code:"+CCA, 0, "")
			must(t, err)
			eq(t, d.CompleteTurns, 2, "native notifications do not add human turns")
			eq(t, len(d.Events), 8)
			view, err := api.open(ctx, "claude-code:"+CCA)
			must(t, err)
			for _, i := range []int{3, 6} {
				eq(t, d.Events[i].Kind, "notification")
				eq(t, core.S(d.Events[i].Parts[0]["text"]), contents[i])
				eq(t, view.Hub.TurnsAt(d.Events[i].ID), i/5+1)
			}
			page, err := api.Thread(ctx, "claude-code:"+CCA, 1, "")
			must(t, err)
			eq(t, len(page.Events), 3, "latest page contains late notification with the second prompt")
			eq(t, page.Events[1].ID, d.Events[6].ID)
			earlier, err := api.Thread(ctx, "claude-code:"+CCA, 1, page.Events[0].ID)
			must(t, err)
			eq(t, len(earlier.Events), 5, "first turn includes task launch, delivered result and answer")
			for _, tc := range []struct {
				name    string
				indices []int
				exclude bool
			}{
				{"all", []int{0, 1, 2, 3, 4, 5, 6, 7}, false},
				{"first-turn", []int{0, 1, 2, 3, 4}, false},
				{"second-turn", []int{5, 6, 7}, false},
				{"question-only", []int{0}, false},
				{"reply-only", []int{1, 2, 3, 4}, false},
				{"notification-only", []int{3}, false},
				{"exclude-last-turn", []int{5, 6, 7}, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					selected := map[int]bool{}
					ids := []string{}
					for _, i := range tc.indices {
						selected[i] = true
						ids = append(ids, d.Events[i].ID)
					}
					req := CarryRequest{From: d.Head, To: target, Cwd: s.Root}
					if tc.exclude {
						req.ExcludeEventIDs = ids
					} else {
						req.EventIDs = ids
					}
					r, err := api.Carry(ctx, req)
					must(t, err)
					ok(t, r.Applied)
					out, err := api.Thread(ctx, target+":"+r.Plan.Target.Session, 30, "")
					must(t, err)
					body := core.ToJSON(out.Events)
					for _, i := range []int{0, 3, 4, 5, 6, 7} {
						encoded := strings.Trim(core.ToJSON(contents[i]), "\"")
						eq(t, strings.Contains(body, encoded), selected[i] != tc.exclude, tc.name, " event ", i)
					}
					ok(t, !strings.Contains(body, "CHILD_PRIVATE_ONLY_NEVER_DELIVERED"), "independent child transcript is not parent context")
					if target == "claude-code" && tc.name == "all" {
						eq(t, out.CompleteTurns, 2, "same-Agent raw notifications retain their native meaning")
					}
					if tc.name == "notification-only" {
						ok(t, out.Turns != nil && *out.Turns == 1, "a notification excerpt stays visible")
					}
					eq(t, text(t, file), before, "source untouched")
					eq(t, text(t, child), childBefore, "child file untouched")
				})
			}
		})
	}
}

func TestClaudeTaskNotificationAnalysisAndLiteralPrompt(t *testing.T) {
	s := newSandbox(t)
	_, _, _ = writeClaudeNotificationFixture(t, s.Claude, CCA)
	file, _, _ := writeClaudeNotificationFixture(t, s.Claude, CCF)
	rows := readJSONL(t, file)
	core.Obj(rows[3]["message"])["content"] = "<task-notification>changed parent result</task-notification>"
	var updated []any
	for _, r := range rows {
		updated = append(updated, r)
	}
	writeLines(t, file, updated)
	a := s.app(enable("claude-code"))
	api := newAPI(a)
	api.Reindex(ctx)
	result, err := api.Analyze(ctx, []string{"claude-code:" + CCA, "claude-code:" + CCF})
	must(t, err)
	for _, s := range result.Sessions {
		eq(t, s.Turns, 2, "comparison keeps original human turns")
	}
	first := map[string]bool{}
	for _, n := range result.Nodes {
		if n.Turn == 1 {
			first[n.ID] = true
		}
		ok(t, !strings.Contains(n.Text, "CHILD_PRIVATE_ONLY_NEVER_DELIVERED"))
	}
	eq(t, len(first), 2, "a changed parent-visible notification creates a real diff")

	// A literal task-notification in a user's question has no native provenance; do not
	// discard it or silently reinterpret it as a background event.
	literal := "<task-notification><result>quoted</result></task-notification>\nWhat does this mean?"
	record := map[string]any{"uuid": "literal", "parentUuid": nil, "type": "user", "sessionId": CCSession, "message": map[string]any{"role": "user", "content": literal}}
	writeLines(t, filepath.Join(ccDir(s.Claude), CCSession+".jsonl"), []any{record})
	api.Reindex(ctx)
	d, err := api.Thread(ctx, "claude-code:"+CCSession, 0, "")
	must(t, err)
	eq(t, d.Events[0].Kind, "message")
	eq(t, core.S(d.Events[0].Parts[0]["text"]), literal)
	// Explicit native notification metadata wins even for older records with isMeta set.
	record["uuid"] = "native"
	record["turnOrigin"] = "task_notification"
	record["isMeta"] = true
	must(t, os.Remove(filepath.Join(ccDir(s.Claude), CCSession+".jsonl")))
	writeLines(t, filepath.Join(ccDir(s.Claude), CCSession+".jsonl"), []any{record})
	api.Reindex(ctx)
	// Analyze decodes into a fresh hub; source cache invalidation is exercised on reopening.
	reopened, err := api.Thread(ctx, "claude-code:"+CCSession, 0, "")
	must(t, err)
	eq(t, reopened.Events[0].Kind, "notification")
}
