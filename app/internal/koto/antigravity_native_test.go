//go:build agy_native

package koto

// Opt-in acceptance against the user's installed CLI. Every destination has a fresh ID;
// historical native conversation files are hashed before and after. No historical ID is resumed.
import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"koto/internal/core"
	"koto/internal/engine"
)

func TestAntigravityNativeResume(t *testing.T) {
	root, bin, out := os.Getenv("KOTO_AGY_TEST_ROOT"), os.Getenv("KOTO_AGY_TEST_BIN"), os.Getenv("KOTO_AGY_TEST_OUTPUT")
	if root == "" || bin == "" || out == "" {
		t.Fatal("explicit KOTO_AGY_TEST_ROOT, KOTO_AGY_TEST_BIN and KOTO_AGY_TEST_OUTPUT required")
	}
	must(t, os.MkdirAll(out, 0700))
	cwd := filepath.Join(out, "work")
	must(t, os.MkdirAll(cwd, 0700))
	originals := map[string]string{}
	ents, err := os.ReadDir(filepath.Join(root, "conversations"))
	must(t, err)
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		p := filepath.Join(root, "conversations", e.Name())
		b, err := os.ReadFile(p)
		must(t, err)
		originals[p] = core.SHA256Hex(b)
	}
	t.Cleanup(func() {
		for p, sha := range originals {
			b, err := os.ReadFile(p)
			if err != nil || core.SHA256Hex(b) != sha {
				t.Errorf("historical file changed: %s", p)
			}
		}
		t.Logf("checked %d pre-existing native files", len(originals))
	})
	for _, targetSource := range []string{"claude-code", "pi", "codex", "dsh"} {
		t.Run(targetSource, func(t *testing.T) {
			s := newSandbox(t)
			s.Data = filepath.Join(out, targetSource+"-hub")
			file := writeCcSession(t, s.Claude, "", true)
			// Add long Unicode history, a tool result, and markers in distinct turns.
			for i := 2; i <= 8; i++ {
				parent := "a5"
				if i > 2 {
					parent = "xa" + itoa(i-1)
				}
				appendLines(t, file, ccTurn(CCSession, parent, i, ""))
			}
			// A completed final turn with two independently checkable history markers.
			stamp := time.Now().Add(-time.Hour).Format(time.RFC3339)
			appendLines(t, file, []any{map[string]any{"type": "user", "uuid": "native-final-u", "parentUuid": "xa8", "sessionId": CCSession, "timestamp": stamp, "cwd": cwd, "message": map[string]any{"role": "user", "content": "koto 原生导入验收。项目代号：雨燕-42。历史数据：" + strings.Repeat("Unicode 测试 αβγ🙂 | ", 120) + "。不要运行历史工具。"}}, map[string]any{"type": "assistant", "uuid": "native-final-a", "parentUuid": "native-final-u", "sessionId": CCSession, "timestamp": stamp, "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "已记录项目代号。验收口令：violet-729。"}}}}})
			a := s.app(enable("claude-code", "pi", "codex", "dsh", "antigravity"), func(c *Config) { c.AntigravityRoot = root })
			scan(t, a)
			source := "claude-code:" + CCSession
			if targetSource != "claude-code" {
				r := carry(t, a, engine.CarryOptions{Selector: source, To: targetSource, Cwd: cwd})
				sid := r.Plan.Target.Session
				if x := core.S(r.Receipt["session"]); x != "" {
					sid = x
				}
				source = targetSource + ":" + sid
				scan(t, a)
			}
			r := carry(t, a, engine.CarryOptions{Selector: source, To: "antigravity", Cwd: cwd, Title: "koto 导入验收 · " + targetSource})
			id := r.Plan.Target.Session
			ok(t, originals[r.Plan.Target.Path] == "", "must be a newly created session")
			entry := core.ToJSON(map[string]any{"id": id, "path": r.Plan.Target.Path, "from": targetSource}) + "\n"
			manifest, err := os.OpenFile(filepath.Join(out, "created.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			must(t, err)
			_, err = manifest.WriteString(entry)
			must(t, err)
			must(t, manifest.Close())
			for attempt := 0; attempt < 2; attempt++ {
				liveCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
				defer cancel()
				cmd := exec.CommandContext(liveCtx, bin, "--conversation", id, "--print", "第 "+itoa(attempt+1)+" 次 koto 验收。不要调用任何工具。只根据已有的历史对话回复项目代号和验收口令。", "--output-format", "json", "--print-timeout", "60s")
				cmd.Dir = cwd
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				raw, err := cmd.Output()
				must(t, os.WriteFile(filepath.Join(out, targetSource+"-stderr-"+itoa(attempt)+".txt"), stderr.Bytes(), 0600))
				must(t, os.WriteFile(filepath.Join(out, targetSource+"-resume-"+itoa(attempt)+".json"), raw, 0600))
				must(t, err)
				var result struct {
					ConversationID string `json:"conversation_id"`
					Status         string `json:"status"`
					Response       string `json:"response"`
				}
				must(t, json.Unmarshal(raw, &result))
				eq(t, result.ConversationID, id, "native CLI must resume the requested imported ID, not start another session")
				eq(t, result.Status, "SUCCESS")
				ok(t, strings.Contains(result.Response, "雨燕-42") && strings.Contains(result.Response, "violet-729"), result.Response)
			}
			t.Logf("%s → Antigravity: %s resumed and recalled both historical markers", targetSource, id)
		})
	}
}

// Proves the installed CLI actually gives copied image bytes to its model, not just that our
// own decoder can read them. Only the new imported conversation is resumed.
func TestAntigravityNativeImage(t *testing.T) {
	agyNativeMaterial(t, materials(t)[0], "不要调用任何工具，也不要读取本地文件。只看历史对话中的图片，回答图片的主要颜色，只输出中文颜色名称。", "红")
}
func TestAntigravityNativePDF(t *testing.T) {
	agyNativeMaterial(t, material{"报告.pdf", "application/pdf", pdfMaterial(), false}, "不要调用工具或读取本地文件。历史对话附带的 PDF 写了什么？只输出 PDF 中的文字。如果看不到，请明确说明。", "koto-native-pdf-729")
}
func agyNativeMaterial(t *testing.T, im material, question, expected string) {
	root, bin, out := os.Getenv("KOTO_AGY_TEST_ROOT"), os.Getenv("KOTO_AGY_TEST_BIN"), os.Getenv("KOTO_AGY_TEST_OUTPUT")
	if root == "" || bin == "" || out == "" {
		t.Fatal("explicit native-test roots required")
	}
	must(t, os.MkdirAll(out, 0700))
	s := newSandbox(t)
	source := materialSource(t, s, []material{im})
	before := text(t, source)
	a := s.app(enable("antigravity"))
	scan(t, a)
	// The source stays in the sandbox; the explicitly added destination is the real native root.
	_, err := a.AddRoot("antigravity", root)
	must(t, err)
	dest := ""
	for _, c := range a.Connectors() {
		if c.Root == root {
			dest = c.ID
		}
	}
	r := carry(t, a, engine.CarryOptions{Selector: "antigravity:materials", To: dest, Cwd: out, Title: "koto 图片复制验收"})
	id := r.Plan.Target.Session
	must(t, os.WriteFile(filepath.Join(out, "image-session.json"), []byte(core.ToJSON(r.Receipt)), 0600))
	a.Close()
	liveCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(liveCtx, bin, "--conversation", id, "--print", question, "--output-format", "json", "--print-timeout", "60s")
	cmd.Dir = out
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	must(t, os.WriteFile(filepath.Join(out, "image-response.json"), raw, 0600))
	must(t, os.WriteFile(filepath.Join(out, "image-stderr.txt"), stderr.Bytes(), 0600))
	must(t, err)
	var result struct {
		ConversationID string `json:"conversation_id"`
		Status         string `json:"status"`
		Response       string `json:"response"`
	}
	must(t, json.Unmarshal(raw, &result))
	eq(t, result.ConversationID, id)
	eq(t, result.Status, "SUCCESS")
	ok(t, strings.Contains(result.Response, expected), result.Response)
	eq(t, text(t, source), before)
	t.Log("native attachment recognized:", result.Response)
}
