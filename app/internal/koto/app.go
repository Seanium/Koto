package koto

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"koto/internal/connectors/antigravity"
	"koto/internal/connectors/claudecode"
	"koto/internal/connectors/codex"
	"koto/internal/connectors/dsh"
	"koto/internal/connectors/opencode"
	"koto/internal/connectors/pi"
	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/hub"
	"koto/internal/store"
)

// Config for New. Empty fields take the platform defaults.
type Config struct {
	DataDir, ClaudeRoot, PiRoot, CodexHome, CodexShadow, CodexBin, DshHome, AntigravityRoot, OpenCodeRoot string
	// ClaudeDesktopDirs: nil = every existing profile (unless ClaudeRoot is given); NoDesktop disables.
	ClaudeDesktopDirs []string
	NoDesktop         bool
	// LiveMs: the live-session guard window (nil = 120 s; 0 disables).
	LiveMs *int64
	// Enable: which built-in connectors (nil = all).
	Enable []string
	Extra  []*engine.Connector
	Log    engine.Logger
	// TrashRename stands in for os.Rename in the trash (tests).
	TrashRename func(from, to string) error
}

// ConnectorInfo is a registered connector as the UI and CLI show it.
type ConnectorInfo struct {
	ID           string            `json:"id"`
	Harness      string            `json:"harness"`
	Version      string            `json:"version"`
	Root         string            `json:"root,omitempty"`
	Status       string            `json:"status"`
	Enabled      bool              `json:"enabled"`
	Error        string            `json:"error,omitempty"`
	Source       bool              `json:"source"`
	Sink         bool              `json:"sink"`
	Removable    bool              `json:"removable"`
	Capabilities core.Capabilities `json:"capabilities"`
}

// Resume is how to continue a native session in its harness: argv in cwd with env (a non-default
// storage root), or the whole thing as one shell line.
type Resume struct {
	Argv    []string          `json:"argv"`
	Command string            `json:"command"`
	Cwd     *string           `json:"cwd"`
	Env     map[string]string `json:"env"`
	Shell   string            `json:"shell"`
}

// BindingView is a native session with how to open it.
type BindingView struct {
	hub.Binding
	Resume *Resume `json:"resume"`
}

// ThreadListing is one conversation in the list, from the index alone.
type ThreadListing struct {
	ID       string        `json:"id"`
	Events   int           `json:"events"`
	Updated  *float64      `json:"updated"`
	Turns    int           `json:"turns"`
	Bindings []BindingView `json:"bindings"`
}

var rootEnv = map[string]string{"claude-code": "CLAUDE_CONFIG_DIR", "pi": "PI_CODING_AGENT_DIR", "codex": "CODEX_HOME", "dsh": "DSH_HOME"}

// RootHarnesses accept an extra storage root.
var RootHarnesses = []string{"claude-code", "pi", "codex", "dsh", "antigravity", "opencode"}

type extraRoot struct {
	ID      string `json:"id"`
	Harness string `json:"harness"`
	Root    string `json:"root"`
}

// App is one koto instance over a data dir.
type App struct {
	Cfg      Config
	Defaults Defaults
	DataDir  string
	Store    *store.Store
	Registry *engine.Registry
	Sessions *store.Sessions
	Trash    *store.Trash
	// Hub and Engine: a working copy of everything scanned (CLI and tests); the app opens one conversation at a time.
	Hub    *hub.Hub
	Engine *engine.Engine
	mu     sync.Mutex
	extras map[string]bool
}

func on(enable []string, id string) bool {
	if enable == nil {
		return true
	}
	for _, e := range enable {
		if e == id {
			return true
		}
	}
	return false
}

func existing(dirs []string) []string {
	var out []string
	for _, d := range dirs {
		if _, err := os.Stat(d); err == nil {
			out = append(out, d)
		}
	}
	return out
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// New wires an App.
func New(cfg Config) (*App, error) {
	d := PlatformDefaults()
	dataDir := or(cfg.DataDir, d.DataDir)
	st, err := store.Open(dataDir)
	if err != nil {
		return nil, err
	}
	if cfg.Log == nil {
		cfg.Log = engine.SilentLogger
	}
	a := &App{Cfg: cfg, Defaults: d, DataDir: dataDir, Store: st, Registry: engine.NewRegistry(), extras: map[string]bool{}}
	r := a.Registry
	desktop := cfg.ClaudeDesktopDirs
	if cfg.NoDesktop {
		desktop = nil
	} else if desktop == nil && cfg.ClaudeRoot == "" {
		desktop = existing(d.ClaudeDesktopDirs)
	}
	if on(cfg.Enable, "claude-code") {
		_ = r.Register(claudecode.New(claudecode.Options{Root: or(cfg.ClaudeRoot, d.ClaudeRoot), DesktopDir: desktop}))
		// Each Claude Desktop Cowork session is its own storage root.
		for _, c := range claudecode.DiscoverCoworkRoots(desktop) {
			_ = r.Register(claudecode.New(claudecode.Options{Root: c.Root, ID: c.ID}))
		}
	}
	if on(cfg.Enable, "pi") {
		_ = r.Register(pi.New(pi.Options{Root: or(cfg.PiRoot, d.PiRoot)}))
	}
	if on(cfg.Enable, "codex") {
		_ = r.Register(codex.New(codex.Options{Home: or(cfg.CodexHome, d.CodexHome), Shadow: or(cfg.CodexShadow, filepath.Join(dataDir, "codex-shadow")), Bin: cfg.CodexBin}))
	}
	if on(cfg.Enable, "dsh") {
		_ = r.Register(dsh.New(dsh.Options{Home: or(cfg.DshHome, d.DshHome)}))
	}
	if on(cfg.Enable, "antigravity") {
		_ = r.Register(antigravity.New(antigravity.Options{Root: or(cfg.AntigravityRoot, d.AntigravityRoot)}))
	}
	if on(cfg.Enable, "opencode") {
		_ = r.Register(opencode.New(opencode.Options{Root: cfg.OpenCodeRoot}))
	}
	for _, c := range cfg.Extra {
		_ = r.Register(c)
	}
	// User-managed settings: extra storage roots and disabled connectors.
	var roots []extraRoot
	st.Setting("roots", &roots)
	for _, x := range roots {
		if !on(cfg.Enable, x.Harness) || r.Get(x.ID) != nil {
			continue
		}
		if c, err := a.makeExtra(x); err == nil {
			_ = r.Register(c)
			a.extras[x.ID] = true
		}
	}
	var disabled []string
	st.Setting("disabled", &disabled)
	for _, id := range disabled {
		if r.Get(id) != nil {
			_ = r.SetEnabled(id, false)
		}
	}
	a.Sessions = store.NewSessions(st, r)
	a.Sessions.Log, a.Sessions.LiveMs = cfg.Log, cfg.LiveMs
	a.Trash = &store.Trash{Store: st, Registry: r, Dir: filepath.Join(dataDir, "trash"), Rename: cfg.TrashRename}
	a.Hub = hub.New()
	a.Engine = engine.New(a.Hub, r)
	a.Engine.Log, a.Engine.Locks = cfg.Log, store.EngineLocks{S: st}
	a.Sessions.Jobs = a.Engine.Jobs
	if cfg.LiveMs != nil {
		a.Engine.LiveMs = *cfg.LiveMs
	}
	a.Engine.Filter = a.notTrashed
	return a, nil
}

func (a *App) notTrashed(ref core.NativeRef) bool {
	for _, e := range a.Store.Trash(false) {
		for _, i := range e.Items {
			if i.Harness == ref.Harness && i.Session == ref.Session {
				return false
			}
		}
	}
	return true
}

var unsafeID = regexp.MustCompile(`[^\w.-]`)

func (a *App) makeExtra(x extraRoot) (*engine.Connector, error) {
	switch x.Harness {
	case "claude-code":
		return claudecode.New(claudecode.Options{Root: x.Root, ID: x.ID}), nil
	case "pi":
		return pi.New(pi.Options{Root: x.Root, ID: x.ID}), nil
	case "codex":
		// One shadow per Codex home: Codex's import ledger keys on the shadow path.
		return codex.New(codex.Options{Home: x.Root, ID: x.ID, Shadow: filepath.Join(a.DataDir, "codex-shadow", unsafeID.ReplaceAllString(x.ID, "_")), Bin: a.Cfg.CodexBin}), nil
	case "dsh":
		return dsh.New(dsh.Options{Home: x.Root, ID: x.ID}), nil
	case "antigravity":
		return antigravity.New(antigravity.Options{Root: x.Root, ID: x.ID}), nil
	case "opencode":
		return opencode.New(opencode.Options{Root: x.Root, ID: x.ID}), nil
	}
	return nil, fmt.Errorf("%s does not take extra roots; supported: %s", x.Harness, strings.Join(RootHarnesses, ", "))
}

// OpenThread opens one native conversation's in-memory view.
func (a *App) OpenThread(ctx context.Context, selector string) (*store.OpenThread, error) {
	return a.Sessions.OpenThread(ctx, selector)
}

func (a *App) info(s engine.ConnectorState) ConnectorInfo {
	c := s.Connector
	return ConnectorInfo{ID: c.ID, Harness: c.Harness, Version: c.Version, Root: c.Root, Status: s.Status, Enabled: s.Status != "disabled", Error: s.Error,
		Source: c.Source != nil, Sink: c.Sink != nil, Removable: a.extras[c.ID], Capabilities: c.Capabilities}
}

// Connectors with their roots and state.
func (a *App) Connectors() []ConnectorInfo {
	var out []ConnectorInfo
	for _, s := range a.Registry.List() {
		out = append(out, a.info(s))
	}
	return out
}

func (a *App) connectorInfo(id string) (ConnectorInfo, error) {
	for _, s := range a.Registry.List() {
		if s.Connector.ID == id {
			return a.info(s), nil
		}
	}
	return ConnectorInfo{}, fmt.Errorf("unknown connector %s", id)
}

// SetConnectorEnabled enables or disables a connector, kept in the settings.
func (a *App) SetConnectorEnabled(id string, enabled bool) (ConnectorInfo, error) {
	if err := a.Registry.SetEnabled(id, enabled); err != nil {
		return ConnectorInfo{}, err
	}
	var off []string
	a.Store.Setting("disabled", &off)
	set := map[string]bool{}
	for _, x := range off {
		set[x] = true
	}
	if enabled {
		delete(set, id)
	} else {
		set[id] = true
	}
	list := []string{}
	for x := range set {
		list = append(list, x)
	}
	sort.Strings(list)
	a.Store.SetSetting("disabled", list)
	return a.connectorInfo(id)
}

// WithResume adds how to open a binding in its harness.
func (a *App) WithResume(b hub.Binding) BindingView {
	c := a.Registry.Get(b.Connector)
	if c == nil {
		if cs := a.Registry.ByHarness(b.Harness); len(cs) > 0 {
			c = cs[0]
		}
	}
	argv := engine.ResumeArgv(c, b.Harness, b.Session)
	if argv == nil {
		return BindingView{Binding: b}
	}
	command := engine.NativeShellLine(argv)
	var cwd *string
	if s, ok := b.Meta["cwd"].(string); ok && s != "" {
		cwd = &s
	}
	envs := map[string]string{}
	defaults := map[string]string{"claude-code": a.Defaults.ClaudeRoot, "pi": a.Defaults.PiRoot, "codex": a.Defaults.CodexHome, "dsh": a.Defaults.DshHome}
	if v, ok := rootEnv[b.Harness]; ok && c != nil && c.Root != "" {
		r1, _ := filepath.Abs(c.Root)
		r2, _ := filepath.Abs(defaults[b.Harness])
		if r1 != r2 {
			envs[v] = c.Root
		}
	}
	if b.Harness == "opencode" && c != nil {
		if db, ok := c.Source.(interface{ DatabasePath() string }); ok {
			envs["OPENCODE_DB"] = db.DatabasePath()
		}
	}
	dir := ""
	if cwd != nil {
		dir = *cwd
	}
	return BindingView{Binding: b, Resume: &Resume{Argv: argv, Command: command, Cwd: cwd, Env: envs, Shell: engine.ShellCommand(argv, dir, envs)}}
}

// Bindings of a thread in a working copy, each with how to open it.
func (a *App) Bindings(h *hub.Hub, thread string) []BindingView {
	var out []BindingView
	for _, b := range h.Bindings(thread) {
		out = append(out, a.WithResume(b))
	}
	return out
}

// ListThreads is the conversation list from the index (no content read), newest first.
func (a *App) ListThreads() []ThreadListing {
	return a.SearchThreads("", "", "", 0)
}

// SearchThreads searches only indexed metadata, before applying the recent-list limit.
// No native conversation or attachment is opened for a keystroke.
func (a *App) SearchThreads(query, scope, project string, limit int) []ThreadListing {
	query = strings.ToLower(strings.TrimSpace(query))
	if strings.HasPrefix(query, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			query = strings.ToLower(filepath.Join(home, query[2:]))
		}
	}
	names := map[string]string{"claude-code": "Claude Code", "pi": "Pi", "codex": "Codex", "dsh": "DeepSeek Harness", "antigravity": "Antigravity CLI", "opencode": "OpenCode"}
	trashed := map[string]bool{}
	for _, e := range a.Store.Trash(false) {
		for _, i := range e.Items {
			trashed[i.Harness+":"+i.Session] = true
		}
	}

	out := []ThreadListing{}
	for _, m := range a.Store.Sessions() {
		id := m.Harness + ":" + m.Session
		if trashed[id] || m.Turns == 0 || core.S(m.Meta["supersededBy"]) != "" {
			continue
		}
		desktop := m.Harness == "claude-code" && core.S(m.Meta["frontend"]) == "claude-desktop"
		if scope == "claude-desktop" && !desktop || scope == "claude-cli" && (m.Harness != "claude-code" || desktop) || scope != "" && scope != "all" && scope != "claude-desktop" && scope != "claude-cli" && m.Harness != scope {
			continue
		}
		if project == "__no_project__" && m.Cwd != "" || project != "" && project != "__no_project__" && m.Cwd != project {
			continue
		}
		if query != "" {
			text := id + "\n" + m.Title + "\n" + m.FirstPrompt + "\n" + m.Cwd + "\n" + names[m.Harness]
			if desktop {
				text += "\nClaude Desktop"
			}
			if !strings.Contains(strings.ToLower(text), query) {
				continue
			}
		}
		meta := core.JsonObject{}
		for k, v := range m.Meta {
			meta[k] = v
		}
		title := m.Title
		if title == "" {
			title = m.FirstPrompt
		}
		meta["title"], meta["cwd"] = title, m.Cwd
		b := a.WithResume(hub.Binding{Harness: m.Harness, Session: m.Session, Connector: m.Connector, Thread: id, Path: m.Path, Meta: meta})
		updated := m.Updated
		out = append(out, ThreadListing{ID: id, Events: m.Turns, Turns: m.Turns, Updated: &updated, Bindings: []BindingView{b}})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if *out[i].Updated != *out[j].Updated {
			return *out[i].Updated > *out[j].Updated
		}
		return out[i].ID < out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// AddRoot registers an extra storage root for a harness (a second CLAUDE_CONFIG_DIR, …), kept in the settings.
func (a *App) AddRoot(harness, root string) (ConnectorInfo, error) {
	ok := false
	for _, h := range RootHarnesses {
		if h == harness {
			ok = true
		}
	}
	if !ok {
		return ConnectorInfo{}, fmt.Errorf("%s does not take extra roots; supported: %s", harness, strings.Join(RootHarnesses, ", "))
	}
	if !on(a.Cfg.Enable, harness) {
		return ConnectorInfo{}, fmt.Errorf("%s is not enabled in this run", harness)
	}
	abs, _ := filepath.Abs(root)
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return ConnectorInfo{}, fmt.Errorf("%s is not a directory", abs)
	}
	real := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		r, _ := filepath.Abs(p)
		return r
	}
	abs = real(abs)
	inside := func(x, y string) bool {
		return x == y || strings.HasPrefix(x, strings.TrimSuffix(y, string(filepath.Separator))+string(filepath.Separator))
	}
	for _, c := range a.Registry.ByHarness(harness) {
		if c.Root == "" {
			continue
		}
		cr := real(c.Root)
		if inside(abs, cr) || inside(cr, abs) {
			if cr == abs {
				return ConnectorInfo{}, fmt.Errorf("%s is already registered as %s", abs, c.ID)
			}
			return ConnectorInfo{}, fmt.Errorf("%s overlaps the root of %s", abs, c.ID)
		}
	}
	x := extraRoot{ID: harness + "@" + core.SHA256Hex(abs)[:8], Harness: harness, Root: abs}
	c, err := a.makeExtra(x)
	if err != nil {
		return ConnectorInfo{}, err
	}
	if err := a.Registry.Register(c); err != nil {
		return ConnectorInfo{}, err
	}
	a.mu.Lock()
	a.extras[x.ID] = true
	a.mu.Unlock()
	var roots []extraRoot
	a.Store.Setting("roots", &roots)
	var next []extraRoot
	for _, r := range roots {
		if r.ID != x.ID {
			next = append(next, r)
		}
	}
	a.Store.SetSetting("roots", append(next, x))
	return a.connectorInfo(x.ID)
}

// RemoveRoot removes a root added with AddRoot (its sessions stay where they are).
func (a *App) RemoveRoot(ctx context.Context, id string) error {
	a.mu.Lock()
	extra := a.extras[id]
	a.mu.Unlock()
	if !extra {
		if a.Registry.Get(id) != nil {
			return fmt.Errorf("%s is built in; disable it instead", id)
		}
		return fmt.Errorf("unknown connector %s", id)
	}
	a.mu.Lock()
	delete(a.extras, id)
	a.mu.Unlock()
	a.Registry.Unregister(id)
	var roots []extraRoot
	a.Store.Setting("roots", &roots)
	var next []extraRoot
	for _, r := range roots {
		if r.ID != id {
			next = append(next, r)
		}
	}
	if next == nil {
		next = []extraRoot{}
	}
	a.Store.SetSetting("roots", next)
	var disabled []string
	a.Store.Setting("disabled", &disabled)
	var nd []string
	for _, d := range disabled {
		if d != id {
			nd = append(nd, d)
		}
	}
	if nd == nil {
		nd = []string{}
	}
	a.Store.SetSetting("disabled", nd)
	return nil
}

// Close lets go of everything.
func (a *App) Close() {
	a.Registry.Dispose()
	a.Engine.Jobs.Clear()
	a.Sessions.Close()
	a.Hub.Close()
	a.Store.Close()
}
