package koto

// The trash: sessions are moved out of their harnesses (never deleted) and restored byte-identical.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"testing"

	"koto/internal/core"
	"koto/internal/store"
)

// snapshot is path → sha256 of every file under the roots (what the user would lose).
func snapshot(roots ...string) map[string]string {
	out := map[string]string{}
	for _, r := range roots {
		_ = filepath.WalkDir(r, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				b, _ := os.ReadFile(p)
				h := sha256.Sum256(b)
				out[p] = hex.EncodeToString(h[:])
			}
			return nil
		})
	}
	return out
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func newAPI(a *App) *API { return &API{App: a} }

func threadIDs(api *API) []string {
	out := []string{}
	for _, t := range api.Threads(0) {
		out = append(out, t.ID)
	}
	return out
}

func harnessesOf(t *testing.T, api *API, thread string) []string {
	d, err := api.Thread(ctx, thread, 1, "")
	must(t, err)
	var out []string
	for _, b := range d.Bindings {
		out = append(out, b.Harness)
	}
	slices.Sort(out)
	return out
}

func apiCode(err error) (int, string) {
	if err == nil {
		return 0, ""
	}
	e := AsAPIError(err)
	return e.Status, e.Code
}

// threeHarnesses: a Claude Code session with its side state, copied to pi and dsh.
func threeHarnesses(t *testing.T, s *sandbox, a *App) (*API, string) {
	writeCcSession(t, s.Claude, "", true)
	must(t, os.MkdirAll(filepath.Join(s.Claude, "file-history", CCSession), 0o755))
	must(t, os.WriteFile(filepath.Join(s.Claude, "file-history", CCSession, "v1"), []byte("old file"), 0o644))
	must(t, os.MkdirAll(filepath.Join(s.Claude, "todos"), 0o755))
	must(t, os.WriteFile(filepath.Join(s.Claude, "todos", CCSession+"-agent-"+CCSession+".json"), []byte("[]"), 0o644))
	must(t, os.WriteFile(filepath.Join(s.Claude, "todos", "other-session-agent.json"), []byte("[]"), 0o644))
	api := newAPI(a)
	api.Reindex(ctx)
	sel := "claude-code:" + CCSession
	for _, to := range []string{"pi", "dsh"} {
		r, err := api.Carry(ctx, CarryRequest{Selector: sel, To: to})
		must(t, err)
		ok(t, r.Applied)
	}
	api.Reindex(ctx)
	thread := threadIDs(api)
	eq(t, len(thread), 3)
	return api, "claude-code:" + CCSession
}

func piSessionFiles(s *sandbox) []string { return jsonlFiles(nil, filepath.Join(s.Pi, "sessions"), "") }

func TestTrashNativeSessionLeavesCopiesUntouched(t *testing.T) {
	s := newSandbox(t)
	a := s.app(enable("claude-code", "pi", "dsh"))
	api, thread := threeHarnesses(t, s, a)
	before := snapshot(s.Claude, s.Pi, s.Dsh)
	e, err := api.TrashThread(ctx, thread, "")
	must(t, err)
	var via []string
	for _, i := range e.Items {
		via = append(via, i.Harness+":"+i.Via)
	}
	slices.Sort(via)
	jsonEq(t, via, []string{"claude-code:move"})
	cc := e.Items[slices.IndexFunc(e.Items, func(i store.TrashItem) bool { return i.Harness == "claude-code" })]
	// transcript, side dir (tool-results), file-history, its todos — not another session's todos
	eq(t, len(cc.Files), 4)
	other := filepath.Join(s.Claude, "todos", "other-session-agent.json")
	jsonEq(t, keys(snapshot(s.Claude)), []string{other})
	for path, hash := range snapshot(s.Pi, s.Dsh) {
		eq(t, hash, before[path])
	}
	inTrash := snapshot(filepath.Join(s.Data, "trash", e.TrashID))
	vals := map[string]bool{}
	for _, h := range before {
		vals[h] = true
	}
	n := 0
	for _, h := range inTrash {
		if vals[h] {
			n++
		}
	}
	eq(t, n, len(before)-1-len(snapshot(s.Pi, s.Dsh)))
	// hidden from the lists, and a scan does not recreate anything
	eq(t, len(threadIDs(api)), 2)
	eq(t, len(api.TrashList()), 1)
	eq(t, api.TrashList()[0].TrashID, e.TrashID)
	api.Reindex(ctx)
	eq(t, len(piSessionFiles(s)), 1)

	r, err := api.Restore(ctx, e.TrashID)
	must(t, err)
	ok(t, r.RestoredAt != nil)
	jsonEq(t, snapshot(s.Claude, s.Pi, s.Dsh), before, "identical bytes back at the identical paths")
	_, err = os.Stat(filepath.Join(s.Data, "trash", e.TrashID))
	ok(t, os.IsNotExist(err), "the emptied trash folder is removed")
	eq(t, len(threadIDs(api)), 3)
	eq(t, len(api.TrashList()), 0)
	// and copying carries on into the same sessions
	appendLines(t, jsonlFiles(t, filepath.Join(s.Claude, "projects"), "subagents")[0], ccTurn(CCSession, "a5", 1, ""))
	api.Reindex(ctx)
	d, err := api.Carry(ctx, CarryRequest{Selector: "claude-code:" + CCSession, To: "pi"})
	must(t, err)
	jsonEq(t, []string{d.Plan.Harness, d.Plan.Mode}, []string{"pi", "create"})
	eq(t, len(piSessionFiles(s)), 2)
}

func TestTrashOneHarness(t *testing.T) {
	s := newSandbox(t)
	a := s.app(enable("claude-code", "pi", "dsh"))
	api, _ := threeHarnesses(t, s, a)
	thread := "pi:" + core.S(readJSONL(t, piSessionFiles(s)[0])[0]["id"])
	e, err := api.TrashThread(ctx, thread, "")
	must(t, err)
	eq(t, len(e.Items), 1)
	eq(t, e.Items[0].Harness, "pi")
	eq(t, len(piSessionFiles(s)), 0)
	eq(t, len(threadIDs(api)), 2, "other native sessions stay listed")
	jsonEq(t, harnessesOf(t, api, "claude-code:"+CCSession), []string{"claude-code"})
	_, err = api.TrashThread(ctx, thread, "pi")
	_, code := apiCode(err)
	eq(t, code, "NOT_FOUND")
	_, err = api.Restore(ctx, e.TrashID)
	must(t, err)
	eq(t, len(piSessionFiles(s)), 1)
	jsonEq(t, harnessesOf(t, api, thread), []string{"pi"})
	status, _ := apiCode(func() error { _, err := api.TrashThread(ctx, thread, "nope"); return err }())
	eq(t, status, 400)
}

func TestTrashOpenAndOccupied(t *testing.T) {
	s := newSandbox(t)
	p := startHolder(t, "")
	pid := p.pid
	a := s.app(enable("claude-code", "pi"))
	api := newAPI(a)
	ccFile := writeCcSession(t, s.Claude, "", true)
	api.Reindex(ctx)
	must(t, os.MkdirAll(filepath.Join(s.Claude, "sessions"), 0o755))
	reg := filepath.Join(s.Claude, "sessions", fmt.Sprintf("%d.json", pid))
	must(t, os.WriteFile(reg, []byte(core.Stringify(map[string]any{"pid": pid, "sessionId": CCSession, "status": "idle"})), 0o644))
	before := snapshot(s.Claude)
	_, err := api.TrashThread(ctx, "claude-code:"+CCSession, "")
	_, code := apiCode(err)
	eq(t, code, "SESSION_OPEN")
	ok(t, strings.Contains(err.Error(), fmt.Sprintf("Claude Code pid %d has it open", pid)), err)
	jsonEq(t, snapshot(s.Claude), before)
	ents, _ := os.ReadDir(filepath.Join(s.Data, "trash"))
	eq(t, len(ents), 0)
	must(t, os.Remove(reg))
	delete(before, reg)

	e, err := api.TrashThread(ctx, "claude-code:"+CCSession, "")
	must(t, err)
	must(t, os.WriteFile(ccFile, []byte("a new session at the same path"), 0o644))
	_, err = api.Restore(ctx, e.TrashID)
	status, code := apiCode(err)
	jsonEq(t, []any{status, code}, []any{409, "RESTORE_CONFLICT"})
	ok(t, strings.Contains(err.Error(), ccFile))
	jsonEq(t, AsAPIError(err).Detail["paths"], []string{ccFile})
	eq(t, text(t, ccFile), "a new session at the same path", "the newcomer is untouched")
	_, err = os.Stat(e.Items[0].Files[0].To)
	ok(t, err == nil, "the trash copy is untouched")
	must(t, os.Remove(ccFile))
	_, err = api.Restore(ctx, e.TrashID)
	must(t, err)
	jsonEq(t, snapshot(s.Claude), before)
	_, err = api.Restore(ctx, e.TrashID)
	status, code = apiCode(err)
	jsonEq(t, []any{status, code}, []any{404, "NOT_FOUND"})
}

func TestTrashAcrossVolumes(t *testing.T) {
	s := newSandbox(t)
	exdev := func(string, string) error { return &os.LinkError{Op: "rename", Err: syscall.EXDEV} }
	a := s.app(enable("claude-code", "pi", "dsh"), func(c *Config) { c.TrashRename = exdev })
	api, thread := threeHarnesses(t, s, a)
	before := snapshot(s.Claude, s.Pi, s.Dsh)
	e, err := api.TrashThread(ctx, thread, "")
	must(t, err)
	eq(t, len(snapshot(s.Claude)), 1)
	_, err = api.Restore(ctx, e.TrashID)
	must(t, err)
	jsonEq(t, snapshot(s.Claude, s.Pi, s.Dsh), before)
	a.Close()

	// the third move fails: the two already moved come back, nothing is recorded
	n := 0
	flaky := s.app(enable("claude-code", "pi", "dsh"), func(c *Config) {
		c.TrashRename = func(x, y string) error {
			if n++; n == 3 {
				return &os.LinkError{Op: "rename", Old: x, New: y, Err: syscall.EACCES}
			}
			return os.Rename(x, y)
		}
	})
	fapi := newAPI(flaky)
	fapi.Reindex(ctx)
	_, err = fapi.TrashThread(ctx, thread, "")
	ok(t, err != nil && errors.Is(err, syscall.EACCES) || err != nil && strings.Contains(err.Error(), "permission denied"), err)
	jsonEq(t, snapshot(s.Claude, s.Pi, s.Dsh), before)
	eq(t, len(fapi.TrashList()), 0)
	eq(t, len(threadIDs(fapi)), 3)
	ents, _ := os.ReadDir(filepath.Join(s.Data, "trash"))
	eq(t, len(ents), 0)
}

func TestTrashListShape(t *testing.T) {
	s := newSandbox(t)
	a := s.app(enable("claude-code", "pi", "dsh"))
	api, _ := threeHarnesses(t, s, a)
	e, err := api.TrashThread(ctx, "claude-code:"+CCSession, "")
	must(t, err)
	ok(t, strings.HasPrefix(e.TrashID, "tr_"))
	item := normalize(api.TrashList()[0].Items[0]).(map[string]any)
	var ks []string
	for k := range item {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	jsonEq(t, ks, []string{"archive", "connector", "files", "harness", "note", "originalPath", "session", "via"})
}

func TestTrashCodexArchive(t *testing.T) {
	if !hasCodex() {
		t.Skip("codex CLI not installed")
	}
	s := newSandbox(t)
	writeCcSession(t, s.Claude, "", false)
	a := s.app(enable("claude-code", "codex"))
	api := newAPI(a)
	api.Reindex(ctx)
	cwd := filepath.Join(s.Root, "work")
	must(t, os.Mkdir(cwd, 0o755))
	r, err := api.Carry(ctx, CarryRequest{Selector: "claude-code:" + CCSession, To: "codex", Cwd: cwd})
	must(t, err)
	threadID, rollout := core.S(r.Receipt["threadId"]), core.S(r.Receipt["path"])
	bytes, _ := os.ReadFile(rollout)
	api.Reindex(ctx)
	e, err := api.TrashThread(ctx, "codex:"+threadID, "codex")
	must(t, err)
	eq(t, e.Items[0].Via, "archive")
	_, err = os.Stat(rollout)
	ok(t, os.IsNotExist(err))
	archived := core.S(e.Items[0].Archive["path"])
	ok(t, strings.HasPrefix(archived, filepath.Join(s.Codex, "archived_sessions")), archived)
	got, _ := os.ReadFile(archived)
	ok(t, len(got) >= len(bytes) && string(got[:len(bytes)]) == string(bytes))
	api.Reindex(ctx) // the archived rollout is still found, but stays in the trash
	jsonEq(t, harnessesOf(t, api, "claude-code:"+CCSession), []string{"claude-code"})
	_, err = api.Restore(ctx, e.TrashID)
	must(t, err)
	_, err = os.Stat(rollout)
	ok(t, err == nil, "back where Codex had it")
	_, err = os.Stat(archived)
	ok(t, os.IsNotExist(err))
}
