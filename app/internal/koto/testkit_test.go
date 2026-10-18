package koto

// Test sandbox: every test gets fresh temp roots for the data and for each harness. Nothing here
// ever touches the real ~/.claude, ~/.codex or ~/.pi.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/hub"
)

const (
	CWD         = "/work/proj"
	CCSession   = "11111111-2222-4333-8444-555555555555"
	PISession   = "01a13866-f891-73d8-b45a-90076c4d7dd4"
	CodexThread = "01a0ec12-95a6-7c33-9ebd-b5e33eeb5c7c"
)

type sandbox struct {
	t                                                                *testing.T
	Root, Data, Claude, Pi, Codex, Shadow, Dsh, Agy, OpenCode, ZCode string
}

func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	s := &sandbox{t: t, Root: root, Data: filepath.Join(root, "hub"), Claude: filepath.Join(root, "claude"), Pi: filepath.Join(root, "pi"),
		Codex: filepath.Join(root, "codex"), Shadow: filepath.Join(root, "shadow"), Dsh: filepath.Join(root, "dsh"), Agy: filepath.Join(root, "agy"), OpenCode: filepath.Join(root, "opencode"), ZCode: filepath.Join(root, "zcode")}
	for _, d := range []string{s.Claude, s.Pi, s.Codex, s.Shadow, s.Dsh} {
		must(t, os.MkdirAll(d, 0o755))
	}
	return s
}

var zero int64

// app opens an App on the sandbox; edit tweaks the config (Enable, LiveMs…).
func (s *sandbox) app(edit ...func(*Config)) *App {
	s.t.Helper()
	cfg := Config{DataDir: s.Data, ClaudeRoot: s.Claude, PiRoot: s.Pi, CodexHome: s.Codex, CodexShadow: s.Shadow, DshHome: s.Dsh, AntigravityRoot: s.Agy, OpenCodeRoot: s.OpenCode, ZCodeRoot: s.ZCode, NoDesktop: true, LiveMs: &zero}
	cfg.WorkBuddyRoot, cfg.WorkBuddyAIRoot = filepath.Join(s.Root, "workbuddy"), filepath.Join(s.Root, "workbuddy-ai")
	cfg.CursorRoot = filepath.Join(s.Root, "cursor")
	for _, e := range edit {
		e(&cfg)
	}
	if on(cfg.Enable, "zcode") {
		initZCodeFixture(s.t, s.ZCode)
	}
	for _, variant := range []string{"workbuddy", "workbuddy-ai"} {
		if on(cfg.Enable, variant) {
			initWorkBuddyFixture(s.t, filepath.Join(s.Root, variant))
		}
	}
	a, err := New(cfg)
	must(s.t, err)
	s.t.Cleanup(a.Close)
	return a
}

func enable(ids ...string) func(*Config) { return func(c *Config) { c.Enable = ids } }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func eq[T comparable](t *testing.T, got, want T, what ...any) {
	t.Helper()
	if got != want {
		t.Fatalf("%v: got %v, want %v", fmt.Sprint(what...), got, want)
	}
}

func ok(t *testing.T, cond bool, what ...any) {
	t.Helper()
	if !cond {
		t.Fatal(what...)
	}
}

func jsonEq(t *testing.T, got, want any, what ...any) {
	t.Helper()
	g, w := core.Stringify(normalize(got)), core.Stringify(normalize(want))
	if g != w {
		t.Fatalf("%v:\n got  %s\n want %s", fmt.Sprint(what...), g, w)
	}
}

// normalize turns structs into JSON values for comparison.
func normalize(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var x any
	_ = json.Unmarshal(b, &x)
	return x
}

var ctx = context.Background()

func scan(t *testing.T, a *App) engine.ScanReport {
	t.Helper()
	r, err := a.Engine.Scan(ctx, engine.ScanOptions{})
	must(t, err)
	return r
}

func carry(t *testing.T, a *App, o engine.CarryOptions) *engine.CarryResult {
	t.Helper()
	r, err := a.Engine.Carry(ctx, o)
	must(t, err)
	if r.Applied {
		scan(t, a)
	}
	return r
}

// ------------------------------------------------------------------ files

func writeLines(t *testing.T, file string, recs []any) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(file), 0o755))
	var b strings.Builder
	for _, r := range recs {
		b.WriteString(core.Stringify(r))
		b.WriteByte('\n')
	}
	must(t, os.WriteFile(file, []byte(b.String()), 0o644))
}

func appendLines(t *testing.T, file string, recs []any) {
	t.Helper()
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_APPEND, 0)
	must(t, err)
	defer f.Close()
	for _, r := range recs {
		_, err := f.WriteString(core.Stringify(r) + "\n")
		must(t, err)
	}
}

func readJSONL(t *testing.T, file string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(file)
	must(t, err)
	var out []map[string]any
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		var m map[string]any
		must(t, json.Unmarshal([]byte(l), &m))
		out = append(out, m)
	}
	return out
}

func fixture(t *testing.T, name string, repl map[string]string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	must(t, err)
	s := string(b)
	for k, v := range repl {
		encoded, err := json.Marshal(v)
		must(t, err)
		// Placeholders live inside JSON strings, including paths on Windows.
		s = strings.ReplaceAll(s, k, string(encoded[1:len(encoded)-1]))
	}
	for i, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" && !json.Valid([]byte(line)) {
			t.Fatalf("fixture %s line %d is not valid JSON after substitution", name, i+1)
		}
	}
	return s
}

func TestFixtureWindowsPathRoundTrip(t *testing.T) {
	root := `C:\Users\测试 O'Brien\project\claude`
	content := fixture(t, "cc_session.jsonl", map[string]string{"{{SESSION}}": CCSession, "{{ROOT}}": root})
	found := false
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row map[string]any
		must(t, json.Unmarshal([]byte(line), &row))
		if path := core.S(core.Obj(row["toolUseResult"])["persistedOutputPath"]); path != "" {
			ok(t, strings.HasPrefix(path, root+"/"), "fixture preserves literal Windows backslashes and Unicode")
			found = true
		}
	}
	ok(t, found, "fixture has a persisted side-file path")
}

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]`)

// writeCcSession: a Claude Code session with parallel tool use (sibling-parented results), a
// persisted tool output, titles and a hook record.
func writeCcSession(t *testing.T, claudeRoot string, session string, persisted bool, extra ...any) string {
	t.Helper()
	if session == "" {
		session = CCSession
	}
	dir := filepath.Join(claudeRoot, "projects", nonAlnum.ReplaceAllString(CWD, "-"))
	big := filepath.Join(dir, session, "tool-results", "out1.txt")
	must(t, os.MkdirAll(filepath.Dir(big), 0o755))
	must(t, os.WriteFile(big, []byte(strings.Repeat("line\n", 5000)), 0o644))
	name := "cc_session.jsonl"
	if !persisted {
		name = "cc_session_plain.jsonl"
	}
	// The fixture was written with the root at {{ROOT}}; dir sits under claudeRoot as in the original.
	content := fixture(t, name, map[string]string{"{{SESSION}}": session, "{{ROOT}}": claudeRoot})
	file := filepath.Join(dir, session+".jsonl")
	must(t, os.WriteFile(file, []byte(content), 0o644))
	if len(extra) > 0 {
		appendLines(t, file, extra)
	}
	return file
}

func ts(n int) string {
	return time.Date(2026, 10, 15, 10, 0, n, 0, time.UTC).Format("2006-01-02T15:04:05.000Z")
}

func ccBase(session string) core.O {
	return core.O{{K: "isSidechain", V: false}, {K: "userType", V: "external"}, {K: "entrypoint", V: "cli"}, {K: "cwd", V: CWD}, {K: "sessionId", V: session}, {K: "version", V: "2.1.296"}, {K: "gitBranch", V: "main"}}
}

func rec(parts ...core.O) core.O {
	var o core.O
	for _, p := range parts {
		o = append(o, p...)
	}
	return o
}

// ccTurn is one user turn and its answer, appended under parent.
func ccTurn(session, parent string, n int, prefix string) []any {
	if prefix == "" {
		prefix = "x"
	}
	u, a := fmt.Sprintf("%su%d", prefix, n), fmt.Sprintf("%sa%d", prefix, n)
	b := ccBase(session)
	return []any{
		rec(core.O{{K: "parentUuid", V: parent}}, b, core.O{{K: "type", V: "user"}, {K: "message", V: core.O{{K: "role", V: "user"}, {K: "content", V: fmt.Sprintf("%s question %d", prefix, n)}}}, {K: "uuid", V: u}, {K: "timestamp", V: ts(100 + 2*n)}, {K: "promptId", V: fmt.Sprintf("%sp%d", prefix, n)}, {K: "promptSource", V: "typed"}, {K: "permissionMode", V: "default"}}),
		rec(core.O{{K: "parentUuid", V: u}}, b, core.O{{K: "type", V: "assistant"}, {K: "message", V: core.O{{K: "model", V: "claude-opus-5-5"}, {K: "id", V: fmt.Sprintf("msg_%s%d", prefix, n)}, {K: "type", V: "message"}, {K: "role", V: "assistant"}, {K: "content", V: []any{core.O{{K: "type", V: "text"}, {K: "text", V: fmt.Sprintf("%s answer %d", prefix, n)}}}}, {K: "stop_reason", V: "end_turn"}}}, {K: "uuid", V: a}, {K: "timestamp", V: ts(101 + 2*n)}}),
	}
}

func writePiSession(t *testing.T, piRoot, session string) string {
	t.Helper()
	if session == "" {
		session = PISession
	}
	file := filepath.Join(piRoot, "sessions", "--"+strings.ReplaceAll(CWD[1:], "/", "-")+"--", "2026-10-15T10-00-00-000Z_"+session+".jsonl")
	must(t, os.MkdirAll(filepath.Dir(file), 0o755))
	must(t, os.WriteFile(file, []byte(fixture(t, "pi_session.jsonl", map[string]string{"{{SESSION}}": session})), 0o644))
	return file
}

func codexRecords(t *testing.T, thread string) []map[string]any {
	t.Helper()
	if thread == "" {
		thread = CodexThread
	}
	var out []map[string]any
	for _, l := range strings.Split(fixture(t, "codex_rollout.jsonl", map[string]string{"{{THREAD}}": thread}), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		must(t, json.Unmarshal([]byte(l), &m))
		out = append(out, m)
	}
	return out
}

func writeCodexRollout(t *testing.T, codexHome, thread string, recs []map[string]any) string {
	t.Helper()
	if thread == "" {
		thread = CodexThread
	}
	if recs == nil {
		recs = codexRecords(t, thread)
	}
	file := filepath.Join(codexHome, "sessions", "2026", "10", "15", "rollout-2026-10-15T10-00-00-"+thread+".jsonl")
	var anys []any
	for _, r := range recs {
		anys = append(anys, r)
	}
	writeLines(t, file, anys)
	return file
}

func hasCodex() bool {
	return exec.Command("codex", "--version").Run() == nil
}

// kindsActors is the path as kind:actor strings.
func kindsActors(path []*core.Event) []string {
	var out []string
	for _, e := range path {
		out = append(out, e.Kind+":"+e.Actor)
	}
	return out
}

func binding(t *testing.T, h *hub.Hub, harness, session string) *hub.Binding {
	t.Helper()
	b := h.Binding(harness, session)
	if b == nil {
		t.Fatalf("no binding %s:%s", harness, session)
	}
	return b
}

func readLine(t *testing.T, l string) map[string]any {
	t.Helper()
	var m map[string]any
	must(t, json.Unmarshal([]byte(l), &m))
	return m
}

func newAPI(a *App) *API { return &API{App: a} }

func apiCode(err error) (int, string) {
	if err == nil {
		return 0, ""
	}
	e := AsAPIError(err)
	return e.Status, e.Code
}
