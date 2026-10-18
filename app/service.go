package main

// AppService is what the frontend calls (Wails generates TS bindings for it): the core's API, and
// the shell's own things (clipboard, windows, openers, preferences).

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"

	"koto/internal/engine"
	"koto/internal/koto"
	"koto/internal/store"
	"koto/internal/update"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type AppService struct {
	core    *Core
	ui      *UI
	prefs   *prefStore
	mark    func(string)
	updates *appUpdater
}

// apiErr: a failure the frontend can read (JSON {status, code, error, detail}).
func apiErr(err error) error {
	if err == nil {
		return nil
	}
	b, _ := json.Marshal(koto.AsAPIError(err))
	return errors.New(string(b))
}

var errStarting = &koto.APIError{Status: 503, Code: "STARTING", Msg: "the core is starting"}

func (s *AppService) api() (*koto.API, error) {
	if a := s.core.API(); a != nil {
		return a, nil
	}
	return nil, apiErr(errStarting)
}

func call[T any](s *AppService, fn func(a *koto.API) (T, error)) (T, error) {
	var zero T
	a, release := s.core.acquire()
	defer release()
	if a == nil {
		return zero, apiErr(errStarting)
	}
	v, err := fn(a)
	if err != nil {
		return zero, apiErr(err)
	}
	return v, nil
}

// Wails has already canceled its promise when this context is canceled. Return
// no late rejection to that abandoned promise; the API still returns ctx errors.
func callContext[T any](s *AppService, ctx context.Context, fn func(*koto.API) (T, error)) (T, error) {
	value, err := call(s, fn)
	if ctx.Err() != nil {
		var zero T
		return zero, nil
	}
	return value, err
}

var bg = context.Background()

func (s *AppService) Status() CoreStatus { return s.core.Status() }

// Restart reopens the core (the "重试" action after it failed).
func (s *AppService) Restart() { go s.core.Restart() }

func (s *AppService) Connectors() ([]koto.ConnectorInfo, error) {
	return call(s, func(a *koto.API) ([]koto.ConnectorInfo, error) { return a.Connectors(), nil })
}

func (s *AppService) SetConnectorEnabled(id string, enabled bool) (koto.ConnectorInfo, error) {
	return call(s, func(a *koto.API) (koto.ConnectorInfo, error) { return a.SetConnectorEnabled(id, enabled) })
}

func (s *AppService) AddConnectorRoot(harness, root, variant string) (koto.ConnectorInfo, error) {
	return call(s, func(a *koto.API) (koto.ConnectorInfo, error) { return a.AddRoot(harness, root, variant) })
}

func (s *AppService) RemoveConnector(id string) (bool, error) {
	return call(s, func(a *koto.API) (bool, error) { return true, a.RemoveRoot(bg, id) })
}

func (s *AppService) Threads(limit int) ([]koto.ThreadListing, error) {
	return call(s, func(a *koto.API) ([]koto.ThreadListing, error) { return a.Threads(limit), nil })
}

func (s *AppService) SearchThreads(query, scope, project, profile, account string) ([]koto.ThreadListing, error) {
	return call(s, func(a *koto.API) ([]koto.ThreadListing, error) {
		return a.App.SearchThreadsFiltered(query, scope, project, profile, account, 500), nil
	})
}

func (s *AppService) Profiles() ([]koto.Profile, error) {
	return call(s, func(a *koto.API) ([]koto.Profile, error) { return a.Profiles(bg), nil })
}

// ThreadPage is a page of a native session's path from the end (before: the page before that node; turns 0: the whole path).
func (s *AppService) ThreadPage(ctx context.Context, selector string, turns int, before string) (*koto.ThreadDetail, error) {
	return callContext(s, ctx, func(a *koto.API) (*koto.ThreadDetail, error) { return a.Thread(ctx, selector, turns, before) })
}

func (s *AppService) ScanStatus() (koto.ScanStatus, error) {
	return call(s, func(a *koto.API) (koto.ScanStatus, error) { return a.ScanStatus(), nil })
}

// Image is a picture from a conversation as a data: URL (sel: the conversation or a node in it).
func (s *AppService) Image(ctx context.Context, selector, sha string) (string, error) {
	return callContext(s, ctx, func(a *koto.API) (string, error) { return a.Image(ctx, selector, sha) })
}

func (s *AppService) Carry(ctx context.Context, req koto.CarryRequest) (*engine.CarryResult, error) {
	return callContext(s, ctx, func(a *koto.API) (*engine.CarryResult, error) {
		r, err := a.Carry(ctx, req)
		if err != nil || r == nil {
			return r, err
		}
		// The webview needs a preview and receipt, never the serialized native transcript.
		out := *r
		plan := *r.Plan
		plan.Content = ""
		plan.Records = nil
		plan.Extra = nil
		out.Plan = &plan
		return &out, nil
	})
}

func (s *AppService) ForgetCopy(requestID string) {
	if a := s.core.API(); a != nil {
		a.ForgetCopy(requestID)
	}
}

func (s *AppService) RebuildIndex() (engine.ScanReport, error) {
	return call(s, func(a *koto.API) (engine.ScanReport, error) { return a.RebuildIndex(bg) })
}

func (s *AppService) Scan() (engine.ScanReport, error) {
	return call(s, func(a *koto.API) (engine.ScanReport, error) { return a.Reindex(bg), nil })
}

// ---------------------------------------------------------------- shell

func (s *AppService) Copy(text string) bool {
	return application.Get().Clipboard.SetText(text)
}

// OpenMain shows the main window; nav (JSON, optional) tells it where to go.
func (s *AppService) OpenMain(nav string) { s.ui.openMain(nav) }

// OpenSettings shows the main window on its settings view; tab: "general" | "agents" | "read" (or "").
func (s *AppService) OpenSettings(tab string) { s.ui.openSettings(tab) }

// Openers lists the terminals and editors (installed ones marked) and the effective choices.
func (s *AppService) Openers() OpenersInfo { return openersInfo(s.prefs.Get()) }

// OpenInTerminal runs a shell line (command may be "": just open at cwd) in a new window of the
// chosen terminal. "copied" when that terminal cannot run it (the line went to the clipboard), "" on failure, else "ok".
func (s *AppService) OpenInTerminal(command, cwd string) string {
	p := s.prefs.Get()
	info := openersInfo(p)
	argv, clip := terminalArgv(info.Terminals, info.Terminal, p.TerminalCustom, command, cwd)
	if argv == nil || !run(argv) {
		return ""
	}
	if clip && command != "" {
		line, _ := terminalArgv([]Opener{{ID: "x", argv: []string{"{cmd}"}}}, "x", "", command, cwd)
		application.Get().Clipboard.SetText(line[0])
		return "copied"
	}
	return "ok"
}

// OpenInEditor opens a folder in the chosen editor.
func (s *AppService) OpenInEditor(path string) bool {
	p := s.prefs.Get()
	info := openersInfo(p)
	return isDir(path) && run(editorArgv(info.Editors, info.Editor, p.EditorCustom, path))
}

// RevealPath shows a project folder or session file in Finder / the file manager.
func (s *AppService) RevealPath(path string) bool {
	return canRevealPath(path) && revealPath(path)
}

func canRevealPath(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && (info.IsDir() || info.Mode().IsRegular())
}

// PathExists: is this folder still there.
func (s *AppService) PathExists(path string) bool { return isDir(path) }

// HomeDir: for showing paths under it as ~.
func (s *AppService) HomeDir() string { return homeDir() }

func (s *AppService) GetPrefs() Prefs { return s.prefs.Get() }

// SetAgentOrder only updates sidebar order, without restarting scans or changing other preferences.
func (s *AppService) SetAgentOrder(ids []string) error { return s.prefs.SetAgentOrder(ids) }

// The WebView resolves system language; the same resolved language labels native menus.
func (s *AppService) SetUILocale(locale string) {
	s.ui.localeMu.Lock()
	defer s.ui.localeMu.Unlock()
	switch locale {
	case "zh", "zh-TW", "en", "ja", "de":
	default:
		locale = "en"
	}
	if s.ui.locale.Load() == locale {
		return
	}
	s.ui.locale.Store(locale)
	// Update existing items: rebuilding would leave registered native menu items behind.
	for _, label := range s.ui.menuLabels {
		label.item.SetLabel(s.ui.text(label.key) + label.suffix)
	}
}

// SetPrefs saves display and opener preferences.
func (s *AppService) SetPrefs(p Prefs) error {
	old := s.prefs.Get()
	if err := s.prefs.Set(p); err != nil {
		return err
	}
	if old.Appearance != s.prefs.Get().Appearance {
		setAppearance(s.ui, s.prefs.Get().Appearance)
	}
	return nil
}

// McpConfig uses the separately installed CLI on the Agent's PATH.
func (s *AppService) McpConfig() string {
	cmd := "koto"
	args := []string{"mcp"}
	if sb := os.Getenv("KOTO_APP_SANDBOX"); sb != "" {
		j := func(d string) string { return filepath.Join(sb, d) }
		args = append(args, "--data", j("hub"), "--claude-root", j("claude"), "--pi-root", j("pi"), "--codex-home", j("codex"), "--dsh-home", j("dsh"), "--agy-root", j("antigravity"), "--opencode-root", j("opencode"), "--zcode-root", j("zcode"))
		args = append(args, "--workbuddy-root", j("workbuddy"), "--workbuddy-ai-root", j("workbuddy-ai"), "--cursor-root", j("cursor"))
	}
	b, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"koto": map[string]any{"command": cmd, "args": args}}}, "", "  ")
	return string(b)
}

// PickDirectory opens the native folder chooser; "" when cancelled.
func (s *AppService) PickDirectory(title string) (string, error) {
	d := application.Get().Dialog.OpenFile().
		CanChooseDirectories(true).CanChooseFiles(false).CanCreateDirectories(false).ShowHiddenFiles(true).
		SetTitle(title)
	if s.ui.main != nil {
		d = d.AttachToWindow(s.ui.main)
	}
	selected, err := d.PromptForSingleSelection()
	if err != nil {
		return "", err
	}
	return selectedDirectory(selected)
}

// Mark receives frontend readiness and verification milestones.
func (s *AppService) Mark(name string) {
	if name == "ui-ready" && s.ui != nil {
		s.ui.frontendReady()
		update.MarkReady()
	}
	if strings.HasPrefix(name, "error: ") {
		log.Printf("frontend %s", name)
		return
	}
	if s.mark != nil {
		s.mark(name)
	}
}

func navJSON(v map[string]string) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// Analyze compares explicitly selected sessions without writing Agent data.
func (s *AppService) Analyze(selectors []string) (*koto.AnalysisResult, error) {
	return call(s, func(a *koto.API) (*koto.AnalysisResult, error) { return a.Analyze(bg, selectors) })
}

func (s *AppService) Analyses() ([]store.AnalysisCollection, error) {
	return call(s, func(a *koto.API) ([]store.AnalysisCollection, error) { return a.Analyses() })
}
func (s *AppService) SaveAnalysis(c store.AnalysisCollection) (store.AnalysisCollection, error) {
	return call(s, func(a *koto.API) (store.AnalysisCollection, error) { return a.SaveAnalysis(c) })
}
func (s *AppService) DeleteAnalysis(id string) (bool, error) {
	return call(s, func(a *koto.API) (bool, error) { return true, a.DeleteAnalysis(id) })
}
