package koto

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/core"
)

func TestAnalysisNativeFileAttachmentsShareQuestionTurnAndSplitByContent(t *testing.T) {
	s := newSandbox(t)
	const second = "bbbbbbbb-2222-4333-8444-555555555555"
	const same = "cccccccc-2222-4333-8444-555555555555"
	attachedPath := filepath.Join(s.Root, "说明 中文🐙.txt")
	must(t, os.WriteFile(attachedPath, []byte("CURRENT DISK BYTES MUST NOT REPLACE THE HISTORICAL SNAPSHOT"), 0600))
	attachmentBefore := text(t, attachedPath)
	files := map[string]string{}
	for id, snapshot := range map[string]string{CCA: "recorded version A", second: "recorded version B", same: "recorded version A"} {
		uid := func(suffix string) string { return id + "-" + suffix }
		rows := []any{
			core.JsonObject{"type": "user", "uuid": uid("u1"), "parentUuid": nil, "message": core.JsonObject{"role": "user", "content": "Same first question"}},
			core.JsonObject{"type": "assistant", "uuid": uid("a1"), "parentUuid": uid("u1"), "message": core.JsonObject{"role": "assistant", "content": "Same first answer", "stop_reason": "end_turn"}},
			core.JsonObject{"type": "user", "uuid": uid("u2"), "parentUuid": uid("a1"), "message": core.JsonObject{"role": "user", "content": "Compare the attached document"}},
			core.JsonObject{"type": "attachment", "uuid": uid("file"), "parentUuid": uid("u2"), "renderedRole": "system", "attachment": core.JsonObject{
				"type": "file", "filename": attachedPath, "content": core.JsonObject{"type": "text", "file": core.JsonObject{
					"filePath": attachedPath, "content": snapshot, "startLine": 1, "numLines": 1, "totalLines": 1,
				}},
			}},
			core.JsonObject{"type": "assistant", "uuid": uid("a2"), "parentUuid": uid("file"), "message": core.JsonObject{"role": "assistant", "content": "Same second answer", "stop_reason": "end_turn"}},
		}
		for _, row := range rows {
			r := row.(core.JsonObject)
			r["sessionId"], r["cwd"], r["version"] = id, CWD, "2.1.300"
		}
		file := filepath.Join(ccDir(s.Claude), id+".jsonl")
		writeLines(t, file, rows)
		files[file] = text(t, file)
	}
	a := s.app(enable("claude-code"))
	api := newAPI(a)
	api.Reindex(ctx)
	indexBefore := core.ToJSON(a.Store.Sessions())
	result, err := api.Analyze(ctx, []string{"claude-code:" + CCA, "claude-code:" + second, "claude-code:" + same})
	must(t, err)
	eq(t, len(result.Nodes), 3, "first question is shared; only the attached snapshot forks the second question")
	for _, session := range result.Sessions {
		eq(t, session.Turns, 2, "a file attachment must not become a new human turn")
		eq(t, len(session.Nodes), 2)
	}
	eq(t, result.Sessions[0].Nodes[0], result.Sessions[1].Nodes[0])
	eq(t, result.Sessions[0].Nodes[1], result.Sessions[2].Nodes[1], "different native IDs do not split identical snapshots")
	ok(t, result.Sessions[0].Nodes[1] != result.Sessions[1].Nodes[1], "different attachment bytes must split the tree")
	seen := map[string]bool{}
	for _, node := range result.Nodes {
		if node.Turn != 2 {
			continue
		}
		ok(t, strings.Contains(node.Text, "说明 中文🐙.txt"), "diff omitted the attachment name")
		for _, value := range []string{"recorded version A", "recorded version B"} {
			hash := core.SHA256Hex(value)
			if strings.Contains(node.Text, hash) {
				seen[hash] = true
			}
		}
	}
	eq(t, len(seen), 2, "diff must expose both recorded attachment SHA values")
	for file, before := range files {
		eq(t, text(t, file), before, "analysis modified a source transcript")
	}
	eq(t, text(t, attachedPath), attachmentBefore, "analysis modified the original attachment")
	eq(t, core.ToJSON(a.Store.Sessions()), indexBefore, "read-only analysis changed the index")
	// The native writer embeds the preserved file in the copied question. That
	// different envelope must keep the same two human turns and attachment bytes.
	copied, err := api.Carry(ctx, CarryRequest{From: "claude-code:" + CCA, To: "claude-code"})
	must(t, err)
	ok(t, copied.Applied)
	target := core.SessionSelector(copied.Plan.Target.Profile, "claude-code", copied.Plan.Target.Session)
	comparison, err := api.Analyze(ctx, []string{"claude-code:" + CCA, target})
	must(t, err)
	for _, session := range comparison.Sessions {
		eq(t, session.Turns, 2, "native copy changed the human turn count")
	}
	for _, node := range comparison.Nodes {
		if node.Turn == 2 {
			ok(t, strings.Contains(node.Text, core.SHA256Hex("recorded version A")), "copy comparison lost snapshot bytes")
		}
	}
	for file, before := range files {
		eq(t, text(t, file), before, "copy or comparison modified a source transcript")
	}
	eq(t, text(t, attachedPath), attachmentBefore)
}

func TestAnalysisAttachmentRecordAndCopiedUserPartsCompareEqually(t *testing.T) {
	question := core.Part{"type": "text", "text": "Read the attached document"}
	file := core.Part{"type": "file", "name": "说明.txt", "mediaType": "text/plain", "blob": core.JsonObject{"sha256": core.SHA256Hex("recorded bytes"), "size": 14}}
	source := []*core.Event{
		{ID: "question", Kind: "message", Actor: "user", Parts: []core.Part{question}},
		{ID: "file", Kind: "attachment", Actor: "user", Parents: []string{"question"}, Parts: []core.Part{file}},
		{ID: "answer", Kind: "message", Actor: "assistant", Parents: []string{"file"}, Parts: []core.Part{{"type": "text", "text": "The file says recorded bytes"}}},
	}
	copied := []*core.Event{
		{ID: "new-question", Kind: "message", Actor: "user", Parts: []core.Part{question, file}},
		{ID: "new-answer", Kind: "message", Actor: "assistant", Parts: source[2].Parts},
	}
	before := core.ToJSON(source)
	sourceCanonical, sourceDisplay, _ := analysisContentDisplay(source)
	copiedCanonical, _, _ := analysisContentDisplay(copied)
	eq(t, sourceCanonical, copiedCanonical, "an attachment's native envelope must not create a false branch after copying")
	ok(t, strings.Contains(sourceDisplay.text, core.SHA256Hex("recorded bytes")))
	eq(t, core.ToJSON(source), before, "canonical comparison mutated source events")
	changed := *source[1]
	changed.Parts = []core.Part{{"type": "file", "name": "说明.txt", "mediaType": "text/plain", "blob": core.JsonObject{"sha256": core.SHA256Hex("changed bytes"), "size": 13}}}
	source[1] = &changed
	changedCanonical, _, _ := analysisContentDisplay(source)
	ok(t, changedCanonical != copiedCanonical, "attachment bytes must remain part of comparison")
}
