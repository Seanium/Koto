package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/koto"
	"koto/internal/planner"
)

const testThread = "01900000-1111-7111-8111-111111111111"
const otherThread = "01900000-2222-7222-8222-222222222222"

type handoffSandbox struct {
	root, codex, pi, cwd string
	flags                []string
}

func newHandoffSandbox(t *testing.T) handoffSandbox {
	t.Helper()
	root := t.TempDir()
	s := handoffSandbox{root: root, codex: filepath.Join(root, "codex"), pi: filepath.Join(root, "pi"), cwd: filepath.Join(root, "a project 'quoted' $(false)")}
	for _, path := range []string{s.codex, s.pi, s.cwd} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	s.flags = []string{"--json", "--data", filepath.Join(root, "data"), "--codex-home", s.codex, "--pi-root", s.pi, "--only", "codex,pi"}
	return s
}

func (s handoffSandbox) writeCodex(t *testing.T, key, marker string, pending bool) string {
	t.Helper()
	file := filepath.Join(s.codex, "sessions", "2026", "10", "17", "rollout-2026-10-17T10-00-00-"+key+".jsonl")
	id := strings.SplitN(key, "_", 2)[0]
	rows := []map[string]any{{"type": "session_meta", "payload": map[string]any{"id": id, "cwd": s.cwd, "source": "cli"}}}
	message := func(role, text string, extras ...map[string]any) map[string]any {
		parts := []any{map[string]any{"type": "input_text", "text": text}}
		for _, p := range extras {
			parts = append(parts, p)
		}
		return map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": role, "content": parts}}
	}
	rows = append(rows, message("developer", "PRIVATE SYSTEM CONTEXT NOT COPIED"), message("user", marker+" question one", map[string]any{"type": "input_image", "image_url": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aVxkAAAAASUVORK5CYII="}), message("assistant", marker+" answer one"), message("user", marker+" question two"), message("assistant", marker+" answer two"))
	if pending {
		rows = append(rows, message("user", "MIGRATE CURRENT REQUEST"), map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "assistant", "phase": "commentary", "content": []any{map[string]any{"type": "output_text", "text": "CURRENT HANDOFF IN PROGRESS"}}}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call", "call_id": "running-koto", "name": "exec_command", "arguments": `{"cmd":"koto copy --current --to pi --json"}`}})
	}
	var out bytes.Buffer
	for i, row := range rows {
		row["timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
		row["ordinal"] = i
		if err := json.NewEncoder(&out).Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, out.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return file
}

func (s handoffSandbox) run(t *testing.T, pos []string, extra []string, env map[string]string) (handoffResult, int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	flags := append(append([]string{}, s.flags...), extra...)
	code := runHandoff(pos, flags, &stdout, &stderr, func(k string) string { return env[k] })
	var result handoffResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("JSON output: %v: %s / stderr %s", err, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
	return result, code, stdout.String()
}

func TestCurrentExactSessionAndMetadata(t *testing.T) {
	s := newHandoffSandbox(t)
	s.writeCodex(t, testThread, "SOURCE", false)
	s.writeCodex(t, otherThread, "NEWER SAME CWD", false)
	r, code, raw := s.run(t, []string{"current"}, nil, map[string]string{"CODEX_THREAD_ID": testThread})
	if code != 0 || !r.OK || r.Source.Selector != "codex:"+testThread || r.Source.Cwd != s.cwd || r.ResolvedBy != "CODEX_THREAD_ID" {
		t.Fatalf("wrong exact session: %d %s", code, raw)
	}
	if strings.Contains(raw, "question") || strings.Contains(raw, "PRIVATE SYSTEM") {
		t.Fatal("current exposed conversation content")
	}
	r, code, _ = s.run(t, []string{"current"}, nil, map[string]string{"CODEX_THREAD_ID": otherThread, "KOTO_SESSION": "codex:" + testThread})
	if code != 0 || r.ResolvedBy != "KOTO_SESSION" || r.Source.Session != testThread {
		t.Fatal("explicit current session was not honored")
	}
}

func TestNativeHostSessionIdentity(t *testing.T) {
	for _, host := range []struct{ key, agent, id string }{
		{"CLAUDE_CODE_SESSION_ID", "claude-code", testThread},
		{"CODEX_THREAD_ID", "codex", testThread},
		{"PI_SESSION_ID", "pi", testThread},
		{"DSH_SESSION_ID", "dsh", "session-" + testThread},
		{"ANTIGRAVITY_CONVERSATION_ID", "antigravity", testThread},
	} {
		t.Run(host.agent, func(t *testing.T) {
			env := map[string]string{host.key: host.id}
			selector, by, err := sourceSelector([]string{"current"}, false, func(k string) string { return env[k] })
			if err != nil || by != host.key || selector != host.agent+":"+host.id {
				t.Fatalf("native identity: %s %s %v", selector, by, err)
			}
		})
	}
	env := map[string]string{"CLAUDE_CODE_SESSION_ID": testThread, "PI_SESSION_ID": otherThread}
	_, _, err := sourceSelector([]string{"current"}, false, func(k string) string { return env[k] })
	if err == nil || err.(*handoffError).Code != "CURRENT_SESSION_AMBIGUOUS" {
		t.Fatal("conflicting inherited identities must not select an arbitrary source")
	}
	env["KOTO_SESSION"] = "pi:" + otherThread
	selector, by, err := sourceSelector([]string{"current"}, false, func(k string) string { return env[k] })
	if err != nil || by != "KOTO_SESSION" || selector != env["KOTO_SESSION"] {
		t.Fatal("explicit identity should disambiguate host environments")
	}
}

func TestCurrentUnknownStaleAmbiguousAndWrongMetadata(t *testing.T) {
	for _, test := range []struct{ name, id, code string }{
		{"missing", "", "CURRENT_SESSION_UNKNOWN"},
		{"stale", otherThread, "SOURCE_NOT_FOUND"},
		{"ambiguous", testThread, "SOURCE_AMBIGUOUS"},
		{"mismatch", testThread, "SOURCE_ID_MISMATCH"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newHandoffSandbox(t)
			file := s.writeCodex(t, testThread, "SOURCE", false)
			if test.name == "ambiguous" {
				s.writeCodex(t, testThread+"_rollback", "ROLLBACK", false)
			}
			if test.name == "mismatch" {
				raw, _ := os.ReadFile(file)
				if err := os.WriteFile(file, bytes.ReplaceAll(raw, []byte(testThread), []byte(otherThread)), 0600); err != nil {
					t.Fatal(err)
				}
			}
			r, code, raw := s.run(t, []string{"current"}, nil, map[string]string{"CODEX_THREAD_ID": test.id})
			if code == 0 || r.OK || r.Error == nil || r.Error.Code != test.code {
				t.Fatalf("expected %s: %d %s", test.code, code, raw)
			}
		})
	}
}

func TestCopyCurrentPreviewNativeReadbackAndSourceUnchanged(t *testing.T) {
	s := newHandoffSandbox(t)
	file := s.writeCodex(t, testThread, "PRESERVED", true)
	s.writeCodex(t, otherThread, "UNRELATED", false)
	before, _ := os.ReadFile(file)
	env := map[string]string{"CODEX_THREAD_ID": testThread}
	r, code, raw := s.run(t, []string{"copy"}, []string{"--current", "--to", "pi", "--dry-run"}, env)
	if code != 0 || r.State != "preview" || r.Resume != nil || r.Destination.Session != "" || r.Counts.Turns != 2 || r.Counts.Messages != 4 || r.Counts.Attachments != 1 || r.Deferred.Events != 3 {
		t.Fatalf("preview: %d %s", code, raw)
	}
	if len(nativeJSONLs(t, s.pi)) != 0 {
		t.Fatal("preview created native target")
	}
	r, code, raw = s.run(t, []string{"copy"}, []string{"--current", "--to", "pi"}, env)
	prefix := "cd " + engine.ShQ(s.cwd) + " && "
	if runtime.GOOS == "windows" {
		prefix = "Set-Location -LiteralPath " + engine.PowerShellQ(s.cwd) + " -ErrorAction Stop; "
	}
	if code != 0 || r.State != "copied" || !r.OK || r.Resume == nil || r.Resume.Cwd == nil || *r.Resume.Cwd != s.cwd || !strings.HasPrefix(r.Resume.Shell, prefix) {
		t.Fatalf("copy: %d %s", code, raw)
	}
	if !strings.Contains(r.Instructions, "resume.shell exactly as returned") || !strings.Contains(r.Instructions, "Do not launch") {
		t.Fatal("copy result must tell the calling Agent how to report and continue")
	}
	if strings.Contains(raw, "iVBOR") || strings.Contains(raw, "PRESERVED answer") || strings.Contains(raw, "records") {
		t.Fatal("copy report exposed raw content")
	}
	after, _ := os.ReadFile(file)
	if !bytes.Equal(before, after) {
		t.Fatal("source was changed")
	}
	files := nativeJSONLs(t, s.pi)
	if len(files) != 1 {
		t.Fatalf("expected exactly one target, got %v", files)
	}
	native, _ := os.ReadFile(files[0])
	for _, absent := range []string{"CURRENT HANDOFF", "MIGRATE CURRENT", "running-koto", "PRIVATE SYSTEM", "UNRELATED"} {
		if bytes.Contains(native, []byte(absent)) {
			t.Fatalf("target contains excluded content %s", absent)
		}
	}
	app, err := koto.New(koto.Config{DataDir: filepath.Join(s.root, "readback"), PiRoot: s.pi, Enable: []string{"pi"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	report, err := app.Engine.Scan(context.Background(), engine.ScanOptions{})
	if err != nil || len(report.Errors) != 0 {
		t.Fatalf("native readback: %v %+v", err, report)
	}
	binding := app.Hub.Binding("pi", r.Destination.Session)
	if binding == nil || app.Hub.TurnsAt(binding.Leaf) != 2 {
		t.Fatal("target did not contain the two completed turns")
	}
	text := core.ToJSON(app.Hub.Path(binding.Leaf))
	if !strings.Contains(text, "PRESERVED answer two") || !strings.Contains(text, `"type":"image"`) {
		t.Fatal("native readback lost text or image")
	}
	// An explicit same-Agent copy must mint a second independent native session.
	r2, code, raw := s.run(t, []string{"copy", r.Destination.Selector}, []string{"--to", "pi"}, nil)
	if code != 0 || r2.Destination.Session == r.Destination.Session || len(nativeJSONLs(t, s.pi)) != 2 {
		t.Fatalf("same-Agent copy did not create a new session: %s", raw)
	}
}

func nativeJSONLs(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".jsonl") {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func TestCopyRejectsWrongTargetOrSelectionWithStructuredError(t *testing.T) {
	s := newHandoffSandbox(t)
	s.writeCodex(t, testThread, "SOURCE", false)
	for _, test := range []struct {
		pos, flags []string
		code       string
	}{
		{[]string{"copy"}, []string{"--current", "--to", "absent"}, "COPY_REJECTED"},
		{[]string{"copy", "codex:019"}, []string{"--to", "pi"}, "SOURCE_NOT_FOUND"},
		{[]string{"copy", "codex:" + testThread}, []string{"--current", "--to", "pi"}, "USAGE"},
		{[]string{"copy"}, []string{"--current", "--to", "pi", "--unknown-option"}, "USAGE"},
		{[]string{"copy"}, []string{"--current", "--to", "pi", "--cwd", filepath.Join(s.root, "missing")}, "INVALID_CWD"},
	} {
		r, code, raw := s.run(t, test.pos, test.flags, map[string]string{"CODEX_THREAD_ID": testThread})
		if code == 0 || r.OK || r.Error == nil || r.Error.Code != test.code {
			t.Fatalf("expected %s: %d %s", test.code, code, raw)
		}
	}
	if len(nativeJSONLs(t, s.pi)) != 0 {
		t.Fatal("failed request created target")
	}
}

func TestCurrentCodexForkIncludesDeclaredHistoryAndFailsIfMissing(t *testing.T) {
	s := newHandoffSandbox(t)
	parent := s.writeCodex(t, testThread, "PARENT", false)
	child := s.writeCodex(t, otherThread, "CHILD", false)
	rows := []map[string]any{
		{"type": "session_meta", "ordinal": 0, "payload": map[string]any{"id": otherThread, "cwd": s.cwd, "forked_from_id": testThread, "forked_from_ordinal_exclusive": 6}},
		{"type": "response_item", "ordinal": 6, "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "CHILD question"}}}},
		{"type": "response_item", "ordinal": 7, "payload": map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "CHILD answer"}}}},
	}
	var data bytes.Buffer
	for _, row := range rows {
		row["timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
		if err := json.NewEncoder(&data).Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(child, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	r, code, raw := s.run(t, []string{"copy"}, []string{"--current", "--to", "pi"}, map[string]string{"CODEX_THREAD_ID": otherThread})
	if code != 0 || r.Counts.Turns != 3 {
		t.Fatalf("fork should include parent history: %d %s", code, raw)
	}
	target, _ := os.ReadFile(nativeJSONLs(t, s.pi)[0])
	if !bytes.Contains(target, []byte("PARENT question one")) || !bytes.Contains(target, []byte("CHILD answer")) {
		t.Fatal("fork copied only its tail")
	}
	if err := os.Remove(parent); err != nil {
		t.Fatal(err)
	}
	r, code, raw = s.run(t, []string{"copy"}, []string{"--current", "--to", "pi"}, map[string]string{"CODEX_THREAD_ID": otherThread})
	if code == 0 || r.Error == nil || r.Error.Code != "SOURCE_HISTORY_MISSING" || len(nativeJSONLs(t, s.pi)) != 1 {
		t.Fatalf("missing parent must not silently copy only tail: %d %s", code, raw)
	}
}

func TestCompletedSnapshotIgnoresL0PendingToolAndCommentary(t *testing.T) {
	path := []*core.Event{
		{ID: "q1", Kind: "message", Actor: "user"},
		{ID: "a1", Kind: "message", Actor: "assistant", Attrs: core.JsonObject{"phase": "final_answer"}},
		{ID: "q2", Kind: "message", Actor: "user"},
		{ID: "c2", Kind: "message", Actor: "assistant", Attrs: core.JsonObject{"phase": "commentary"}},
		{ID: "t2", Kind: "tool_call", Actor: "assistant", Parts: []core.Part{{"type": "tool_call", "callId": "in-progress"}}},
	}
	if head, n := completedSnapshot(path); head != "a1" || n != 3 {
		t.Fatalf("pending boundary: %s %d", head, n)
	}
	if head, n := completedSnapshot(path[:4]); head != "a1" || n != 2 {
		t.Fatalf("commentary is not final: %s %d", head, n)
	}
	path = append(path, &core.Event{ID: "r2", Actor: "tool", Parts: []core.Part{{"type": "tool_result", "callId": "in-progress"}}}, &core.Event{ID: "a2", Kind: "message", Actor: "assistant", Attrs: core.JsonObject{"phase": "final_answer"}})
	if head, n := completedSnapshot(path); head != "a2" || n != 0 {
		t.Fatalf("completed boundary: %s %d", head, n)
	}
}

func TestCompletedSnapshotExcludesConsecutiveSteeringUntilFinalAnswer(t *testing.T) {
	for _, attrs := range []core.JsonObject{{"phase": "commentary"}, {"stopReason": "toolUse"}, {"stopReason": "tool_use"}, {"stopReason": "aborted"}, {"stopReason": "pause_turn"}, {"stopReason": "max_tokens"}} {
		path := []*core.Event{
			{ID: "old-question", Kind: "message", Actor: "user"},
			{ID: "old-answer", Kind: "message", Actor: "assistant", Attrs: core.JsonObject{"phase": "final_answer"}},
			{ID: "migration-request", Kind: "message", Actor: "user"},
			{ID: "commentary", Kind: "message", Actor: "assistant", Attrs: attrs, Parts: []core.Part{{"type": "text", "text": "Working on it"}}},
			{ID: "current-call", Kind: "tool_call", Actor: "assistant", Parts: []core.Part{{"type": "tool_call", "callId": "current"}}},
			{ID: "clarification", Kind: "message", Actor: "user"},
			{ID: "pending-copy", Kind: "tool_call", Actor: "assistant", Parts: []core.Part{{"type": "tool_call", "callId": "copy"}}},
		}
		if head, n := completedSnapshot(path); head != "old-answer" || n != 5 {
			t.Fatalf("steering leaked for %v: head=%s deferred=%d", attrs, head, n)
		}
	}
}

func TestCurrentSameAgentPiExcludesRunningInvocation(t *testing.T) {
	s := newHandoffSandbox(t)
	s.writeCodex(t, testThread, "ORIGINAL", false)
	first, code, raw := s.run(t, []string{"copy", "codex:" + testThread}, []string{"--to", "pi"}, nil)
	if code != 0 {
		t.Fatal(raw)
	}
	file := nativeJSONLs(t, s.pi)[0]
	data, _ := os.ReadFile(file)
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	var last map[string]any
	if err := json.Unmarshal(lines[len(lines)-1], &last); err != nil {
		t.Fatal(err)
	}
	newRows := []map[string]any{
		{"type": "message", "id": "request-koto", "parentId": last["id"], "message": map[string]any{"role": "user", "content": "RUNNING REQUEST"}},
		{"type": "message", "id": "invoke-koto", "parentId": "request-koto", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "id": "invoke", "name": "bash", "arguments": map[string]any{"command": "koto copy --current --to pi --json"}}}}},
	}
	var tail bytes.Buffer
	for _, row := range newRows {
		row["timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
		if err := json.NewEncoder(&tail).Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	before := append(append([]byte{}, data...), tail.Bytes()...)
	if err := os.WriteFile(file, before, 0600); err != nil {
		t.Fatal(err)
	}
	result, code, raw := s.run(t, []string{"copy"}, []string{"--current", "--to", "pi"}, map[string]string{"KOTO_SESSION": first.Destination.Selector})
	if code != 0 || result.Counts.Turns != 2 || result.Deferred.Events != 2 || result.Destination.Session == first.Destination.Session {
		t.Fatalf("L0 pending copy: %d %s", code, raw)
	}
	after, _ := os.ReadFile(file)
	if !bytes.Equal(before, after) {
		t.Fatal("Pi current source was modified")
	}
	for _, target := range nativeJSONLs(t, s.pi) {
		if target == file {
			continue
		}
		body, _ := os.ReadFile(target)
		if bytes.Contains(body, []byte("RUNNING REQUEST")) || bytes.Contains(body, []byte("invoke-koto")) {
			t.Fatal("L0 replay copied active invocation")
		}
	}
}

func TestProjectionCountsDoNotTreatToolsOrSummariesAsTurns(t *testing.T) {
	q := &core.Event{ID: "question", Kind: "message", Actor: "user"}
	a := &core.Event{ID: "answer", Kind: "message", Actor: "assistant"}
	tool := &core.Event{ID: "tool", Kind: "tool_result", Actor: "tool"}
	p := &planner.Projection{Items: []planner.Item{
		{Event: q, Role: "user"},
		{Event: q, Role: "user", Synthetic: "handoff summary"},
		{Event: tool, Role: "user", Parts: []core.Part{{"type": "image"}, {"type": "file"}}},
		{Event: tool, Role: "tool", Parts: []core.Part{{"type": "tool_result", "parts": []any{core.Part{"type": "image"}, core.Part{"type": "file"}}}}},
		{Event: a, Role: "assistant", Merged: []string{a.ID}},
	}}
	got := projectionCounts(p, []*core.Event{q, a, tool})
	if got != (copyCounts{Turns: 1, Messages: 2, Attachments: 4}) {
		t.Fatalf("counts include synthetic/tool turns: %+v", got)
	}
}

func TestCopyToolResultImageAndFile(t *testing.T) {
	s := newHandoffSandbox(t)
	cc := filepath.Join(s.root, "cc")
	file := filepath.Join(cc, "projects", "-synthetic", testThread+".jsonl")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{
		{"uuid": "q", "parentUuid": nil, "type": "user", "message": map[string]any{"role": "user", "content": "Read the attachment"}},
		{"uuid": "call", "parentUuid": "q", "type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "read", "name": "Read", "input": map[string]any{"file_path": "fixture"}}}}},
		{"uuid": "result", "parentUuid": "call", "type": "user", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "read", "content": []any{
			map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aVxkAAAAASUVORK5CYII="}},
			map[string]any{"type": "document", "title": "note.txt", "source": map[string]any{"type": "base64", "media_type": "text/plain", "data": "aGFuZG9mZiBhdHRhY2htZW50"}},
		}}}}},
		{"uuid": "a", "parentUuid": "result", "type": "assistant", "message": map[string]any{"role": "assistant", "content": "Read complete"}},
	}
	var data bytes.Buffer
	for _, row := range rows {
		row["sessionId"], row["cwd"], row["timestamp"] = testThread, s.cwd, time.Now().UTC().Format(time.RFC3339Nano)
		if err := json.NewEncoder(&data).Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(file, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	r, code, raw := s.run(t, []string{"copy", "claude-code:" + testThread}, []string{"--to", "pi", "--only", "claude-code,pi", "--claude-root", cc}, nil)
	if code != 0 || r.Counts.Turns != 1 || r.Counts.Attachments != 2 {
		t.Fatalf("tool attachment copy: %d %s", code, raw)
	}
	app, err := koto.New(koto.Config{DataDir: filepath.Join(s.root, "attachment-readback"), PiRoot: s.pi, Enable: []string{"pi"}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	report, err := app.Engine.Scan(context.Background(), engine.ScanOptions{})
	if err != nil || len(report.Errors) != 0 {
		t.Fatalf("readback: %v %+v", err, report)
	}
	b := app.Hub.Binding("pi", r.Destination.Session)
	text := core.ToJSON(app.Hub.Path(b.Leaf))
	if !strings.Contains(text, `"type":"image"`) || !strings.Contains(text, `"type":"file"`) || !strings.Contains(text, "Read complete") {
		t.Fatal("native readback lost tool image/file")
	}
	if after, _ := os.ReadFile(file); !bytes.Equal(after, data.Bytes()) {
		t.Fatal("source with tool attachments changed")
	}
}

// Exercise the actual positional/flag rearrangement and process exit, not just runHandoff.
func TestHandoffCLIProcess(t *testing.T) {
	if os.Getenv("KOTO_HANDOFF_TEST_CHILD") == "1" {
		for i, arg := range os.Args {
			if arg == "--" {
				os.Args = append([]string{"koto"}, os.Args[i+1:]...)
				main()
				return
			}
		}
		os.Exit(97)
	}
	s := newHandoffSandbox(t)
	s.writeCodex(t, testThread, "SOURCE", false)
	for _, test := range []struct {
		args []string
		code string
	}{
		{[]string{"copy", "--current", "--to", "foo", "--json"}, "COPY_REJECTED"},
		{[]string{"copy", "--current", "--to", "pi", "--bogus", "--json"}, "USAGE"},
		{[]string{"copy", "--current", "--json"}, "USAGE"},
		{[]string{"--json", "current"}, ""},
		{[]string{"copy", "codex:" + testThread, "--to=pi", "--dry-run", "--json"}, ""},
	} {
		args := append([]string{"-test.run=^TestHandoffCLIProcess$", "--"}, test.args...)
		args = append(args, s.flags...)
		cmd := exec.Command(os.Args[0], args...)
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "KOTO_SESSION=") && !strings.HasPrefix(value, "CODEX_THREAD_ID=") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		cmd.Env = append(cmd.Env, "KOTO_HANDOFF_TEST_CHILD=1", "CODEX_THREAD_ID="+testThread)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		raw, err := cmd.Output()
		var result handoffResult
		if decodeErr := json.Unmarshal(raw, &result); decodeErr != nil || stderr.Len() != 0 {
			t.Fatalf("invalid process output %v: %s stderr=%s", decodeErr, raw, stderr.String())
		}
		if test.code == "" {
			if err != nil || !result.OK {
				t.Fatalf("process should succeed: %v %s", err, raw)
			}
		} else if err == nil || result.OK || result.Error == nil || result.Error.Code != test.code {
			t.Fatalf("process expected %s with nonzero exit: %v %s", test.code, err, raw)
		}
	}
}
