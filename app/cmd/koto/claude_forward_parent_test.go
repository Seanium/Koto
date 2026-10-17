package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyCurrentClaudeForwardAttachmentParent(t *testing.T) {
	s := newHandoffSandbox(t)
	claude := filepath.Join(s.root, "claude")
	s.flags = []string{"--json", "--data", filepath.Join(s.root, "hub"), "--claude-root", claude, "--pi-root", s.pi, "--only", "claude-code,pi"}
	file := filepath.Join(claude, "projects", "project", testThread+".jsonl")
	rows := []map[string]any{
		{"type": "attachment", "uuid": "ctx1", "parentUuid": "u1", "attachment": map[string]any{"type": "date"}, "rendered": []any{map[string]any{"content": "SOURCE SYSTEM CONTEXT"}}},
		{"type": "attachment", "uuid": "ctx2", "parentUuid": "ctx1", "attachment": map[string]any{"type": "permissions"}},
		{"type": "user", "uuid": "u1", "parentUuid": nil, "message": map[string]any{"role": "user", "content": "PRESERVED question"}},
		{"type": "assistant", "uuid": "a1", "parentUuid": "ctx2", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "PRESERVED answer"}}, "stop_reason": "end_turn"}},
		{"type": "user", "uuid": "u2", "parentUuid": "a1", "message": map[string]any{"role": "user", "content": "CURRENT MIGRATION REQUEST"}},
		{"type": "assistant", "uuid": "a2", "parentUuid": "u2", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "running", "name": "Bash", "input": map[string]any{"command": "koto copy --current --to pi"}}}, "stop_reason": "tool_use"}},
	}
	var data bytes.Buffer
	for _, row := range rows {
		row["cwd"], row["sessionId"], row["version"] = s.cwd, testThread, "2.1.300"
		if err := json.NewEncoder(&data).Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	r, code, raw := s.run(t, []string{"copy"}, []string{"--current", "--to", "pi"}, map[string]string{"CLAUDE_CODE_SESSION_ID": testThread})
	if code != 0 || r.State != "copied" || r.Counts.Turns != 1 || r.Counts.Messages != 2 || r.Deferred.Events != 2 {
		t.Fatalf("copy must retain the first question and exclude the active migration: %d %s", code, raw)
	}
	files := nativeJSONLs(t, s.pi)
	if len(files) != 1 {
		t.Fatalf("targets: %v", files)
	}
	native, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, wanted := range []string{"PRESERVED question", "PRESERVED answer"} {
		if !bytes.Contains(native, []byte(wanted)) {
			t.Fatalf("missing %s", wanted)
		}
	}
	for _, excluded := range []string{"CURRENT MIGRATION", "SOURCE SYSTEM CONTEXT", "running"} {
		if bytes.Contains(native, []byte(excluded)) {
			t.Fatalf("copied excluded content: %s", excluded)
		}
	}
	after, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(after, data.Bytes()) {
		t.Fatal("source changed", err)
	}
}
