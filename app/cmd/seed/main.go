// Seed a sandbox for app development: synthetic native sessions for Claude Code, pi and Codex
// under <sandbox>/{claude,pi,codex}, and independent copies with different lengths so every
// screen has something to show. Never touches the real ~/.claude, ~/.pi, ~/.codex, ~/.dsh.
//
//	go run ./cmd/seed [sandbox-dir]     (default app/.sandbox; wiped first)
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"koto/internal/connectors/pi"
	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/koto"
)

type O = core.O
type KV = core.KV

var (
	root string
	now  = time.Now()
)

func j(d ...string) string { return filepath.Join(append([]string{root}, d...)...) }

func iso(minAgo float64, s int) string {
	return now.Add(-time.Duration(minAgo*float64(time.Minute)) + time.Duration(s)*time.Second).UTC().Format("2006-01-02T15:04:05.000Z")
}

func ms(minAgo float64, s int) int64 {
	return now.Add(-time.Duration(minAgo*float64(time.Minute)) + time.Duration(s)*time.Second).UnixMilli()
}

func lines(file string, recs []any, appendTo bool) {
	must(os.MkdirAll(filepath.Dir(file), 0o755))
	var b strings.Builder
	for _, r := range recs {
		b.WriteString(core.Stringify(r) + "\n")
	}
	flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if appendTo {
		flag = os.O_WRONLY | os.O_APPEND
	}
	f, err := os.OpenFile(file, flag, 0o644)
	must(err)
	_, err = f.WriteString(b.String())
	must(err)
	must(f.Close())
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func merge(parts ...O) O {
	var o O
	for _, p := range parts {
		o = append(o, p...)
	}
	return o
}

// ---------------------------------------------------------------- Claude Code

type tool struct {
	name  string
	input O
	out   string
}

type turn struct {
	user  string
	tools []tool
	reply string
}

type ccSess struct {
	file, session, title string
	base                 O
	recs                 []any
	last                 string
}

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]`)

func ccSession(session, cwd, title string, minAgo float64, turns []turn, tag string) *ccSess {
	base := O{{K: "isSidechain", V: false}, {K: "userType", V: "external"}, {K: "entrypoint", V: "cli"}, {K: "cwd", V: cwd}, {K: "sessionId", V: session}, {K: "version", V: "2.1.296"}, {K: "gitBranch", V: "main"}}
	recs, last := ccRecords(base, turns, minAgo, nil, tag)
	return &ccSess{file: j("claude", "projects", nonAlnum.ReplaceAllString(cwd, "-"), session+".jsonl"), session: session, title: title, base: base, recs: recs, last: last}
}

func ccRecords(base O, turns []turn, minAgo float64, parent any, tag string) ([]any, string) {
	var recs []any
	prev := parent
	for i, t := range turns {
		s := i * 40
		u := fmt.Sprintf("%su%d", tag, i)
		recs = append(recs, merge(O{{K: "parentUuid", V: prev}}, base, O{{K: "type", V: "user"}, {K: "message", V: O{{K: "role", V: "user"}, {K: "content", V: t.user}}}, {K: "uuid", V: u}, {K: "timestamp", V: iso(minAgo, s)}, {K: "promptId", V: fmt.Sprintf("%sp%d", tag, i)}, {K: "promptSource", V: "typed"}, {K: "permissionMode", V: "default"}}))
		prev = u
		// Real sessions interleave hidden harness context with messages. Keep this in
		// UI fixtures so selecting a turn exercises context filtering during copying.
		contextID := fmt.Sprintf("%sctx%d", tag, i)
		recs = append(recs, merge(O{{K: "parentUuid", V: prev}}, base, O{{K: "type", V: "attachment"}, {K: "attachment", V: O{{K: "type", V: "date"}}}, {K: "rendered", V: []any{O{{K: "content", V: "koto sandbox harness context"}}}}, {K: "uuid", V: contextID}, {K: "timestamp", V: iso(minAgo, s+1)}}))
		prev = contextID
		for k, tl := range t.tools {
			id := fmt.Sprintf("toolu_%s%d_%d", tag, i, k)
			tu, tr := fmt.Sprintf("%st%d_%d", tag, i, k), fmt.Sprintf("%sr%d_%d", tag, i, k)
			recs = append(recs,
				merge(O{{K: "parentUuid", V: prev}}, base, O{{K: "type", V: "assistant"}, {K: "message", V: O{{K: "model", V: "model-a"}, {K: "id", V: fmt.Sprintf("msg_%s%dt%d", tag, i, k)}, {K: "type", V: "message"}, {K: "role", V: "assistant"}, {K: "content", V: []any{O{{K: "type", V: "tool_use"}, {K: "id", V: id}, {K: "name", V: tl.name}, {K: "input", V: tl.input}}}}, {K: "stop_reason", V: "tool_use"}}}, {K: "uuid", V: tu}, {K: "timestamp", V: iso(minAgo, s+5+k)}}),
				merge(O{{K: "parentUuid", V: tu}}, base, O{{K: "type", V: "user"}, {K: "message", V: O{{K: "role", V: "user"}, {K: "content", V: []any{O{{K: "type", V: "tool_result"}, {K: "tool_use_id", V: id}, {K: "content", V: tl.out}}}}}}, {K: "uuid", V: tr}, {K: "timestamp", V: iso(minAgo, s+6+k)}, {K: "promptId", V: fmt.Sprintf("%sp%d", tag, i)}, {K: "permissionMode", V: "default"}}))
			prev = tr
		}
		a := fmt.Sprintf("%sa%d", tag, i)
		recs = append(recs, merge(O{{K: "parentUuid", V: prev}}, base, O{{K: "type", V: "assistant"}, {K: "message", V: O{{K: "model", V: "model-a"}, {K: "id", V: fmt.Sprintf("msg_%s%d", tag, i)}, {K: "type", V: "message"}, {K: "role", V: "assistant"}, {K: "content", V: []any{O{{K: "type", V: "text"}, {K: "text", V: t.reply}}}}, {K: "stop_reason", V: "end_turn"}}}, {K: "uuid", V: a}, {K: "timestamp", V: iso(minAgo, s+12)}}))
		prev = a
	}
	return recs, prev.(string)
}

const imgSession = "7a8b9ca0-7f8e-4091-9ca3-b4c5d6e7f806"
const manySession = "8b9cadb0-808f-41a2-8db4-c5d6e7f80907"

// pngB64 is a small synthetic screenshot (a toolbar, a button, some text lines) as base64 PNG.
func pngB64(w, h, variant int) string {
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	fill := func(x0, y0, x1, y1 int, c color.RGBA) {
		for y := y0; y < y1 && y < h; y++ {
			for x := x0; x < x1 && x < w; x++ {
				im.Set(x, y, c)
			}
		}
	}
	fill(0, 0, w, h, color.RGBA{247, 247, 248, 255})
	fill(0, 0, w, 28, color.RGBA{230, 231, 235, 255})
	for i := range 4 {
		fill(20, 50+i*22, w-60-i*30, 58+i*22, color.RGBA{200, 202, 208, 255})
	}
	bc := color.RGBA{36, 99, 235, 255}
	if variant == 1 {
		bc = color.RGBA{16, 160, 110, 255}
	}
	fill(20, h-56, 140, h-24, bc)
	fill(150, h-50+variant*4, 260, h-30+variant*4, color.RGBA{120, 122, 130, 255})
	var b bytes.Buffer
	must(png.Encode(&b, im))
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

// desktopify marks a session as written from Claude Desktop's Code tab.
func desktopify(s *ccSess) {
	set := func(o O) O {
		for i := range o {
			if o[i].K == "entrypoint" {
				o[i].V = "claude-desktop"
			}
		}
		return o
	}
	s.base = set(append(O{}, s.base...))
	for i, r := range s.recs {
		s.recs[i] = set(r.(O))
	}
}

func bash(cmd, out string) tool { return tool{"Bash", O{{K: "command", V: cmd}}, out} }

// ---------------------------------------------------------------- pi

var piUsage = O{{K: "input", V: 1}, {K: "output", V: 1}, {K: "cacheRead", V: 0}, {K: "cacheWrite", V: 0}, {K: "totalTokens", V: 2}, {K: "cost", V: O{{K: "input", V: 0}, {K: "output", V: 0}, {K: "cacheRead", V: 0}, {K: "cacheWrite", V: 0}, {K: "total", V: 0}}}}

func piSession(session, cwd, name string, minAgo float64, turns []turn) {
	t := func(s int) KV { return KV{K: "timestamp", V: iso(minAgo, s)} }
	recs := []any{O{{K: "type", V: "session"}, {K: "version", V: 3}, {K: "id", V: session}, t(0), {K: "cwd", V: cwd}}, O{{K: "type", V: "model_change"}, {K: "id", V: "m0"}, {K: "parentId", V: nil}, t(0), {K: "provider", V: "acme"}, {K: "modelId", V: "acme-large"}}}
	prev := "m0"
	asst := func(content []any, stop string, s int) O {
		return O{{K: "role", V: "assistant"}, {K: "content", V: content}, {K: "api", V: "openai-responses"}, {K: "provider", V: "acme"}, {K: "model", V: "acme-large"}, {K: "usage", V: piUsage}, {K: "stopReason", V: stop}, {K: "timestamp", V: ms(minAgo, s)}}
	}
	for i, tt := range turns {
		s := 1 + i*30
		u := fmt.Sprintf("u%d", i)
		recs = append(recs, O{{K: "type", V: "message"}, {K: "id", V: u}, {K: "parentId", V: prev}, t(s), {K: "message", V: O{{K: "role", V: "user"}, {K: "content", V: []any{O{{K: "type", V: "text"}, {K: "text", V: tt.user}}}}, {K: "timestamp", V: ms(minAgo, s)}}}})
		prev = u
		for _, tl := range tt.tools {
			call := fmt.Sprintf("call_%d|fc_%d", i, i)
			c, r := fmt.Sprintf("c%d", i), fmt.Sprintf("r%d", i)
			recs = append(recs,
				O{{K: "type", V: "message"}, {K: "id", V: c}, {K: "parentId", V: prev}, t(s + 2), {K: "message", V: asst([]any{O{{K: "type", V: "toolCall"}, {K: "id", V: call}, {K: "name", V: tl.name}, {K: "arguments", V: tl.input}}}, "toolUse", s+2)}},
				O{{K: "type", V: "message"}, {K: "id", V: r}, {K: "parentId", V: c}, t(s + 4), {K: "message", V: O{{K: "role", V: "toolResult"}, {K: "toolCallId", V: call}, {K: "toolName", V: tl.name}, {K: "content", V: []any{O{{K: "type", V: "text"}, {K: "text", V: tl.out}}}}, {K: "isError", V: false}, {K: "timestamp", V: ms(minAgo, s+4)}}}})
			prev = r
		}
		a := fmt.Sprintf("a%d", i)
		recs = append(recs, O{{K: "type", V: "message"}, {K: "id", V: a}, {K: "parentId", V: prev}, t(s + 6), {K: "message", V: asst([]any{O{{K: "type", V: "text"}, {K: "text", V: tt.reply}}}, "stop", s+6)}})
		prev = a
	}
	recs = append(recs, O{{K: "type", V: "session_info"}, {K: "id", V: "si"}, {K: "parentId", V: prev}, t(200), {K: "name", V: name}})
	stamp := strings.NewReplacer(":", "-", ".", "-").Replace(iso(minAgo, 0))
	lines(j("pi", "sessions", pi.CwdDir(cwd), stamp+"_"+session+".jsonl"), recs, false)
}

// ---------------------------------------------------------------- Codex

func codexRollout(thread, cwd string, minAgo float64, user, cmd, out, reply string) {
	o := 0
	line := func(typ string, payload O, s int) any {
		r := O{{K: "timestamp", V: iso(minAgo, s)}, {K: "ordinal", V: o}, {K: "type", V: typ}, {K: "payload", V: payload}}
		o++
		return r
	}
	args, _ := json.Marshal(map[string]string{"cmd": cmd, "workdir": cwd})
	recs := []any{
		line("session_meta", O{{K: "session_id", V: thread}, {K: "id", V: thread}, {K: "timestamp", V: iso(minAgo, 0)}, {K: "cwd", V: cwd}, {K: "originator", V: "codex_cli_rs"}, {K: "cli_version", V: "0.162.1"}, {K: "source", V: "cli"}, {K: "model_provider", V: "acme"}, {K: "base_instructions", V: O{{K: "text", V: "You are Codex."}}}, {K: "history_mode", V: "paginated"}}, 0),
		line("event_msg", O{{K: "type", V: "task_started"}, {K: "turn_id", V: "t1"}}, 0),
		line("turn_context", O{{K: "turn_id", V: "t1"}, {K: "cwd", V: cwd}, {K: "approval_policy", V: "never"}, {K: "sandbox_policy", V: O{{K: "type", V: "read-only"}}}, {K: "model", V: "acme-coder"}, {K: "effort", V: "high"}}, 1),
		line("response_item", O{{K: "type", V: "message"}, {K: "id", V: "m3"}, {K: "role", V: "user"}, {K: "content", V: []any{O{{K: "type", V: "input_text"}, {K: "text", V: user}}}}, {K: "internal_chat_message_metadata_passthrough", V: O{{K: "content_item_kinds", V: []any{"user.text"}}}}}, 2),
		line("response_item", O{{K: "type", V: "function_call"}, {K: "id", V: "fc_1"}, {K: "name", V: "exec_command"}, {K: "arguments", V: string(args)}, {K: "call_id", V: "call_a"}}, 4),
		line("response_item", O{{K: "type", V: "function_call_output"}, {K: "id", V: "fco_1"}, {K: "call_id", V: "call_a"}, {K: "output", V: out}}, 5),
		line("response_item", O{{K: "type", V: "message"}, {K: "id", V: "m4"}, {K: "role", V: "assistant"}, {K: "content", V: []any{O{{K: "type", V: "output_text"}, {K: "text", V: reply}}}}}, 6),
		line("event_msg", O{{K: "type", V: "task_complete"}, {K: "turn_id", V: "t1"}}, 6),
	}
	d := now.Add(-time.Duration(minAgo * float64(time.Minute))).UTC()
	name := "rollout-" + strings.ReplaceAll(iso(minAgo, 0)[:19], ":", "-") + "-" + thread + ".jsonl"
	lines(j("codex", "sessions", d.Format("2006"), d.Format("01"), d.Format("02"), name), recs, false)
}

// ---------------------------------------------------------------- main

func main() {
	root = filepath.Join(must2(os.Getwd()), ".sandbox")
	if len(os.Args) > 1 {
		root = must2(filepath.Abs(os.Args[1]))
	}
	if _, err := os.Stat(root); err == nil {
		if _, err := os.Stat(filepath.Join(root, ".koto-sandbox")); err != nil {
			panic("refusing to erase an unmarked directory: " + root)
		}
	}
	must(os.RemoveAll(root))
	for _, d := range []string{"hub", "claude", "pi", "codex", "codex-shadow", "dsh", "antigravity", "opencode"} {
		must(os.MkdirAll(j(d), 0o755))
	}
	must(os.WriteFile(j(".koto-sandbox"), []byte("synthetic fixtures\n"), 0600))
	// Project folders the sessions point at (they must exist, or copies refuse the cwd).
	work := j("work")
	for _, d := range []string{"koto", "web-app", "etl", "storage"} {
		must(os.MkdirAll(filepath.Join(work, d), 0o755))
	}

	cc1 := ccSession("6b1f2a40-1c2d-4e3f-8a4b-5c6d7e8f9a01", work+"/koto", "把 CAS 打包改成按天分片", 50, []turn{
		{"看一下 hub 的 CAS 现在怎么打包的", []tool{bash(`rg -n "pack" src/hub/cas.ts`, "12:export function pack(\n48:  const packed = zstd(blob)")}, "CAS 现在把所有 blob 打进一个 pack 文件，按大小滚动。"},
		{"改成按天分片，旧的 pack 保留只读", []tool{{"Edit", O{{K: "file_path", V: work + "/koto/src/hub/cas.ts"}, {K: "old_string", V: "packFile()"}, {K: "new_string", V: "packFile(day)"}}, "The file has been updated."}}, "已改为 packs/YYYY-MM-DD.pack，旧文件不动。"},
		{"跑一下测试", []tool{bash("npm test", "# tests 47\n# pass 47\n# fail 0")}, "47 个测试全部通过。"},
	}, "a")
	// 登录页闪烁 was started in Claude Desktop's Code tab (same files as the CLI, entrypoint claude-desktop).
	cc2 := ccSession("9c3e4d50-2b3a-4c5d-9e6f-7a8b9c0d1e02", work+"/web-app", "登录页闪烁", 180, []turn{
		{"登录页首屏会闪一下白，查一下", []tool{{"Read", O{{K: "file_path", V: work + "/web-app/src/Login.tsx"}}, "export function Login() {\n  const theme = useTheme()\n  …"}}, "主题在 hydrate 之后才读 localStorage，所以先渲染了浅色。"},
		{"在 <head> 里内联一段脚本先设 data-theme", nil, "加好了，首屏不再闪。"},
	}, "W")
	desktopify(cc2)
	// Three independent native sessions with an identical prefix and two genuine content forks.
	common := []turn{{"整理登录状态的处理流程", nil, "分为初始化、登录和退出三个阶段。"}, {"先实现初始化检查", nil, "启动时读取本地状态，检查过期时间。"}}
	for i, suffix := range [][]turn{
		{{"使用 cookie 保存状态", nil, "采用 HttpOnly cookie。"}, {"补上退出登录", nil, "退出时清理 cookie 并更新页面。"}},
		{{"使用 token 保存状态", nil, "将 token 保存在内存中。"}, {"增加自动过期处理", nil, "到期后清空内存并返回登录页。"}},
		{{"使用 token 保存状态", nil, "将 token 保存在内存中。"}, {"改为手动退出", nil, "仅在点击退出时清空内存。"}},
	} {
		c := ccSession(fmt.Sprintf("ab128321-3465-4cc1-ab11-%012d", i+1), work+"/web-app", "对比分叉 · "+[]string{"Cookie", "Token 过期", "手动退出"}[i], 200+float64(i), append(append([]turn{}, common...), suffix...), fmt.Sprintf("cmp%d_", i))
		lines(c.file, append(c.recs, O{{K: "type", V: "custom-title"}, {K: "customTitle", V: c.title}, {K: "sessionId", V: c.session}}), false)
	}
	// A long one (64 turns): code blocks, long replies, runs of tool calls.
	var long []turn
	for i := range 64 {
		n := i + 1
		st := work + "/storage/src/hub/hub.ts"
		switch {
		case n%9 == 0:
			long = append(long, turn{fmt.Sprintf("第 %d 步：把 %s 的读路径换成游标", n, []string{"index", "cas", "events", "refs"}[n%4]), []tool{
				bash(`rg -n "readAll" src/hub`, "src/hub/hub.ts:812: readAll()"),
				{"Read", O{{K: "file_path", V: st}}, "export class Hub {"},
				{"Edit", O{{K: "file_path", V: st}, {K: "old_string", V: "readAll()"}, {K: "new_string", V: "cursor()"}}, "updated"},
				bash("npm test", "# pass 112\n# fail 0"),
			}, "换好了，112 个测试通过。"})
		case n%7 == 0:
			var b []string
			for k := range 42 {
				b = append(b, fmt.Sprintf("%d. 迁移第 %d 条：旧表 t%d 按天拆分，保留只读副本，写入走新路径。", k+1, k+1, k))
			}
			long = append(long, turn{fmt.Sprintf("第 %d 步：给迁移写一段说明", n), nil, strings.Join(b, "\n")})
		case n%5 == 0:
			long = append(long, turn{fmt.Sprintf("第 %d 步：写一个按天分片的辅助函数", n), nil, "可以这样写：\n\n```ts\nexport function dayOf(ms: number): string {\n  const d = new Date(ms)\n  return d.toISOString().slice(0, 10)\n}\n\nexport function shardPath(root: string, ms: number) {\n  return path.join(root, 'packs', dayOf(ms) + '.pack')\n}\n```\n\n调用方只需要传时间戳。"})
		default:
			long = append(long, turn{fmt.Sprintf("第 %d 步：继续整理存储层，处理 %s", n, []string{"写入", "读取", "压缩", "校验"}[n%4]), []tool{bash(fmt.Sprintf("npm test -- --grep step%d", n), fmt.Sprintf("# pass %d", n))}, fmt.Sprintf("第 %d 步完成。", n)})
		}
	}
	cc3 := ccSession("3d5f6e70-4c5b-4d6e-8f70-8192a3b4c503", work+"/storage", "重构存储层", 900, long, "L")
	for _, s := range []*ccSess{cc1, cc2, cc3} {
		lines(s.file, append(s.recs, O{{K: "type", V: "custom-title"}, {K: "customTitle", V: s.title}, {K: "sessionId", V: s.session}}), false)
	}
	// The long one has been edited and resent twice: at turn 10 (three turns since) and at turn 40.
	b10, _ := ccRecords(cc3.base, []turn{
		{"第 10 步换个思路：先给读路径加缓存", []tool{bash("npm test -- --grep cache", "# pass 8")}, "缓存层加好了。"},
		{"缓存失效怎么处理", nil, "按 pack 文件的 mtime 失效。"},
		{"压测一下", []tool{bash("npm run bench", "p99 4.1ms")}, "p99 从 9 ms 降到 4 ms。"},
	}, 120, "La8", "B")
	b40, _ := ccRecords(cc3.base, []turn{
		{"第 40 步：迁移说明改成表格", nil, "| 旧表 | 新路径 |\n|---|---|\n| t0 | packs/day |"},
		{"再补一列回滚方式", nil, "加好了。"},
	}, 60, "La38", "C")
	lines(cc3.file, append(b10, b40...), true)

	// A stress case: 60 turns with 40 edited-and-resent branches along the way, one or two turns each.
	var stress []turn
	for i := range 60 {
		stress = append(stress, turn{fmt.Sprintf("压测第 %d 轮：调整参数 %d", i+1, i), nil, fmt.Sprintf("第 %d 轮完成。", i+1)})
	}
	cc4 := ccSession("5e6f7a80-5d6c-4e7f-9a81-92a3b4c5d604", work+"/storage", "分支压测", 600, stress, "S")
	lines(cc4.file, append(cc4.recs, O{{K: "type", V: "custom-title"}, {K: "customTitle", V: cc4.title}, {K: "sessionId", V: cc4.session}}), false)
	var branches []any
	for k := range 40 {
		at := 1 + (k*37)%58 // spread over the turns
		n := 1 + k%2
		var ts []turn
		for j := range n {
			ts = append(ts, turn{fmt.Sprintf("分支 %d：从第 %d 轮换个做法（%d）", k+1, at+1, j+1), nil, "好的。"})
		}
		rs, _ := ccRecords(cc4.base, ts, 500-float64(k*10), fmt.Sprintf("Sa%d", at-1), fmt.Sprintf("K%d_", k))
		branches = append(branches, rs...)
	}
	lines(cc4.file, branches, true)

	// Fifteen versions side by side from one turn: the prompt of turn 6 edited and resent 15 times.
	var fan []turn
	for i := range 8 {
		fan = append(fan, turn{fmt.Sprintf("扇形第 %d 轮：准备数据 %d", i+1, i), nil, fmt.Sprintf("第 %d 轮好了。", i+1)})
	}
	cc5 := ccSession("6f7a8b90-6e7d-4f80-8b92-a3b4c5d6e705", work+"/etl", "同一轮分出 15 个版本", 400, fan, "F")
	lines(cc5.file, append(cc5.recs, O{{K: "type", V: "custom-title"}, {K: "customTitle", V: cc5.title}, {K: "sessionId", V: cc5.session}}), false)
	var fans []any
	for k := range 15 {
		n := 1 + k%3
		var ts []turn
		for j := range n {
			ts = append(ts, turn{fmt.Sprintf("方案 %d：第 6 轮换个问法（%d）", k+1, j+1), nil, fmt.Sprintf("方案 %d 的回答。", k+1)})
		}
		rs, _ := ccRecords(cc5.base, ts, 300-float64(k*8), "Fa4", fmt.Sprintf("G%d_", k))
		fans = append(fans, rs...)
	}
	lines(cc5.file, fans, true)

	// Pictures: a screenshot pasted into Claude Code, and one the agent read with a tool; a pi
	// session with a picture in the prompt.
	shot1, shot2 := pngB64(320, 200, 0), pngB64(480, 300, 1)
	imgBase := O{{K: "isSidechain", V: false}, {K: "userType", V: "external"}, {K: "entrypoint", V: "cli"}, {K: "cwd", V: work + "/web-app"}, {K: "sessionId", V: imgSession}, {K: "version", V: "2.1.296"}, {K: "gitBranch", V: "main"}}
	img := func(b64 string) O {
		return O{{K: "type", V: "image"}, {K: "source", V: O{{K: "type", V: "base64"}, {K: "media_type", V: "image/png"}, {K: "data", V: b64}}}}
	}
	imgRecs := []any{
		merge(O{{K: "parentUuid", V: nil}}, imgBase, O{{K: "type", V: "user"}, {K: "message", V: O{{K: "role", V: "user"}, {K: "content", V: []any{img(shot1), O{{K: "type", V: "document"}, {K: "title", V: "验收说明.txt"}, {K: "source", V: O{{K: "type", V: "text"}, {K: "media_type", V: "text/plain"}, {K: "data", V: "koto 附件预览验收\n原始 Unicode 内容：αβγ🙂\n点击预览时才读取正文。"}}}}, O{{K: "type", V: "text"}, {K: "text", V: "这个按钮的对齐是不是有问题？截图在上面"}}}}}}, {K: "uuid", V: "Iu0"}, {K: "timestamp", V: iso(70, 0)}, {K: "promptId", V: "Ip0"}, {K: "permissionMode", V: "default"}}),
		merge(O{{K: "parentUuid", V: "Iu0"}}, imgBase, O{{K: "type", V: "assistant"}, {K: "message", V: O{{K: "model", V: "model-a"}, {K: "id", V: "msg_I0t"}, {K: "type", V: "message"}, {K: "role", V: "assistant"}, {K: "content", V: []any{O{{K: "type", V: "tool_use"}, {K: "id", V: "toolu_I0"}, {K: "name", V: "Read"}, {K: "input", V: O{{K: "file_path", V: work + "/web-app/docs/design.png"}}}}}}, {K: "stop_reason", V: "tool_use"}}}, {K: "uuid", V: "It0"}, {K: "timestamp", V: iso(70, 5)}}),
		merge(O{{K: "parentUuid", V: "It0"}}, imgBase, O{{K: "type", V: "user"}, {K: "message", V: O{{K: "role", V: "user"}, {K: "content", V: []any{O{{K: "type", V: "tool_result"}, {K: "tool_use_id", V: "toolu_I0"}, {K: "content", V: []any{img(shot2)}}}}}}}, {K: "uuid", V: "Ir0"}, {K: "timestamp", V: iso(70, 6)}, {K: "promptId", V: "Ip0"}, {K: "permissionMode", V: "default"}}),
		merge(O{{K: "parentUuid", V: "Ir0"}}, imgBase, O{{K: "type", V: "assistant"}, {K: "message", V: O{{K: "model", V: "model-a"}, {K: "id", V: "msg_I0"}, {K: "type", V: "message"}, {K: "role", V: "assistant"}, {K: "content", V: []any{O{{K: "type", V: "text"}, {K: "text", V: "对比设计稿，按钮的基线比文字低了 2px：把 align-items 改成 baseline 就对齐了。"}}}}, {K: "stop_reason", V: "end_turn"}}}, {K: "uuid", V: "Ia0"}, {K: "timestamp", V: iso(70, 12)}}),
		O{{K: "type", V: "custom-title"}, {K: "customTitle", V: "按钮对齐（带截图）"}, {K: "sessionId", V: imgSession}},
	}
	lines(j("claude", "projects", nonAlnum.ReplaceAllString(work+"/web-app", "-"), imgSession+".jsonl"), imgRecs, false)

	// Fourteen screenshots in one message.
	many := []any{O{{K: "type", V: "text"}, {K: "text", V: "这 14 张是各页面的截图，挑出排版有问题的"}}}
	for i := range 14 {
		many = append(many, img(pngB64(200+i*10, 130+i*6, i%2)))
	}
	manyBase := append(O{}, imgBase...)
	for i := range manyBase {
		if manyBase[i].K == "sessionId" {
			manyBase[i].V = manySession
		}
	}
	lines(j("claude", "projects", nonAlnum.ReplaceAllString(work+"/web-app", "-"), manySession+".jsonl"), []any{
		merge(O{{K: "parentUuid", V: nil}}, manyBase, O{{K: "type", V: "user"}, {K: "message", V: O{{K: "role", V: "user"}, {K: "content", V: many}}}, {K: "uuid", V: "Mu0"}, {K: "timestamp", V: iso(65, 0)}, {K: "promptId", V: "Mp0"}, {K: "permissionMode", V: "default"}}),
		merge(O{{K: "parentUuid", V: "Mu0"}}, manyBase, O{{K: "type", V: "assistant"}, {K: "message", V: O{{K: "model", V: "model-a"}, {K: "id", V: "msg_M0"}, {K: "type", V: "message"}, {K: "role", V: "assistant"}, {K: "content", V: []any{O{{K: "type", V: "text"}, {K: "text", V: "第 3、7、12 张的按钮没对齐，第 9 张标题换行了。"}}}}, {K: "stop_reason", V: "end_turn"}}}, {K: "uuid", V: "Ma0"}, {K: "timestamp", V: iso(65, 8)}}),
		O{{K: "type", V: "custom-title"}, {K: "customTitle", V: "十四张页面截图"}, {K: "sessionId", V: manySession}},
	}, false)

	const piOrders = "01a13866-f891-73d8-b45a-90076c4d7d01"
	piSession(piOrders, work+"/etl", "订单表迁移脚本", 95, []turn{
		{"数一下 migrations 目录里有几个脚本", []tool{{"bash", O{{K: "command", V: "ls migrations | wc -l"}}, "14"}}, "一共 14 个迁移脚本。"},
		{"第 15 个：给 orders 加 shipped_at 列", []tool{{"write", O{{K: "path", V: "migrations/015_orders_shipped_at.sql"}, {K: "content", V: "ALTER TABLE orders ADD COLUMN shipped_at timestamptz;"}}, "Wrote 1 line"}}, "已写入 015_orders_shipped_at.sql。"},
	})
	// pi: a picture in the prompt.
	piImg := "01a13866-f891-73d8-b45a-90076c4d7d0f"
	pt := func(s int) KV { return KV{K: "timestamp", V: iso(40, s)} }
	lines(j("pi", "sessions", pi.CwdDir(work+"/etl"), strings.NewReplacer(":", "-", ".", "-").Replace(iso(40, 0))+"_"+piImg+".jsonl"), []any{
		O{{K: "type", V: "session"}, {K: "version", V: 3}, {K: "id", V: piImg}, pt(0), {K: "cwd", V: work + "/etl"}},
		O{{K: "type", V: "message"}, {K: "id", V: "pu0"}, {K: "parentId", V: nil}, pt(1), {K: "message", V: O{{K: "role", V: "user"}, {K: "content", V: []any{O{{K: "type", V: "text"}, {K: "text", V: "这张报表图里的峰值是哪天？"}}, O{{K: "type", V: "image"}, {K: "data", V: pngB64(360, 220, 1)}, {K: "mimeType", V: "image/png"}}}}, {K: "timestamp", V: ms(40, 1)}}}},
		O{{K: "type", V: "message"}, {K: "id", V: "pa0"}, {K: "parentId", V: "pu0"}, pt(5), {K: "message", V: O{{K: "role", V: "assistant"}, {K: "content", V: []any{O{{K: "type", V: "text"}, {K: "text", V: "峰值在周三，比平时高约 40%。"}}}}, {K: "api", V: "openai-responses"}, {K: "provider", V: "acme"}, {K: "model", V: "acme-large"}, {K: "usage", V: piUsage}, {K: "stopReason", V: "stop"}, {K: "timestamp", V: ms(40, 5)}}}},
		O{{K: "type", V: "session_info"}, {K: "id", V: "si"}, {K: "parentId", V: "pa0"}, pt(6), {K: "name", V: "报表峰值（带图）"}},
	}, false)
	codexRollout("01a0ec12-95a6-7c33-9ebd-b5e33eeb5c03", work+"/web-app", 300, "跑一遍 lint 并修掉报错", "npm run lint -- --fix", "✔ 0 problems", "lint 已经干净了。")

	// ---------------------------------------------------------------- copies
	zero := int64(0)
	app, err := koto.New(koto.Config{DataDir: j("hub"), ClaudeRoot: j("claude"), PiRoot: j("pi"), CodexHome: j("codex"), CodexShadow: j("codex-shadow"),
		DshHome: j("dsh"), AntigravityRoot: j("antigravity"), OpenCodeRoot: j("opencode"), NoDesktop: true, LiveMs: &zero, Enable: []string{"claude-code", "pi", "codex", "dsh"}})
	must(err)
	defer app.Close()
	api := &koto.API{App: app}
	ctx := context.Background()
	r := api.Reindex(ctx)
	fmt.Printf("index: %d sessions, %d errors\n", r.Sessions, len(r.Errors))
	cp := func(sel, to string) *engine.CarryResult {
		res, err := api.Carry(ctx, koto.CarryRequest{Selector: sel, To: to})
		must(err)
		fmt.Printf("copy %s → %s: %s %s\n", sel, to, res.Plan.Mode, res.Plan.Target.Session)
		return res
	}

	// 1. CAS (CC) → pi; one more CC turn copied over; then both sides go on from the same turn: two versions.
	c1 := cp("claude-code:"+cc1.session, "pi")
	extra, extraLast := ccRecords(cc1.base, []turn{{"顺便把旧 pack 的读取路径也测一下", []tool{bash("node --test test/cas.test.ts", "# pass 6\n# fail 0")}, "旧 pack 读取正常，6 个用例通过。"}}, 20, cc1.last, "b")
	lines(cc1.file, extra, true)
	api.Reindex(ctx)
	cp("claude-code:"+cc1.session, "pi")
	piFile := c1.Plan.Target.Path
	data, _ := os.ReadFile(piFile)
	ls := strings.Split(strings.TrimSpace(string(data)), "\n")
	var last map[string]any
	_ = json.Unmarshal([]byte(ls[len(ls)-1]), &last)
	lines(piFile, []any{
		O{{K: "type", V: "message"}, {K: "id", V: "pf-u"}, {K: "parentId", V: last["id"]}, {K: "timestamp", V: iso(8, 0)}, {K: "message", V: O{{K: "role", V: "user"}, {K: "content", V: []any{O{{K: "type", V: "text"}, {K: "text", V: "按天分片后，冷数据用 zstd 19 级再压一遍"}}}}, {K: "timestamp", V: ms(8, 0)}}}},
		O{{K: "type", V: "message"}, {K: "id", V: "pf-a"}, {K: "parentId", V: "pf-u"}, {K: "timestamp", V: iso(7, 0)}, {K: "message", V: O{{K: "role", V: "assistant"}, {K: "content", V: []any{O{{K: "type", V: "text"}, {K: "text", V: "加了 compactColdPacks()：7 天前的 pack 重新以 zstd -19 压缩，体积降了 31%。"}}}}, {K: "api", V: "openai-responses"}, {K: "provider", V: "acme"}, {K: "model", V: "acme-large"}, {K: "usage", V: piUsage}, {K: "stopReason", V: "stop"}, {K: "timestamp", V: ms(7, 0)}}}},
	}, true)
	fork, _ := ccRecords(cc1.base, []turn{{"给按天分片补一个迁移：把旧 pack 拆到对应日期", []tool{bash("node scripts/split-packs.ts --dry-run", "would split 3 packs into 41 day files")}, "迁移脚本写好了，试运行会把 3 个旧 pack 拆成 41 个按天的文件。"}}, 4, extraLast, "c")
	lines(cc1.file, fork, true)

	// 2. 登录页闪烁 (CC) → dsh once; then two more CC turns, so the dsh copy is behind.
	cp("claude-code:"+cc2.session, "dsh")
	more, _ := ccRecords(cc2.base, []turn{
		{"暗色模式下按钮边框太亮了", nil, "把 --border 在暗色下改成 #333336。"},
		{"顺便检查一下 Safari", []tool{bash("npx playwright test --project=webkit login", "3 passed")}, "Safari（WebKit）下 3 个登录用例都通过。"},
	}, 30, cc2.last, "d")
	lines(cc2.file, more, true)

	// 3. 订单表迁移脚本 (pi) → Claude Code.
	cp("pi:"+piOrders, "claude-code")

	r = api.Reindex(ctx)
	fmt.Printf("reindex: %d sessions; %d conversations\n", r.Sessions, len(api.Threads(0)))
	fmt.Println("sandbox ready:", root)
}

func must2[T any](v T, err error) T {
	must(err)
	return v
}
