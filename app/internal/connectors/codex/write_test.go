package codex

import (
	"encoding/json"
	"koto/internal/core"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeRolloutRead(t *testing.T) {
	bin, e := exec.LookPath("codex")
	if e != nil {
		t.Skip("no codex")
	}
	root := t.TempDir()
	home := filepath.Join(root, "codex")
	id := core.UUID5(root)
	now := time.Now().UTC()
	file := filepath.Join(home, "sessions", now.Format("2006/01/02"), "rollout-"+now.Format("2006-01-02T15-04-05")+"-"+id+".jsonl")
	os.MkdirAll(filepath.Dir(file), 0700)
	records := []map[string]any{}
	add := func(typ string, p any) {
		records = append(records, map[string]any{"timestamp": now.Format(time.RFC3339Nano), "ordinal": len(records), "type": typ, "payload": p})
	}
	add("session_meta", map[string]any{"id": id, "timestamp": now.Format(time.RFC3339Nano), "cwd": root, "originator": "koto", "cli_version": "0.0.0", "source": "cli"})
	add("event_msg", map[string]any{"type": "task_started", "turn_id": "copy-1"})
	add("event_msg", map[string]any{"type": "user_message", "message": "Native sentinel", "images": []string{}, "local_images": []string{}, "text_elements": []any{}})
	add("response_item", map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Native sentinel"}}})
	add("event_msg", map[string]any{"type": "agent_message", "message": "Native answer"})
	add("response_item", map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Native answer"}}})
	add("event_msg", map[string]any{"type": "task_complete", "turn_id": "copy-1"})
	var body []byte
	for _, r := range records {
		b, _ := json.Marshal(r)
		body = append(append(body, b...), '\n')
	}
	os.WriteFile(file, body, 0600)
	s := &AppServer{Bin: bin, Env: map[string]string{"HOME": root, "CODEX_HOME": home}, IdleMs: 60000, Timeout: 30 * time.Second}
	defer s.Stop()
	b, e := s.Call("thread/read", map[string]any{"threadId": id, "includeTurns": true})
	if e != nil {
		t.Fatal(e)
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	th := core.Obj(m["thread"])
	if len(core.Arr(th["turns"])) != 1 {
		t.Fatal("missing initial turn")
	}
	s.Stop()
	b, e = s.Call("thread/read", map[string]any{"threadId": id, "includeTurns": true})
	if e != nil {
		t.Fatal(e)
	}
	json.Unmarshal(b, &m)
	th = core.Obj(m["thread"])
	if len(core.Arr(th["turns"])) != 1 {
		t.Fatal("missing imported turn")
	}
}
