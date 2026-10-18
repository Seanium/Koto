// koto — the command line: the same operations as the app, plain text or --json.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"time"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/hub"
	"koto/internal/koto"
	"koto/internal/legal"
	"koto/internal/store"
	"koto/internal/update"
)

// version is set by the release build (-ldflags -X main.version=…).
var version = "dev"

const help = `koto — copy coding-agent conversations between harnesses

usage: koto <command> [options]

commands
  rebuild-index              rebuild disposable session summaries from native sources
  scan                       read the harness folders into the index (only changed files are read; nothing is written there)
  threads                    list conversations (from the index)
  show <sel>                 show a native session (sel: harness:session, or an event id)
  current                    identify this Agent's exact current session
  copy <agent:session> --to <agent>  copy a session and print its continuation command
  copy --current --to <agent>  copy this session using its native ID or KOTO_SESSION
  analyze <session> <session>…  read-only comparison of sessions from the same Agent
  carry <sel> --to <conn>    copy into a new session, from any node (dry-run first with --dry-run)
  trash [<sel>] [--harness h]  move a conversation's native sessions (or one harness's) to the trash; no <sel>: list the trash
  restore <trash-id>         put a trash entry's sessions back where they were
  mcp                        stdio MCP server: list_sessions / get_session, so any MCP-capable harness can pull a session
  connectors                 list connectors and capabilities
  profiles                   list storage profiles and evidenced identities
  version                    print the version
  update [--check]            check for a new version or update this CLI
  licenses                   show complete license and third-party notices

options
  --data <dir>          data dir (default $KOTO_HOME or the platform data folder)
  --claude-root <dir>   Claude Code config dir (default $CLAUDE_CONFIG_DIR or ~/.claude)
  --pi-root <dir>       pi agent dir (default $PI_CODING_AGENT_DIR or ~/.pi/agent)
  --codex-home <dir>    Codex home (default $CODEX_HOME or ~/.codex)
  --codex-shadow <dir>  isolated working directory for Codex imports
  --dsh-home <dir>      dsh home (default $DSH_HOME or ~/.dsh)
  --agy-root <dir>      Antigravity CLI dir (default ~/.gemini/antigravity-cli)
  --opencode-root <dir> OpenCode data directory (default $XDG_DATA_HOME/opencode)
  --zcode-root <dir>    ZCode storage directory (default $ZCODE_STORAGE_DIR or ~/.zcode)
  --zcode-bin <path>    ZCode executable (default detected installation or PATH)
  --cursor-app-root <dir>  Cursor App user data (separate from CLI)
  --cursor-root <dir>   Cursor CLI data (default ~/.cursor)
  --cursor-bin <path>   Explicit Cursor CLI executable
  --workbuddy-root <dir> WorkBuddy App data (default ~/.workbuddy)
  --workbuddy-ai-root <dir> WorkBuddy AI App data (default ~/.workbuddy-ai)
  --only <ids>          comma-separated connector ids to enable
  --profile <id>        filter session list by storage profile
  --account <id>        filter evidenced historical account (within profile)
  --to-profile <id>     copy into this registered storage profile
  --to-root <dir>       register and copy into this existing Agent data directory
  --to-account <id>     select a native owner only where the target supports it
  --level L0|L1|L2|L3   projection level for carry
  --max-tokens <n>      token budget for carry (older turns summarized)
  --title <text>        a new session's title
  --live-ms <n>         how recent a write makes a session look open (default 120000; 0 disables)
  --dry-run             plan only; print records and losses
  --json                machine-readable output
` + "\n" + agentGuide

func main() {
	if len(os.Args) == 4 && os.Args[1] == "--koto-install" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		exe, err := os.Executable()
		if err == nil {
			err = update.InstallCLI(ctx, exe, os.Args[2], version, os.Args[3])
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "--koto-update-recover" {
		if err := update.RunRecovery(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	update.CleanPrevious(update.CLI)
	if len(os.Args) > 1 && os.Args[1] == "update" {
		os.Exit(runUpdate(os.Args[2:], os.Stdout, os.Stderr))
	}
	// Flags may come after the positionals (koto carry <sel> --to pi).
	var pos, flags []string
	for i := 1; i < len(os.Args); i++ {
		a := os.Args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && i+1 < len(os.Args) && !isBool(a) && !strings.HasPrefix(os.Args[i+1], "-") {
				flags = append(flags, os.Args[i+1])
				i++
			}
		} else {
			pos = append(pos, a)
		}
	}
	if len(pos) > 0 && (pos[0] == "current" || pos[0] == "copy") {
		os.Exit(runHandoff(pos, flags, os.Stdout, os.Stderr, os.Getenv))
	}
	fs := flag.NewFlagSet("koto", flag.ContinueOnError)
	fs.Usage = func() { fmt.Print(help) }
	s := func(n string) *string { return fs.String(n, "", "") }
	b := func(n string) *bool { return fs.Bool(n, false, "") }
	data, claude, piRoot, codexHome, codexShadow, codexBin, dshHome, agy := s("data"), s("claude-root"), s("pi-root"), s("codex-home"), s("codex-shadow"), s("codex-bin"), s("dsh-home"), s("agy-root")
	opencodeRoot := s("opencode-root")
	zcodeRoot := s("zcode-root")
	zcodeBin := s("zcode-bin")
	workBuddyRoot, workBuddyAIRoot := s("workbuddy-root"), s("workbuddy-ai-root")
	cursorRoot, cursorBin := s("cursor-root"), s("cursor-bin")
	cursorAppRoot := s("cursor-app-root")
	only, to, level, maxTokens, harness, liveMs, limit, cwd, title := s("only"), s("to"), s("level"), s("max-tokens"), s("harness"), s("live-ms"), s("limit"), s("cwd"), s("title")
	profile, account, toProfile, toAccount, toRoot := s("profile"), s("account"), s("to-profile"), s("to-account"), s("to-root")
	dry, asJSON, h := b("dry-run"), b("json"), b("help")
	if err := fs.Parse(flags); err != nil {
		os.Exit(2)
	}
	if len(pos) == 0 || *h {
		fmt.Print(help)
		return
	}
	if pos[0] == "version" {
		fmt.Println("koto", version)
		return
	}
	if pos[0] == "licenses" {
		text, err := legal.Notices()
		if err != nil {
			fatal(err)
		}
		if *asJSON {
			b, _ := json.Marshal(map[string]string{"notices": text})
			fmt.Println(string(b))
		} else {
			fmt.Print(text)
		}
		return
	}
	cmd, sel := pos[0], ""
	if len(pos) > 1 {
		sel = pos[1]
	}
	cfg := koto.Config{DataDir: *data, ClaudeRoot: *claude, PiRoot: *piRoot, CodexHome: *codexHome, CodexShadow: *codexShadow, CodexBin: *codexBin, DshHome: *dshHome, AntigravityRoot: *agy, OpenCodeRoot: *opencodeRoot, ZCodeRoot: *zcodeRoot, Log: stderrLog{}}
	cfg.ZCodeBin, cfg.WorkBuddyRoot, cfg.WorkBuddyAIRoot = *zcodeBin, *workBuddyRoot, *workBuddyAIRoot
	cfg.CursorRoot, cfg.CursorBin = *cursorRoot, *cursorBin
	cfg.CursorAppRoot = *cursorAppRoot
	if *liveMs != "" {
		n, _ := strconv.ParseInt(*liveMs, 10, 64)
		cfg.LiveMs = &n
	}
	if *only != "" {
		cfg.Enable = strings.Split(*only, ",")
	}
	app, err := koto.New(cfg)
	if err != nil {
		fatal(err)
	}
	defer app.Close()
	if *toRoot != "" {
		if *toProfile != "" {
			fatal(fmt.Errorf("choose --to-root or --to-profile, not both"))
		}
		p, err := app.EnsureRoot(*to, *toRoot)
		if err != nil {
			fatal(err)
		}
		*toProfile = p.ID
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	out := func(human string, v any) {
		if *asJSON {
			b, _ := json.MarshalIndent(v, "", "  ")
			fmt.Println(string(b))
		} else {
			fmt.Println(human)
		}
	}
	type view struct {
		hub    *hub.Hub
		engine *engine.Engine
		thread string
		head   string
	}
	open := func(s string) view {
		if strings.HasPrefix(s, "ev") {
			if _, err := app.Engine.Scan(ctx, engine.ScanOptions{}); err != nil {
				fatal(err)
			}
			t, hd, err := app.Hub.ResolveHead(s)
			if err != nil || hd == "" {
				fatal(fmt.Errorf("nothing matches %s", s))
			}
			return view{app.Hub, app.Engine, t, hd}
		}
		app.ScanIndex(ctx, nil)
		v, err := app.OpenThread(ctx, s)
		if err != nil {
			fatal(err)
		}
		var hd string
		if strings.HasPrefix(s, "th:") {
			hd = v.Hub.ThreadHead(v.Thread)
		} else {
			_, hd, _ = v.Hub.ResolveHead(s)
		}
		if hd == "" {
			fatal(fmt.Errorf("nothing matches %s", s))
		}
		return view{v.Hub, v.Engine, v.Thread, hd}
	}
	need := func(v, usage string) {
		if v == "" {
			fatal(fmt.Errorf("usage: koto %s", usage))
		}
	}
	switch cmd {
	case "profiles":
		app.ScanIndex(ctx, nil)
		profiles := app.Profiles(ctx)
		var lines []string
		for _, p := range profiles {
			lines = append(lines, p.ID+"\t"+p.Root)
		}
		out(strings.Join(lines, "\n"), profiles)
	case "scan", "rebuild-index":
		t := time.Now()
		var r engine.ScanReport
		if cmd == "rebuild-index" {
			var err error
			r, err = (&koto.API{App: app}).RebuildIndex(ctx)
			if err != nil {
				fatal(err)
			}
		} else {
			r = app.ScanIndex(ctx, nil)
		}
		var lines []string
		for i, e := range r.Errors {
			if i >= 20 {
				break
			}
			lines = append(lines, fmt.Sprintf("\n  %s%s: %s", e.Connector, map[bool]string{true: "/" + e.Session, false: ""}[e.Session != ""], e.Error))
		}
		out(fmt.Sprintf("sessions %d, changed %d, errors %d in %d ms%s", r.Sessions, r.Changed, len(r.Errors), time.Since(t).Milliseconds(), strings.Join(lines, "")), r)
	case "threads":
		app.ScanIndex(ctx, nil)
		n := 50
		if *limit != "" {
			n, _ = strconv.Atoi(*limit)
		}
		rows := app.SearchThreadsFiltered("", *harness, "", *profile, *account, 0)
		if len(rows) > n {
			rows = rows[:n]
		}
		var lines []string
		for _, t := range rows {
			title := ""
			var bs []string
			for _, x := range t.Bindings {
				if title == "" {
					for _, k := range []string{"title", "aiTitle"} {
						if s, ok := x.Meta[k].(string); ok && s != "" {
							title = s
							break
						}
					}
				}
				ss := x.Session
				if len(ss) > 13 {
					ss = ss[:13]
				}
				bs = append(bs, x.Harness+":"+ss)
			}
			lines = append(lines, fmt.Sprintf("%s  %4d turns  %s  %s", t.ID, t.Turns, strings.Join(bs, " "), title))
		}
		out(strings.Join(lines, "\n"), rows)
	case "show":
		need(sel, "show <sel>")
		v := open(sel)
		path := v.hub.Path(v.head)
		var bs []string
		for _, x := range v.hub.Bindings(v.thread) {
			bs = append(bs, fmt.Sprintf("%s:%s", x.Harness, x.Session))
		}
		lines := []string{fmt.Sprintf("thread %s  head %s", v.thread, v.head), "bindings: " + strings.Join(bs, ", ")}
		for _, e := range path {
			lines = append(lines, describe(e))
		}
		out(strings.Join(lines, "\n"), map[string]any{"thread": v.thread, "head": v.head, "events": path})
	case "analyze":
		app.ScanIndex(ctx, nil)
		result, err := (&koto.API{App: app}).Analyze(ctx, pos[1:])
		if err != nil {
			fatal(err)
		}
		raw, _ := json.MarshalIndent(result, "", "  ")
		out(string(raw), result)
	case "carry":
		need(sel, "carry <sel> --to <connector>")
		need(*to, "carry <sel> --to <connector>")
		v := open(sel)
		mt := 0
		if *maxTokens != "" {
			mt, _ = strconv.Atoi(*maxTokens)
		}
		res, err := v.engine.Carry(ctx, engine.CarryOptions{Thread: v.thread, Head: v.head, To: *to, TargetProfile: *toProfile, TargetAccount: *toAccount, Level: *level, DryRun: *dry,
			MaxTokens: mt, Cwd: *cwd, Title: *title})
		if err != nil {
			fatal(err)
		}
		p := res.Plan
		state := "plan"
		switch {
		case res.Applied:
			state = "delivered"
		case p.Mode == "noop":
			state = "nothing to do"
		case p.Mode == "reject":
			state = "rejected"
		}
		session := p.Target.Session
		if s, ok := res.Receipt["session"].(string); ok {
			session = s
		}
		byKind := map[string]int{}
		var kinds []string
		for _, l := range p.Losses {
			k := l.Kind + " " + l.Field
			if byKind[k] == 0 {
				kinds = append(kinds, k)
			}
			byKind[k]++
		}
		recs := len(p.Records)
		if recs == 0 {
			if n, ok := core.Num(p.Extra["shadowRecords"]); ok {
				recs = int(n)
			}
		}
		lines := []string{fmt.Sprintf("%s: %s %s:%s (%s)", state, p.Mode, p.Harness, session, p.Level)}
		if p.Target.Path != "" {
			lines = append(lines, "file: "+p.Target.Path)
		}
		lines = append(lines, fmt.Sprintf("records: %d", recs))
		ls := fmt.Sprintf("losses: %d", len(p.Losses))
		for _, k := range kinds {
			ls += fmt.Sprintf("\n  %d × %s", byKind[k], k)
		}
		lines = append(lines, ls)
		for _, n := range p.Notes {
			lines = append(lines, "note: "+n)
		}
		if res.Resume != "" {
			lines = append(lines, "resume with: "+res.Resume)
		}
		pc := *p
		pc.Content = ""
		r := *res
		r.Plan = &pc
		out(strings.Join(lines, "\n"), r)
		if p.Mode == "reject" {
			defer os.Exit(1)
		}
	case "mcp":
		if err := app.ServeMCP(ctx, os.Stdin, os.Stdout); err != nil {
			fatal(err)
		}
	case "trash":
		if sel == "" {
			es := app.Store.Trash(false)
			var lines []string
			for _, e := range es {
				var is []string
				for _, i := range e.Items {
					ss := i.Session
					if len(ss) > 13 {
						ss = ss[:13]
					}
					is = append(is, i.Harness+":"+ss)
				}
				t := ""
				if e.Title != nil {
					t = *e.Title
				}
				lines = append(lines, fmt.Sprintf("%s  %s  %s  %s", e.TrashID, time.UnixMilli(e.TrashedAt).UTC().Format(time.RFC3339), strings.Join(is, " "), t))
			}
			if len(lines) == 0 {
				lines = []string{"(trash is empty)"}
			}
			out(strings.Join(lines, "\n"), es)
			break
		}
		v := open(sel)
		e, err := app.Trash.Put(ctx, store.Context{Hub: v.hub, Engine: v.engine, Thread: v.thread, Selector: sel}, *harness)
		if err != nil {
			fatal(err)
		}
		lines := []string{fmt.Sprintf("moved to the trash as %s (koto restore %s):", e.TrashID, e.TrashID)}
		for _, i := range e.Items {
			what := "hidden in koto only"
			switch i.Via {
			case "move":
				what = fmt.Sprintf("%d path(s) moved", len(i.Files))
			case "archive":
				what = "archived in its harness"
			}
			note := ""
			if i.Note != nil {
				note = " (" + *i.Note + ")"
			}
			lines = append(lines, fmt.Sprintf("  %s:%s  %s%s", i.Harness, i.Session, what, note))
		}
		out(strings.Join(lines, "\n"), e)
	case "restore":
		need(sel, "restore <trash-id>")
		e, err := app.Trash.Restore(ctx, sel)
		if err != nil {
			fatal(err)
		}
		var is []string
		for _, i := range e.Items {
			is = append(is, i.Harness+":"+i.Session)
		}
		out("restored "+strings.Join(is, ", "), e)
	case "connectors":
		cs := app.Connectors()
		sort.SliceStable(cs, func(i, j int) bool { return false })
		var lines []string
		for _, c := range cs {
			sink := "no"
			if c.Sink && c.Capabilities.Write != nil {
				sink = c.Capabilities.Write.Mode
			}
			src := "no "
			if c.Source {
				src = "yes"
			}
			e := ""
			if c.Error != "" {
				e = "  " + c.Error
			}
			lines = append(lines, fmt.Sprintf("%-14s %-6s source:%s sink:%s  %s%s", c.ID, c.Status, src, sink, c.Version, e))
		}
		out(strings.Join(lines, "\n"), cs)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %s\n%s", cmd, help)
		os.Exit(2)
	}
}

func isBool(f string) bool {
	switch strings.TrimLeft(f, "-") {
	case "force", "dry-run", "json", "help", "h", "current":
		return true
	}
	return false
}

type stderrLog struct{}

func (stderrLog) Info(m string) { fmt.Fprintln(os.Stderr, m) }
func (stderrLog) Warn(m string) { fmt.Fprintln(os.Stderr, "warn: "+m) }

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error: "+err.Error())
	os.Exit(1)
}

func describe(e *core.Event) string {
	var bits []string
	for _, p := range e.Parts {
		switch core.S(p["type"]) {
		case "text":
			bits = append(bits, core.S(p["text"]))
		case "tool_call":
			bits = append(bits, "⚙ "+core.S(p["rawName"]))
		case "tool_result":
			bits = append(bits, "↳ "+core.S(p["outcome"]))
		case "reasoning":
			bits = append(bits, "💭")
		default:
			bits = append(bits, "["+core.S(p["type"])+"]")
		}
	}
	text := []rune(strings.Join(strings.Fields(strings.Join(bits, " ")), " "))
	if len(text) > 100 {
		text = text[:100]
	}
	id := e.ID
	if len(id) >= 23 {
		id = id[11:23]
	}
	return fmt.Sprintf("%s %-11s %-9s %s", id, e.Kind, e.Actor, string(text))
}
