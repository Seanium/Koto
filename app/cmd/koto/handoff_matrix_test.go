package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"koto/internal/connectors/dsh"
	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/koto"
)

var matrixAgents = []string{"claude-code", "pi", "codex", "dsh", "antigravity"}

// This is a CLI native-write/readback matrix. Codex destinations use the real app-server in
// an isolated HOME; no test runs a model or claims that a generated resume command was run.
func TestHandoffCLINativeMatrix(t *testing.T) {
	codexBin, _ := exec.LookPath("codex")
	for _, from := range matrixAgents {
		t.Run(from, func(t *testing.T) {
			s := newHandoffSandbox(t)
			cfg := koto.Config{DataDir: filepath.Join(s.root, "bootstrap"), ClaudeRoot: filepath.Join(s.root, "cc"), PiRoot: s.pi, CodexHome: s.codex, CodexShadow: filepath.Join(s.root, "shadow"), CodexBin: codexBin, DshHome: filepath.Join(s.root, "dsh"), AntigravityRoot: filepath.Join(s.root, "agy"), NoDesktop: true, Enable: matrixAgents}
			s.flags = append(s.flags, "--only", strings.Join(matrixAgents, ","), "--claude-root", cfg.ClaudeRoot, "--dsh-home", cfg.DshHome, "--agy-root", cfg.AntigravityRoot, "--codex-shadow", cfg.CodexShadow)
			if codexBin != "" {
				s.flags = append(s.flags, "--codex-bin", codexBin)
			}
			s.writeCodex(t, testThread, "MATRIX PRESERVED", false)
			sel := "codex:" + testThread
			app, err := koto.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if report, err := app.Engine.Scan(ctx, engine.ScanOptions{Connectors: []string{"codex"}}); err != nil || len(report.Errors) != 0 {
				t.Fatalf("bootstrap scan: %v %+v", err, report)
			}
			if from != "codex" {
				r, err := app.Engine.Carry(ctx, engine.CarryOptions{Selector: sel, To: from, Cwd: s.cwd, Title: "CLI matrix fixture"})
				if err != nil || !r.Applied {
					t.Fatalf("bootstrap %s: %v", from, err)
				}
				sel = from + ":" + r.Plan.Target.Session
			}
			native, _, err := discoverSource(ctx, app, sel, "explicit")
			if err != nil {
				t.Fatal(err)
			}
			app.Close()
			appendMatrixHandoff(t, native.ref)
			before, err := os.ReadFile(native.ref.Path)
			if err != nil {
				t.Fatal(err)
			}
			env := map[string]string{"KOTO_SESSION": sel}
			if key := map[string]string{"claude-code": "CLAUDE_CODE_SESSION_ID", "codex": "CODEX_THREAD_ID", "pi": "PI_SESSION_ID", "dsh": "DSH_SESSION_ID", "antigravity": "ANTIGRAVITY_CONVERSATION_ID"}[from]; key != "" {
				env = map[string]string{key: strings.TrimPrefix(sel, from+":")}
			}
			current, code, raw := s.run(t, []string{"current"}, nil, env)
			if code != 0 || current.Source.Selector != sel {
				t.Fatalf("current source: %d %s", code, raw)
			}
			for _, to := range matrixAgents {
				t.Run("to-"+to, func(t *testing.T) {
					if to == "codex" && codexBin == "" {
						t.Skip("Codex native app-server unavailable; this target was not tested")
					}
					preview, code, raw := s.run(t, []string{"copy"}, []string{"--current", "--to", to, "--dry-run"}, env)
					if code != 0 || preview.State != "preview" || preview.Counts.Turns != 2 || preview.Counts.Attachments != 1 || preview.Deferred.Events < 4 {
						t.Fatalf("preview: %d %s", code, raw)
					}
					result, code, raw := s.run(t, []string{"copy"}, []string{"--current", "--to", to}, env)
					if code != 0 || !result.OK || result.State != "copied" || result.Counts.Turns != 2 || result.Resume == nil || result.Resume.Shell == "" || result.Resume.Cwd == nil || *result.Resume.Cwd != s.cwd || result.Destination.Selector == sel {
						t.Fatalf("copy: %d %s", code, raw)
					}
					if raw, _ := os.ReadFile(native.ref.Path); !bytes.Equal(raw, before) {
						t.Fatal("source native session changed")
					}
					readCfg := cfg
					readCfg.DataDir = filepath.Join(s.root, "readback-"+to)
					readback, err := koto.New(readCfg)
					if err != nil {
						t.Fatal(err)
					}
					defer readback.Close()
					ref, refs, err := discoverSource(ctx, readback, result.Destination.Selector, "explicit")
					if err != nil {
						t.Fatal(err)
					}
					if err := loadSource(ctx, readback.Hub, ref, refs, map[string]bool{}); err != nil {
						t.Fatal(err)
					}
					b := readback.Hub.Binding(to, result.Destination.Session)
					if b == nil {
						t.Fatal("destination native session did not load")
					}
					path := readback.Hub.Path(b.Leaf)
					body := core.ToJSON(path)
					for _, expected := range []string{"MATRIX PRESERVED question one", "MATRIX PRESERVED answer two", `"type":"image"`} {
						if !strings.Contains(body, expected) {
							t.Fatalf("native readback missing %s", expected)
						}
					}
					for _, excluded := range []string{"MATRIX MIGRATION REQUEST", "MATRIX CLARIFICATION", "matrix-current", "matrix-copy", "PRIVATE SYSTEM CONTEXT"} {
						if strings.Contains(body, excluded) {
							t.Fatalf("native readback leaked %s", excluded)
						}
					}
					if readback.Hub.TurnsAt(b.Leaf) != 2 {
						t.Fatalf("native target has %d turns", readback.Hub.TurnsAt(b.Leaf))
					}
					t.Logf("native readback passed %s -> %s: 2 turns, 1 image; resume generated (not executed)", from, to)
				})
			}
		})
	}
}

// Add two user messages inside one unfinished turn using each Agent's native format. The
// first request and its clarification must both disappear, including on same-Agent L0 copies.
func appendMatrixHandoff(t *testing.T, ref core.NativeRef) {
	t.Helper()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	var rows []map[string]any
	texts := []string{"MATRIX MIGRATION REQUEST", "MATRIX CLARIFICATION"}
	ids := []string{"matrix-current", "matrix-copy"}
	switch ref.Harness {
	case "codex":
		for i, text := range texts {
			rows = append(rows, map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": text}}}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call", "name": "exec_command", "call_id": ids[i], "arguments": `{"cmd":"koto copy --current --to pi"}`}})
		}
		raw, _ := os.ReadFile(ref.Path)
		for i, row := range rows {
			row["ordinal"] = bytes.Count(raw, []byte("\n")) + i
		}
	case "claude-code", "pi":
		raw, _ := os.ReadFile(ref.Path)
		lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
		var last map[string]any
		if err := json.Unmarshal(lines[len(lines)-1], &last); err != nil {
			t.Fatal(err)
		}
		parent := core.S(last["id"])
		if ref.Harness == "claude-code" {
			parent = core.S(last["uuid"])
		}
		for i, text := range texts {
			uid := ids[i] + "-user"
			if ref.Harness == "claude-code" {
				rows = append(rows, map[string]any{"type": "user", "uuid": uid, "parentUuid": parent, "sessionId": ref.Session, "message": map[string]any{"role": "user", "content": text}}, map[string]any{"type": "assistant", "uuid": ids[i], "parentUuid": uid, "sessionId": ref.Session, "message": map[string]any{"role": "assistant", "stop_reason": "tool_use", "content": []any{map[string]any{"type": "tool_use", "id": ids[i], "name": "Bash", "input": map[string]any{"command": "koto copy --current --to pi"}}}}})
			} else {
				rows = append(rows, map[string]any{"type": "message", "id": uid, "parentId": parent, "message": map[string]any{"role": "user", "content": text}}, map[string]any{"type": "message", "id": ids[i], "parentId": uid, "message": map[string]any{"role": "assistant", "stopReason": "toolUse", "content": []any{map[string]any{"type": "toolCall", "id": ids[i], "name": "bash", "arguments": map[string]any{"command": "koto copy --current --to pi"}}}}})
			}
			parent = ids[i]
		}
	case "dsh":
		state, err := dsh.ReadState(ref.Path, nil)
		if err != nil {
			t.Fatal(err)
		}
		for i, text := range texts {
			rows = append(rows, map[string]any{"type": "user/message", "data": map[string]any{"id": ids[i] + "-user", "role": "user", "source": map[string]any{"kind": "user"}, "content": []any{map[string]any{"type": "text", "text": text}}}}, map[string]any{"type": "assistant/message", "data": map[string]any{"message": map[string]any{"id": ids[i], "role": "assistant", "content": []any{map[string]any{"type": "tool-call", "id": ids[i], "name": "bash", "arguments": `{"command":"koto copy --current --to pi"}`}}}}})
		}
		for i, row := range rows {
			row["seq"], row["time"], row["surfaceOp"] = state.Seq+i, time.Now().UnixMilli(), "append"
		}
	case "antigravity":
		db, err := sql.Open("sqlite", "file:"+ref.Path+"?mode=rw")
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var idx int
		if err := db.QueryRow("SELECT COALESCE(MAX(idx),-1)+1 FROM steps").Scan(&idx); err != nil {
			t.Fatal(err)
		}
		for i, text := range texts {
			user := matrixPB(19, matrixPB(2, []byte(text)))
			call := append(matrixPB(1, []byte(ids[i])), matrixPB(2, []byte("run_command"))...)
			call = append(call, matrixPB(3, []byte(`{"CommandLine":"koto copy --current --to pi"}`))...)
			model := matrixPB(20, matrixPB(7, call))
			for j, row := range []struct {
				typ  int
				body []byte
			}{{14, user}, {15, model}} {
				if _, err := db.Exec("INSERT INTO steps(idx,step_type,status,step_payload) VALUES(?,?,3,?)", idx+i*2+j, row.typ, row.body); err != nil {
					t.Fatal(err)
				}
			}
		}
		return
	default:
		t.Fatalf("unsupported matrix source %s", ref.Harness)
	}
	var tail bytes.Buffer
	for _, row := range rows {
		row["timestamp"] = stamp
		if err := json.NewEncoder(&tail).Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	data := tail.Bytes()
	if ref.Harness == "dsh" {
		data = core.ZstdCompress(data)
	}
	f, err := os.OpenFile(ref.Path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func matrixPB(field int, body []byte) []byte {
	var key, size [binary.MaxVarintLen64]byte
	n, m := binary.PutUvarint(key[:], uint64(field<<3|2)), binary.PutUvarint(size[:], uint64(len(body)))
	return append(append(append([]byte{}, key[:n]...), size[:m]...), body...)
}
