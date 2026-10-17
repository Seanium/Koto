package koto

// Concurrency and conflict scenarios: several koto processes on one data dir, several harnesses
// writing the same thread at once, a harness caught mid-write.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"koto/internal/core"
	"koto/internal/engine"
)

var (
	cliOnce sync.Once
	cliPath string
	cliErr  error
)

// kotoCLI builds cmd/koto once per test run.
func kotoCLI(t *testing.T) string {
	t.Helper()
	cliOnce.Do(func() {
		dir, err := os.MkdirTemp("", "koto-cli-")
		if err != nil {
			cliErr = err
			return
		}
		cliPath = filepath.Join(dir, "koto")
		if runtime.GOOS == "windows" {
			cliPath += ".exe"
		}
		out, err := exec.Command("go", "build", "-o", cliPath, "koto/cmd/koto").CombinedOutput()
		if err != nil {
			cliErr = fmt.Errorf("build test CLI: %w: %s", err, out)
		}
	})
	if cliErr != nil {
		t.Fatal(cliErr)
	}
	return cliPath
}

func (s *sandbox) cli(t *testing.T, args ...string) (string, error) {
	args = append(args, "--data", s.Data, "--claude-root", s.Claude, "--pi-root", s.Pi, "--only", "claude-code,pi", "--json")
	out, err := exec.Command(kotoCLI(t), args...).CombinedOutput()
	return string(out), err
}

func lines(t *testing.T, f string) []string {
	var out []string
	for _, l := range strings.Split(text(t, f), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func allParse(t *testing.T, f string) bool {
	for _, l := range lines(t, f) {
		if !json.Valid([]byte(l)) {
			return false
		}
	}
	return true
}

func distinct(xs []string) int {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return len(m)
}

func TestConflictsThreeProcesses(t *testing.T) {
	s := newSandbox(t)
	ccFile := writeCcSession(t, s.Claude, "", true)
	if out, err := s.cli(t, "scan"); err != nil {
		t.Fatal(err, out)
	}
	parallel := func() []string {
		var wg sync.WaitGroup
		res := make([]string, 3)
		for i := range 3 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out, err := s.cli(t, "carry", "claude-code:"+CCSession, "--to", "pi")
				if err == nil {
					res[i] = "ok"
				} else {
					res[i] = out
				}
			}()
		}
		wg.Wait()
		return res
	}
	rs := parallel()
	ok(t, strings.Contains(strings.Join(rs, "|"), "ok"), rs)
	files := jsonlFiles(t, filepath.Join(s.Pi, "sessions"), "")
	eq(t, len(files), 3, "independent CLI invocations each create a session")
	first := lines(t, files[0])
	eq(t, distinct(first), len(first), "no duplicated records")

	// More turns at the source, then three concurrent new copies.
	appendLines(t, ccFile, append(ccTurn(CCSession, "a5", 1, ""), ccTurn(CCSession, "xa1", 2, "")...))
	if out, err := s.cli(t, "scan"); err != nil {
		t.Fatal(err, out)
	}
	parallel()
	files = jsonlFiles(t, filepath.Join(s.Pi, "sessions"), "")
	eq(t, len(files), 6)
	var after []string
	for _, f := range files {
		if len(lines(t, f)) > len(first) {
			after = lines(t, f)
		}
	}
	eq(t, len(after), len(first)+4, "new full copy contains the new turns")
	eq(t, distinct(after), len(after))
	ok(t, allParse(t, files[0]))

	// A fresh process indexes every native session independently; rescanning is stable.
	a := s.app(enable("claude-code", "pi"))
	scan(t, a)
	n := a.Hub.EventCount()
	eq(t, scan(t, a).Added, 0, "no echo")
	eq(t, a.Hub.EventCount(), n)
	th := a.Hub.Threads()
	eq(t, len(th), 7)
	eq(t, len(a.Hub.Bindings(th[0].ID)), 1)
}

func TestConflictsSerializedInProcess(t *testing.T) {
	s := newSandbox(t)
	writeCcSession(t, s.Claude, "", true)
	a := s.app(enable("claude-code", "pi"))
	scan(t, a)
	var wg sync.WaitGroup
	rs := make([]*engine.CarryResult, 5)
	for i := range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := a.Engine.Carry(ctx, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi", RequestID: "concurrent"})
			if err == nil {
				rs[i] = r
			}
		}()
	}
	wg.Wait()
	applied := 0
	for _, r := range rs {
		ok(t, r != nil)
		if r.Applied {
			applied++
		} else {
			eq(t, r.Plan.Mode, "noop")
		}
	}
	eq(t, applied, 5, "every retry receives a success receipt")
	eq(t, len(jsonlFiles(t, filepath.Join(s.Pi, "sessions"), "")), 1)
}

func TestConflictsPartialTrailingRecord(t *testing.T) {
	s := newSandbox(t)
	ccFile := writeCcSession(t, s.Claude, "", true)
	a := s.app(enable("claude-code", "pi"))
	scan(t, a)
	first := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi"})
	piFile := first.Plan.Target.Path
	recs := readJSONL(t, piFile)
	piLast := core.S(recs[len(recs)-1]["id"])
	// pi is in the middle of writing a record; meanwhile CC moves on
	u := piTurn("p1", piLast, "half written", 30)[0]
	full := core.Stringify(u) + "\n"
	f, err := os.OpenFile(piFile, os.O_WRONLY|os.O_APPEND, 0)
	must(t, err)
	_, _ = f.WriteString(full[:40])
	appendLines(t, ccFile, ccTurn(CCSession, "a5", 1, ""))
	mid := text(t, piFile)
	scan(t, a)
	// Refused outright (the file ends mid-record) or deferred: either way, not written into it.
	r, err := a.Engine.Carry(ctx, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi"})
	if err != nil {
		ok(t, strings.Contains(err.Error(), "partial record"), err)
	} else {
		ok(t, !r.Applied || r.Plan.Target.Session != first.Plan.Target.Session, "not written into the half-written session")
	}
	now := text(t, piFile)
	ok(t, strings.HasPrefix(now, mid), "nothing written into the middle of the record")
	ok(t, !strings.Contains(now[len(mid):], "x question 1"))
	// pi finishes its record: the two are two versions; the CC version can still be copied, to a new pi session
	_, _ = f.WriteString(full[40:])
	f.Close()
	scan(t, a)
	ok(t, allParse(t, piFile))
	thread := binding(t, a.Hub, "claude-code", CCSession).Thread
	eq(t, len(a.Hub.Leaves(thread)), 1)
	r2 := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi"})
	eq(t, r2.Plan.Mode, "create")
	ok(t, strings.Contains(text(t, r2.Plan.Target.Path), "x question 1"))
}
