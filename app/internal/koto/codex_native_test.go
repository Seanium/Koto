//go:build codex_native

package koto

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"koto/internal/core"
	"koto/internal/engine"
)

func TestCodexNativeImage(t *testing.T) {
	root, bin, out := os.Getenv("KOTO_CODEX_TEST_ROOT"), os.Getenv("KOTO_CODEX_TEST_BIN"), os.Getenv("KOTO_CODEX_TEST_OUTPUT")
	if root == "" || bin == "" || out == "" {
		t.Fatal("explicit native-test roots required")
	}
	must(t, os.MkdirAll(out, 0700))
	s := newSandbox(t)
	materialSource(t, s, materials(t)[:1])
	a := s.app(enable("antigravity", "codex"), func(c *Config) { c.CodexHome = root })
	scan(t, a)
	r := carry(t, a, engine.CarryOptions{Selector: "antigravity:materials", To: "codex", Cwd: out, Title: "koto 图片复制验收"})
	id := core.S(r.Receipt["session"])
	must(t, os.WriteFile(filepath.Join(out, "session.json"), []byte(core.ToJSON(r.Receipt)), 0600))
	a.Close()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "exec", "resume", "--skip-git-repo-check", id, "不要调用任何工具或读取本地文件。只看历史会话中附带的图片，回答其主要颜色，只输出中文颜色名称。", "--json", "--output-last-message", filepath.Join(out, "answer.txt"))
	cmd.Dir = out
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, e := cmd.Output()
	must(t, os.WriteFile(filepath.Join(out, "events.jsonl"), raw, 0600))
	must(t, os.WriteFile(filepath.Join(out, "stderr.txt"), stderr.Bytes(), 0600))
	must(t, e)
	answer, e := os.ReadFile(filepath.Join(out, "answer.txt"))
	must(t, e)
	ok(t, strings.Contains(string(answer), "红"), string(answer))
	t.Log("native image recognized:", string(answer))
}
