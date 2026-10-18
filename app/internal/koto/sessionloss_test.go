package koto

// Session-loss audit (2026-10-16): each failure mode the audit reproduced, now asserting the safe
// outcome. Sandbox only: temp dirs and synthetic fixtures.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
)

const S2 = "22222222-2222-4333-8444-555555555555"

func piFilesIn(root string) []string { return jsonlFiles(nil, filepath.Join(root, "sessions"), "") }

func threadTexts(a *App) string {
	var b strings.Builder
	for _, th := range a.Hub.Threads() {
		for _, e := range a.Hub.ThreadEvents(th.ID) {
			b.WriteString(core.Stringify(e.Parts))
		}
	}
	return b.String()
}

func userPi(text string) string {
	return core.Stringify(core.O{{K: "type", V: "message"}, {K: "id", V: "user0001"}, {K: "parentId", V: nil}, {K: "timestamp", V: "2026-10-16T00:00:00.000Z"}, {K: "message", V: core.O{{K: "role", V: "user"}, {K: "content", V: text}, {K: "timestamp", V: 0}}}}) + "\n"
}

func appendRaw(t *testing.T, file, s string) {
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_APPEND, 0)
	must(t, err)
	_, err = f.WriteString(s)
	must(t, err)
	must(t, f.Close())
}

func TestSessionLossUnparseableLine(t *testing.T) {
	s := newSandbox(t)
	ccFile := writeCcSession(t, s.Claude, "", false)
	a := s.app(enable("claude-code", "pi"))
	scan(t, a)
	carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi"})
	piFile := piFilesIn(s.Pi)[0]
	appendRaw(t, piFile, "{\"broken\": \n")
	scan(t, a)
	appendLines(t, ccFile, ccTurn(CCSession, "a5", 1, ""))
	damaged := text(t, piFile)
	r, err := a.Engine.Carry(ctx, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi"})
	if err == nil {
		ok(t, r.Plan.Target.Path != piFile, "delivered elsewhere, if at all")
	}
	eq(t, text(t, piFile), damaged)
}

func TestSessionLossCreateNeverReplaces(t *testing.T) {
	s := newSandbox(t)
	file := filepath.Join(s.Pi, "sessions", "x", "target.jsonl")
	shared.BeforeLink = func(f string) { _ = os.WriteFile(f, []byte(`{"user":"their own session"}`+"\n"), 0o644) }
	defer func() { shared.BeforeLink = nil }()
	_, err := shared.ApplyFilePlan(&engine.DeliveryPlan{ID: "x", Sink: "pi", Harness: "pi", Thread: "th:x", Head: "ev", Level: "L2", Mode: "create",
		Target: engine.DeliveryTarget{Mode: "create", Session: "s", Path: file}, Content: `{"koto":"ours"}` + "\n"})
	ok(t, err != nil && strings.Contains(err.Error(), "appeared"), err)
	eq(t, text(t, file), `{"user":"their own session"}`+"\n")
	ents, _ := os.ReadDir(filepath.Dir(file))
	eq(t, len(ents), 1, "no tmp left behind")
}

func lastRec(t *testing.T, f string) map[string]any {
	r := readJSONL(t, f)
	return r[len(r)-1]
}

func TestSessionLossOpenIdleSession(t *testing.T) {
	s := newSandbox(t)
	h := startHolder(t, "")
	ccFile := writeCcSession(t, s.Claude, "", true)
	a := s.app(enable("claude-code", "pi"))
	scan(t, a)
	thread := binding(t, a.Hub, "claude-code", CCSession).Thread
	// a pi session continues the thread
	toPi := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi"})
	piFile := toPi.Plan.Target.Path
	appendLines(t, piFile, []any{piMsg("p1u", core.S(lastRec(t, piFile)["id"]), time.Date(2026, 10, 15, 14, 0, 0, 0, time.UTC), core.O{{K: "role", V: "user"}, {K: "content", V: "from pi"}, {K: "timestamp", V: 1}})})
	// Claude Code has the CC session open (its registry entry), idle; the file is hours old
	must(t, os.MkdirAll(filepath.Join(s.Claude, "sessions"), 0o755))
	reg := filepath.Join(s.Claude, "sessions", fmt.Sprintf("%d.json", h.pid))
	must(t, os.WriteFile(reg, []byte(core.Stringify(map[string]any{"pid": h.pid, "sessionId": CCSession, "cwd": "/work/proj", "status": "idle", "kind": "interactive"})), 0o644))
	old := time.Now().Add(-5 * time.Hour)
	must(t, os.Chtimes(ccFile, old, old))
	before := text(t, ccFile)
	strict := s.app(enable("claude-code", "pi"))
	scan(t, strict)
	piSel := "pi:" + core.S(toPi.Receipt["session"])
	deferred := carry(t, strict, engine.CarryOptions{Selector: piSel, To: "claude-code"})
	ok(t, deferred.Applied && deferred.Plan.Target.Session != CCSession, "copying never writes into the open session")
	eq(t, text(t, ccFile), before, "nothing appended under the open session")
	dry := carry(t, strict, engine.CarryOptions{Selector: thread, To: "claude-code", DryRun: true})
	eq(t, dry.Plan.Mode, "create")

	// the registry entry is for another session (the instance switched): appends again
	must(t, os.WriteFile(reg, []byte(core.Stringify(map[string]any{"pid": h.pid, "sessionId": S2, "status": "idle"})), 0o644))
	r := carry(t, strict, engine.CarryOptions{Selector: piSel, To: "claude-code"})
	jsonEq(t, []string{r.Plan.Mode, r.Plan.Target.Session}, []string{"create", r.Plan.Target.Session})
	ok(t, !strings.Contains(text(t, ccFile), "from pi"))
}

func TestSessionLossCodexRegistrationFails(t *testing.T) {
	if !hasCodex() {
		t.Skip("codex CLI not installed")
	}
	s := newSandbox(t)
	ccFile := writeCcSession(t, s.Claude, "", false)
	cwd := filepath.Join(s.Root, "work")
	must(t, os.Mkdir(cwd, 0o755))
	a := s.app(enable("claude-code", "codex"))
	scan(t, a)
	r1 := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "codex", Cwd: cwd})
	original := text(t, core.S(r1.Receipt["path"]))
	a.Close()

	// A server that initializes but rejects registering the new native file.
	fake := filepath.Join(s.Root, "fake-codex")
	if runtime.GOOS == "windows" {
		fake += ".exe"
	}
	source := filepath.Join(s.Root, "fake-codex.go")
	must(t, os.WriteFile(source, []byte(`package main
import ("bufio"; "encoding/json"; "os")
func main() {
  scanner := bufio.NewScanner(os.Stdin)
  encoder := json.NewEncoder(os.Stdout)
  for scanner.Scan() {
    var request map[string]any
    if json.Unmarshal(scanner.Bytes(), &request) != nil || request["id"] == nil { continue }
    response := map[string]any{"id": request["id"]}
    if request["method"] == "initialize" { response["result"] = map[string]any{} } else {
      response["error"] = map[string]any{"code": -1, "message": "registration failed: boom"}
    }
    encoder.Encode(response)
  }
}
`), 0o600))
	if output, err := exec.Command("go", "build", "-o", fake, source).CombinedOutput(); err != nil {
		t.Fatalf("build rejecting app-server: %v: %s", err, output)
	}
	appendLines(t, ccFile, ccTurn(CCSession, "a5", 1, ""))
	bad := s.app(enable("claude-code", "codex"), func(c *Config) { c.CodexBin = fake })
	scan(t, bad)
	_, err := bad.Engine.Carry(ctx, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "codex", Cwd: cwd})
	ok(t, err != nil && regexp.MustCompile(`boom|did not accept`).MatchString(err.Error()), err)
	eq(t, text(t, core.S(r1.Receipt["path"])), original, "failed new copy must not alter a prior session")
	bad.Close()

	good := s.app(enable("claude-code", "codex"))
	scan(t, good)
	r2 := carry(t, good, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "codex", Cwd: cwd})
	eq(t, r2.Plan.Mode, "create")
	ok(t, r2.Applied)
	ok(t, strings.Contains(text(t, core.S(r2.Receipt["path"])), "x question 1"))
}

func TestSessionLossPiV1NeverAppended(t *testing.T) {
	s := newSandbox(t)
	v1 := filepath.Join(s.Pi, "sessions", "--work-proj--", "2026-01-01T00-00-00-000Z_0aaaaaaa-0000-4000-8000-000000000001.jsonl")
	writeLines(t, v1, []any{
		core.O{{K: "type", V: "session"}, {K: "id", V: "0aaaaaaa-0000-4000-8000-000000000001"}, {K: "timestamp", V: "2026-01-01T00:00:00.000Z"}, {K: "cwd", V: "/work/proj"}},
		core.O{{K: "type", V: "message"}, {K: "timestamp", V: "2026-01-01T00:00:01.000Z"}, {K: "message", V: core.O{{K: "role", V: "user"}, {K: "content", V: "old v1 question"}, {K: "timestamp", V: 1}}}},
		core.O{{K: "type", V: "message"}, {K: "timestamp", V: "2026-01-01T00:00:02.000Z"}, {K: "message", V: core.O{{K: "role", V: "assistant"}, {K: "content", V: []any{map[string]any{"type": "text", "text": "old v1 answer"}}}, {K: "api", V: "openai-responses"}, {K: "provider", V: "acme"}, {K: "model", V: "m"}, {K: "usage", V: piUsage()}, {K: "stopReason", V: "stop"}, {K: "timestamp", V: 2}}}},
	})
	before := text(t, v1)
	a := s.app(enable("claude-code", "pi"))
	scan(t, a)
	toCc := carry(t, a, engine.CarryOptions{Selector: "pi:0aaaaaaa", To: "claude-code"})
	ccFile := toCc.Plan.Target.Path
	appendLines(t, ccFile, ccTurn(toCc.Plan.Target.Session, core.S(lastRec(t, ccFile)["uuid"]), 1, ""))
	back := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + toCc.Plan.Target.Session, To: "pi"})
	eq(t, back.Plan.Mode, "create")
	eq(t, text(t, v1), before, "the v1 file is untouched")
	recs := readJSONL(t, back.Plan.Target.Path)
	eq(t, recs[0]["version"], any(float64(3)))
	all := text(t, back.Plan.Target.Path)
	ok(t, strings.Contains(all, "old v1 question") && strings.Contains(all, "x question 1"))
}

func TestSessionLossPiFreshIDs(t *testing.T) {
	s := newSandbox(t)
	ccFile := writeCcSession(t, s.Claude, "", true)
	a := s.app(enable("claude-code", "pi"))
	scan(t, a)
	r := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi"})
	file := r.Plan.Target.Path
	// the user's own pi records happen to use ids koto would mint next (8 hex chars, same scheme)
	appendLines(t, ccFile, ccTurn(CCSession, "a5", 1, ""))
	scan(t, a)
	dry := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi", DryRun: true})
	var minted []string
	for _, x := range dry.Plan.Records {
		minted = append(minted, x.NativeID)
	}
	parent := core.S(lastRec(t, file)["id"])
	var recs []any
	for _, id := range minted {
		recs = append(recs, core.O{{K: "type", V: "custom"}, {K: "id", V: id}, {K: "parentId", V: parent}, {K: "timestamp", V: "2026-10-15T14:00:00.000Z"}, {K: "customType", V: "x"}, {K: "data", V: map[string]any{}}})
		parent = id
	}
	appendLines(t, file, recs)
	r2 := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi"})
	eq(t, r2.Plan.Mode, "create")
	ok(t, len(minted) > 0)
	ok(t, r2.Plan.Target.Path != file, "copies use independent namespaces")
	seen := map[string]bool{}
	for _, x := range readJSONL(t, file) {
		if id := core.S(x["id"]); id != "" {
			ok(t, !seen[id], "ids stay unique within the file: ", id)
			seen[id] = true
		}
	}
}

func TestSessionLossRoots(t *testing.T) {
	s := newSandbox(t)
	link := filepath.Join(s.Root, "claude-link")
	must(t, os.Symlink(s.Claude, link))
	a := s.app(enable("claude-code", "pi"))
	_, err := a.AddRoot("claude-code", link)
	ok(t, err != nil && strings.Contains(err.Error(), "already registered as claude-code"), err)
	must(t, os.Mkdir(filepath.Join(s.Claude, "nested"), 0o755))
	_, err = a.AddRoot("claude-code", filepath.Join(s.Claude, "nested"))
	ok(t, err != nil && strings.Contains(err.Error(), "overlaps the root of claude-code"), err)
	other := filepath.Join(s.Root, "pi2")
	must(t, os.Mkdir(other, 0o755))
	x, err := a.AddRoot("pi", other)
	must(t, err)
	writeCcSession(t, s.Claude, "", true)
	scan(t, a)
	carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: x.ID})
	must(t, a.RemoveRoot(ctx, x.ID))
	ok(t, len(jsonlFiles(t, filepath.Join(other, "sessions"), "")) > 0)
}

func TestSessionLossDeletedTarget(t *testing.T) {
	s := newSandbox(t)
	ccFile := writeCcSession(t, s.Claude, "", true)
	a := s.app(enable("claude-code", "pi"))
	scan(t, a)
	r1 := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi"})
	must(t, os.Remove(r1.Plan.Target.Path))
	appendLines(t, ccFile, ccTurn(CCSession, "a5", 1, ""))
	r2 := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi"})
	eq(t, r2.Plan.Mode, "create")
	ok(t, r2.Applied)
	ok(t, strings.Contains(text(t, r2.Plan.Target.Path), "x question 1"))
}

func TestSessionLossTwoProcesses(t *testing.T) {
	s := newSandbox(t)
	writeCcSession(t, s.Claude, "", true)
	a := s.app(enable("claude-code", "pi"))
	b := s.app(enable("claude-code", "pi"))
	scan(t, a)
	scan(t, b)
	// engine operations of two processes never overlap (the lock in koto.sqlite)
	var mu sync.Mutex
	var log []string
	var wg sync.WaitGroup
	for i, e := range []*engine.Engine{a.Engine, b.Engine, a.Engine} {
		tag := string(rune('a' + i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			must(t, e.Locked(ctx, func() error {
				mu.Lock()
				log = append(log, tag+"<")
				mu.Unlock()
				time.Sleep(80 * time.Millisecond)
				mu.Lock()
				log = append(log, ">"+tag)
				mu.Unlock()
				return nil
			}))
		}()
	}
	wg.Wait()
	for i := 0; i < len(log); i += 2 {
		eq(t, log[i][:1], log[i+1][1:], "no overlap: ", strings.Join(log, " "))
	}
	// both processes copy at the same moment: one writes, the other finds it there (the shared ledger)
	rs := make([]*engine.CarryResult, 4)
	for i, x := range []*App{a, b, a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := x.Engine.Carry(ctx, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi", RequestID: "shared-request"})
			if err == nil {
				rs[i] = r
			}
		}()
	}
	wg.Wait()
	applied := 0
	for _, r := range rs {
		if r != nil && r.Applied {
			applied++
		}
	}
	eq(t, applied, 4, "all retries receive a successful receipt")
	eq(t, len(piFilesIn(s.Pi)), 2, "one independent session per process, retries do not duplicate")
}

func TestSessionLossPiCwd(t *testing.T) {
	s := newSandbox(t)
	f := filepath.Join(s.Pi, "sessions", "--repo-x--", "2026-01-01T00-00-00-000Z_0bbbbbbb-0000-4000-8000-000000000001.jsonl")
	writeLines(t, f, []any{
		core.O{{K: "type", V: "session"}, {K: "version", V: 3}, {K: "id", V: "0bbbbbbb-0000-4000-8000-000000000001"}, {K: "timestamp", V: "2026-01-01T00:00:00.000Z"}, {K: "cwd", V: "/repo/x"}},
		core.O{{K: "type", V: "message"}, {K: "id", V: "u1"}, {K: "parentId", V: nil}, {K: "timestamp", V: "2026-01-01T00:00:01.000Z"}, {K: "message", V: core.O{{K: "role", V: "user"}, {K: "content", V: "hello"}, {K: "timestamp", V: 1}}}},
	})
	a := s.app(enable("claude-code", "pi"))
	scan(t, a)
	r := carry(t, a, engine.CarryOptions{Selector: "pi:0bbbbbbb", To: "claude-code"})
	eq(t, filepath.Base(filepath.Dir(r.Plan.Target.Path)), "-repo-x")
	eq(t, readJSONL(t, r.Plan.Target.Path)[0]["cwd"], any("/repo/x"))
}

func TestSessionLossResumeAndOpenReason(t *testing.T) {
	s := newSandbox(t)
	h := startHolder(t, "")
	ccFile := writeCcSession(t, s.Claude, "", true)
	a := s.app(enable("claude-code", "pi"))
	scan(t, a)
	thread := binding(t, a.Hub, "claude-code", CCSession).Thread
	var cc *BindingView
	for _, b := range a.Bindings(a.Hub, thread) {
		if b.Harness == "claude-code" {
			cc = &b
		}
	}
	command := "claude --resume " + CCSession
	shell := "cd /work/proj && CLAUDE_CONFIG_DIR=" + engine.ShQ(s.Claude) + " " + command
	if runtime.GOOS == "windows" {
		command = "& 'claude' '--resume' '" + CCSession + "'"
		shell = "Set-Location -LiteralPath '/work/proj' -ErrorAction Stop; $env:CLAUDE_CONFIG_DIR=" + engine.PowerShellQ(s.Claude) + "; " + command
	}
	jsonEq(t, cc.Resume, map[string]any{"argv": []string{"claude", "--resume", CCSession}, "command": command, "cwd": "/work/proj",
		"env": map[string]string{"CLAUDE_CONFIG_DIR": s.Claude}, "shell": shell})

	// a copy, then pi continues while CC has the session open: a copy back waits, with the reason
	toPi := carry(t, a, engine.CarryOptions{Selector: thread, To: "pi"})
	piFile := toPi.Plan.Target.Path
	appendLines(t, piFile, []any{piMsg("p1u", core.S(lastRec(t, piFile)["id"]), time.Date(2026, 10, 15, 14, 0, 0, 0, time.UTC), core.O{{K: "role", V: "user"}, {K: "content", V: "from pi"}, {K: "timestamp", V: 1}})})
	must(t, os.MkdirAll(filepath.Join(s.Claude, "sessions"), 0o755))
	must(t, os.WriteFile(filepath.Join(s.Claude, "sessions", fmt.Sprintf("%d.json", h.pid)), []byte(core.Stringify(map[string]any{"sessionId": CCSession, "status": "idle"})), 0o644))
	scan(t, a)
	r := carry(t, a, engine.CarryOptions{Selector: "pi:" + core.S(toPi.Receipt["session"]), To: "claude-code"})
	ok(t, r.Applied && r.Plan.Target.Session != CCSession)
	ok(t, !strings.Contains(text(t, ccFile), "from pi"))
}
