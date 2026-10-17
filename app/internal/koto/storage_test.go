package koto

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"koto/internal/engine"
)

func TestNothingOfAConversationIsWritten(t *testing.T) {
	s := newSandbox(t)
	ccFile := writeCcSession(t, s.Claude, "", true)
	a := s.app(enable("claude-code", "pi"))
	scan(t, a)
	r := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi"})
	ok(t, r.Applied)
	a.Close()
	var files []string
	_ = filepath.Walk(s.Data, func(p string, _ os.FileInfo, _ error) error { files = append(files, p); return nil })
	for _, f := range files {
		ok(t, !regexp.MustCompile(`hub\.sqlite|blobs|\.pack$`).MatchString(f), f)
	}
	st, _ := os.Stat(filepath.Join(s.Data, "koto.sqlite"))
	ok(t, st.Size() < 1_000_000)
	a = s.app(enable("claude-code", "pi"))
	a.Sessions.Scan(ctx, nil, nil)
	list := a.ListThreads()
	eq(t, len(list), 2, "native sessions remain independent")
	var bs []string
	for _, row := range list {
		for _, b := range row.Bindings {
			bs = append(bs, b.Harness)
		}
	}
	sort.Strings(bs)
	eq(t, strings.Join(bs, ","), "claude-code,pi")
	appendLines(t, ccFile, ccTurn(CCSession, "a5", 1, ""))
	a.Sessions.Scan(ctx, nil, nil)
	v, err := a.OpenThread(ctx, "claude-code:"+CCSession)
	must(t, err)
	_, head, _ := v.Hub.ResolveHead("claude-code:" + CCSession)
	up, err := v.Engine.Carry(ctx, engine.CarryOptions{Thread: v.Thread, Head: head, To: "pi"})
	must(t, err)
	eq(t, up.Plan.Mode, "create")
	ok(t, up.Plan.Target.Session != r.Plan.Target.Session, "each copy is independent")
}

func TestScanProgress(t *testing.T) {
	s := newSandbox(t)
	a := s.app()
	for i := 0; i < 120; i++ {
		session := "00000000-0000-4000-8000-" + pad12(i)
		file := writeCcSession(t, s.Claude, session, false)
		parent := "a5"
		var recs []any
		for n := 0; n < 40; n++ {
			recs = append(recs, ccTurn(session, parent, n, "")...)
			parent = "xa" + itoa(n)
		}
		appendLines(t, file, recs)
	}
	var seen []engine.ScanProgress
	r, err := a.Engine.Scan(ctx, engine.ScanOptions{OnProgress: func(p engine.ScanProgress) { seen = append(seen, p) }, ProgressMs: 1})
	must(t, err)
	eq(t, r.Changed, 120)
	ok(t, len(seen) >= 2, "reports as it goes: ", len(seen))
	end := seen[len(seen)-1]
	jsonEq(t, []any{end.Connector, end.Harness, end.Done, end.Total}, []any{"claude-code", "claude-code", 120, 120})
	for i := 1; i < len(seen); i++ {
		ok(t, seen[i].Done >= seen[i-1].Done)
	}
	ok(t, seen[0].Done < 120)
	var quiet []engine.ScanProgress
	_, err = a.Engine.Scan(ctx, engine.ScanOptions{OnProgress: func(p engine.ScanProgress) { quiet = append(quiet, p) }})
	must(t, err)
	eq(t, len(quiet), 0, "an incremental scan stays quiet")
}

func pad12(i int) string {
	s := itoa(i)
	return strings.Repeat("0", 12-len(s)) + s
}
