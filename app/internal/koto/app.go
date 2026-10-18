package koto

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"

	"koto/internal/connectors/antigravity"
	"koto/internal/connectors/claudecode"
	"koto/internal/connectors/codex"
	"koto/internal/connectors/cursor"
	"koto/internal/connectors/dsh"
	"koto/internal/connectors/opencode"
	"koto/internal/connectors/pi"
	"koto/internal/connectors/workbuddy"
	"koto/internal/connectors/zcode"
	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/hub"
	"koto/internal/store"
)

// Config for New. Empty fields take the platform defaults.
type Config struct {
	DataDir, ClaudeRoot, PiRoot, CodexHome, CodexShadow, CodexBin, DshHome, AntigravityRoot, OpenCodeRoot, ZCodeRoot string
	WorkBuddyRoot, WorkBuddyAIRoot, ZCodeBin                                                                         string
	CursorRoot, CursorBin                                                                                            string
	CursorAppRoot                                                                                                    string
	// ClaudeDesktopDirs: nil = every existing profile (unless ClaudeRoot is given); NoDesktop disables.
	ClaudeDesktopDirs []string
	NoDesktop         bool
	// Enable: which built-in connectors (nil = all).
	Enable []string
	Extra  []*engine.Connector
	Log    engine.Logger
}

// ConnectorInfo is a registered connector as the UI and CLI show it.
type ConnectorInfo struct {
	ID           string            `json:"id"`
	Harness      string            `json:"harness"`
	Version      string            `json:"version"`
	Variant      string            `json:"variant"`
	Default      bool              `json:"default"`
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
	Argv         []string          `json:"argv"`
	Command      string            `json:"command"`
	Cwd          *string           `json:"cwd"`
	Env          map[string]string `json:"env"`
	Shell        string            `json:"shell"`
	Instructions string            `json:"instructions,omitempty"`
}

// BindingView is a native session with how to open it.
type BindingView struct {
	hub.Binding
	Resume *Resume `json:"resume"`
}

// ThreadListing is one conversation in the list, from the index alone.
type ThreadListing struct {
	ID       string        `json:"id"`
	Revision string        `json:"revision"`
	Events   int           `json:"events"`
	Updated  *float64      `json:"updated"`
	Turns    int           `json:"turns"`
	Bindings []BindingView `json:"bindings"`
}

var rootEnv = map[string]string{"claude-code": "CLAUDE_CONFIG_DIR", "pi": "PI_CODING_AGENT_DIR", "codex": "CODEX_HOME", "dsh": "DSH_HOME"}

// RootHarnesses accept an extra storage root.
var RootHarnesses = []string{"claude-code", "pi", "codex", "dsh", "antigravity", "opencode", "zcode", "workbuddy", "workbuddy-ai", "cursor"}

type extraRoot struct {
	ID      string `json:"id"`
	Harness string `json:"harness"`
	Root    string `json:"root"`
	Variant string `json:"variant,omitempty"`
}

// App is one koto instance over a data dir.
type App struct {
	Cfg      Config
	Defaults Defaults
	DataDir  string
	Store    *store.Store
	Registry *engine.Registry
	Sessions *store.Sessions
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
	registerPrimary := func(c *engine.Connector) {
		if c.ProfileKind == "cowork" {
			// An explicit CLAUDE_CONFIG_DIR may point inside a native Cowork task.
			// Keep its detected product/identity and read-only capabilities.
			_ = r.Register(c)
			return
		}
		variant := engine.DefaultVariant(c.Harness)
		id := engine.ProfileID(c.Harness, c.Root, variant)
		database := ""
		if native, ok := c.Source.(interface{ DatabasePath() string }); ok && c.Harness == "opencode" {
			database = native.DatabasePath()
			if normalizedRoot(database) != normalizedRoot(filepath.Join(c.Root, "opencode.db")) {
				id = engine.ProfileID(c.Harness, c.Root, variant+"\x00database:"+normalizedRoot(database))
			}
		}
		primary, err := a.makeExtra(extraRoot{ID: id, Harness: c.Harness, Root: c.Root, Variant: variant})
		if err != nil {
			return
		}
		if c.Harness == "codex" {
			primary = codex.New(codex.Options{ID: id, Home: c.Root, Shadow: or(cfg.CodexShadow, filepath.Join(dataDir, "codex-shadow")), Bin: cfg.CodexBin})
		}
		if database != "" {
			primary = opencode.New(opencode.Options{ID: id, Root: c.Root, DBPath: database})
			if id != engine.ProfileID(c.Harness, c.Root, variant) {
				primary.ProfileLabel = database
			}
		}
		primary.Default, primary.Variant = true, variant
		_ = r.Register(primary)
	}
	desktop := cfg.ClaudeDesktopDirs
	if cfg.NoDesktop {
		desktop = nil
	} else if desktop == nil && cfg.ClaudeRoot == "" {
		desktop = existing(d.ClaudeDesktopDirs)
	}
	if on(cfg.Enable, "claude-code") {
		claudeRoot := or(cfg.ClaudeRoot, d.ClaudeRoot)
		primary := claudecode.New(claudecode.Options{Root: claudeRoot})
		registerPrimary(primary)
		if primary.ProfileKind != "cowork" {
			for _, p := range claudecode.DiscoverDesktopProfiles(desktop, claudeRoot) {
				c := claudecode.New(claudecode.Options{Root: p.Root, ID: p.ID, DesktopScope: p.Scope, Identity: p.Identity})
				c.ProfileLabel, c.ProfileKind = p.Label, "desktop-account"
				_ = r.Register(c)
			}
		}
		// Each Claude Desktop Cowork session is its own storage root.
		for _, c := range claudecode.DiscoverCoworkRoots(desktop) {
			_ = r.Register(claudecode.NewCowork(c))
		}
	}
	if on(cfg.Enable, "pi") {
		registerPrimary(pi.New(pi.Options{Root: or(cfg.PiRoot, d.PiRoot)}))
	}
	if on(cfg.Enable, "codex") {
		registerPrimary(codex.New(codex.Options{Home: or(cfg.CodexHome, d.CodexHome), Shadow: or(cfg.CodexShadow, filepath.Join(dataDir, "codex-shadow")), Bin: cfg.CodexBin}))
	}
	if on(cfg.Enable, "dsh") {
		registerPrimary(dsh.New(dsh.Options{Home: or(cfg.DshHome, d.DshHome)}))
	}
	if on(cfg.Enable, "antigravity") {
		registerPrimary(antigravity.New(antigravity.Options{Root: or(cfg.AntigravityRoot, d.AntigravityRoot)}))
	}
	if on(cfg.Enable, "opencode") {
		registerPrimary(opencode.New(opencode.Options{Root: cfg.OpenCodeRoot}))
	}
	if on(cfg.Enable, "zcode") {
		registerPrimary(zcode.New(zcode.Options{Root: cfg.ZCodeRoot, Command: optionalCommand(cfg.ZCodeBin)}))
	}
	if on(cfg.Enable, "workbuddy") {
		registerPrimary(workbuddy.New(workbuddy.Options{Root: cfg.WorkBuddyRoot}))
	}
	if on(cfg.Enable, "workbuddy-ai") {
		registerPrimary(workbuddy.New(workbuddy.Options{Root: cfg.WorkBuddyAIRoot, Edition: "international"}))
	}
	if on(cfg.Enable, "cursor") {
		registerPrimary(cursor.NewCLI(cursor.CLIOptions{Root: cfg.CursorRoot, ID: "cursor", Command: optionalCommand(cfg.CursorBin)}))
	}
	if on(cfg.Enable, "cursor") && !cfg.NoDesktop {
		if cfg.CursorAppRoot != "" {
			variant := "ide"
			c := cursor.New(cursor.Options{Root: cfg.CursorAppRoot, ID: engine.ProfileID("cursor", cfg.CursorAppRoot, variant)})
			c.Variant, c.ProfileKind = variant, "app"
			_ = r.Register(c)
		} else if cfg.CursorRoot == "" {
			for _, found := range cursor.DefaultAppProfiles() {
				variant := "ide"
				c := cursor.New(cursor.Options{Root: found.Root, ID: engine.ProfileID("cursor", found.Root, variant)})
				c.Variant, c.ProfileKind = variant, "app"
				_ = r.Register(c)
			}
		}
	}
	a.registerDefaultProfiles()
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
	a.Sessions.Log = cfg.Log
	a.Hub = hub.New()
	a.Engine = engine.New(a.Hub, r)
	a.Engine.Log, a.Engine.Locks = cfg.Log, store.NewEngineLocks(st)
	a.Sessions.Jobs = a.Engine.Jobs
	return a, nil
}

var unsafeID = regexp.MustCompile(`[^\w.-]`)

func (a *App) makeExtra(x extraRoot) (*engine.Connector, error) {
	if x.Variant != "" && x.Variant != engine.DefaultVariant(x.Harness) {
		if x.Harness != "cursor" || x.Variant != "ide" {
			return nil, fmt.Errorf("unsupported %s variant %s", x.Harness, x.Variant)
		}
		c := cursor.New(cursor.Options{Root: x.Root, ID: x.ID})
		c.Variant, c.ProfileKind = x.Variant, "app"
		return c, nil
	}

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
	case "zcode":
		return zcode.New(zcode.Options{Root: x.Root, ID: x.ID, Command: optionalCommand(a.Cfg.ZCodeBin)}), nil
	case "workbuddy":
		return workbuddy.New(workbuddy.Options{Root: x.Root, ID: x.ID}), nil
	case "workbuddy-ai":
		return workbuddy.New(workbuddy.Options{Root: x.Root, ID: x.ID, Edition: "international"}), nil
	case "cursor":
		return cursor.NewCLI(cursor.CLIOptions{Root: x.Root, ID: x.ID, Command: optionalCommand(a.Cfg.CursorBin)}), nil
	}
	return nil, fmt.Errorf("%s does not take extra roots; supported: %s", x.Harness, strings.Join(RootHarnesses, ", "))
}

func optionalCommand(bin string) []string {
	if bin == "" {
		return nil
	}
	return []string{bin}
}

// OpenThread opens one native conversation's in-memory view.
func (a *App) OpenThread(ctx context.Context, selector string) (*store.OpenThread, error) {
	return a.Sessions.OpenThread(ctx, selector)
}

func (a *App) info(s engine.ConnectorState) ConnectorInfo {
	c := s.Connector
	return ConnectorInfo{ID: c.ID, Harness: c.Harness, Version: c.Version, Variant: engine.ProfileVariant(c), Default: c.Default, Root: c.Root, Status: s.Status, Enabled: s.Status != "disabled", Error: s.Error,
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
	if c := a.Registry.Get(id); c != nil {
		id = c.ID
	}
	for _, s := range a.Registry.List() {
		if s.Connector.ID == id {
			return a.info(s), nil
		}
	}
	return ConnectorInfo{}, fmt.Errorf("unknown connector %s", id)
}

// SetConnectorEnabled enables or disables a connector, kept in the settings.
func (a *App) SetConnectorEnabled(id string, enabled bool) (ConnectorInfo, error) {
	if c := a.Registry.Get(id); c != nil {
		id = c.ID
	}
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
	return a.withResume(b, true)
}

func (a *App) withResume(b hub.Binding, refreshProject bool) BindingView {
	// Mutable native project metadata can change while the message view stays
	// cached. Return a fresh presentation map without modifying that shared Hub.
	meta := core.JsonObject{}
	for k, v := range b.Meta {
		meta[k] = v
	}
	if historical, ok := meta["historicalCwd"].(string); ok {
		meta["cwd"] = historical
	}
	if refreshProject && a.Store != nil {
		if row, ok := a.Store.Session(b.Harness, b.Session, b.Connector); ok {
			if historical, ok := row.Meta["cwd"].(string); ok {
				meta["cwd"] = historical
			}
			for _, k := range []string{"currentCwd", "currentProject"} {
				if v, exists := row.Meta[k]; exists && v != nil {
					meta[k] = v
				} else {
					delete(meta, k)
				}
			}
		}
	}
	if current := core.CurrentDirectory(meta); current != "" {
		meta["historicalCwd"], meta["cwd"] = meta["cwd"], current
	}
	b.Meta = meta
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
	envs := engine.ResumeEnvironment(c, b.Harness, b.Session)
	defaults := map[string]string{"claude-code": a.Defaults.ClaudeRoot, "pi": a.Defaults.PiRoot, "codex": a.Defaults.CodexHome, "dsh": a.Defaults.DshHome}
	if v, ok := rootEnv[b.Harness]; ok && c != nil && c.Root != "" {
		r1, _ := filepath.Abs(c.Root)
		r2, _ := filepath.Abs(defaults[b.Harness])
		// Defaults include the caller's environment. Preserve an inherited
		// storage override too, so the command also works in a fresh terminal.
		if r1 != r2 || os.Getenv(v) != "" {
			envs[v] = c.Root
		}
	}
	if b.Harness == "opencode" && c != nil {
		if db, ok := c.Source.(interface{ DatabasePath() string }); ok {
			envs["OPENCODE_DB"] = db.DatabasePath()
		}
	}
	if b.Harness == "zcode" && c != nil {
		envs["ZCODE_STORAGE_DIR"] = c.Root
		if db, ok := c.Source.(interface{ DatabasePath() string }); ok {
			envs["ZCODE_SESSION_DB_PATH"] = db.DatabasePath()
		}
	}
	dir := ""
	if cwd != nil {
		dir = *cwd
	}
	resume := &Resume{Argv: argv, Command: command, Cwd: cwd, Env: envs, Shell: engine.ShellCommand(argv, dir, envs)}
	// A custom resume command may open another interface; only describe stdin
	// when the actual command uses DSH's official headless stdin mode.
	if b.Harness == "dsh" && len(argv) == 5 && argv[1] == "headless" && argv[2] == "--session-id" && argv[4] == "-" {
		resume.Instructions = "Type your next message, then submit on a new line with Ctrl+D."
		if runtime.GOOS == "windows" {
			resume.Command = engine.ShellCommandWithInputPrompt(argv, "", nil, "Message")
			resume.Shell = engine.ShellCommandWithInputPrompt(argv, dir, envs, "Message")
			resume.Instructions = "Type your next message and press Enter to submit."
		}
	}
	return BindingView{Binding: b, Resume: resume}
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
	return a.SearchThreadsFiltered(query, scope, project, "", "", limit)
}

func (a *App) SearchThreadsFiltered(query, scope, project, profile, account string, limit int) []ThreadListing {
	variant := ""
	if i := strings.Index(scope, "/"); i >= 0 {
		scope, variant = scope[:i], scope[i+1:]
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if strings.HasPrefix(query, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			query = strings.ToLower(filepath.Join(home, query[2:]))
		}
	}
	names := map[string]string{"claude-code": "Claude Code", "pi": "Pi", "codex": "Codex", "dsh": "DeepSeek Harness", "antigravity": "Antigravity CLI", "opencode": "OpenCode", "zcode": "ZCode", "workbuddy": "WorkBuddy", "workbuddy-ai": "WorkBuddy AI", "cursor": "Cursor CLI"}
	out := []ThreadListing{}
	for _, m := range a.Sessions.Rows() {
		id := core.SessionSelector(m.Connector, m.Harness, m.Session)
		if m.Turns == 0 && m.Offset != nil || core.S(m.Meta["supersededBy"]) != "" {
			continue
		}
		if variant != "" && core.S(core.Obj(m.Meta["sourceProfile"])["variant"]) != variant {
			continue
		}
		desktop := m.Harness == "claude-code" && core.S(m.Meta["frontend"]) == "claude-desktop"
		if scope == "claude-desktop" && !desktop || scope == "claude-cli" && (m.Harness != "claude-code" || desktop) || scope != "" && scope != "all" && scope != "claude-desktop" && scope != "claude-cli" && m.Harness != scope {
			continue
		}
		if project == "__no_project__" && m.Cwd != "" || project != "" && project != "__no_project__" && m.Cwd != project {
			continue
		}
		if profile != "" && profile != m.Connector {
			continue
		}
		if account != "" {
			identity := core.ReadIdentity(m.Meta)
			match := false
			if account == "__unknown__" {
				match = identity.Status == "unknown"
			} else {
				for _, claim := range identity.Claims {
					if claim.Kind == "account" && claim.ID == account {
						match = true
					}
				}
			}
			if !match {
				continue
			}
		}
		if query != "" {
			text := id + "\n" + m.Harness + ":" + m.Session + "\n" + m.Title + "\n" + m.FirstPrompt + "\n" + m.Cwd + "\n" + names[m.Harness]
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
		if title == "" && m.Offset == nil {
			title = m.Session
		}
		meta["title"] = title
		if core.S(meta["cwd"]) == "" {
			meta["cwd"] = m.Cwd
		}
		// This row is already the current index snapshot. Do not add one SQL
		// lookup per list item to refresh metadata we have just read.
		b := a.withResume(hub.Binding{Harness: m.Harness, Session: m.Session, Connector: m.Connector, Thread: id, Path: m.Path, Meta: meta}, false)
		updated := m.Updated
		revision := store.RowRevision(m)
		out = append(out, ThreadListing{ID: id, Revision: revision, Events: m.Turns, Turns: m.Turns, Updated: &updated, Bindings: []BindingView{b}})
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
func (a *App) AddRoot(harness, root string, variants ...string) (ConnectorInfo, error) {
	variant := engine.DefaultVariant(harness)
	if len(variants) > 0 && variants[0] != "" {
		variant = variants[0]
	}
	if variant != engine.DefaultVariant(harness) && (harness != "cursor" || variant != "ide") {
		return ConnectorInfo{}, fmt.Errorf("unsupported %s variant %s", harness, variant)
	}
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
		if c.Root == "" || engine.ProfileVariant(c) != variant && c.ProfileKind != "cowork" {
			continue
		}
		cr := normalizedRoot(c.Root)
		rootKey := normalizedRoot(abs)
		if inside(rootKey, cr) || inside(cr, rootKey) {
			if cr == rootKey {
				if native, ok := c.Source.(interface{ DatabasePath() string }); ok && harness == "opencode" && normalizedRoot(native.DatabasePath()) != normalizedRoot(filepath.Join(abs, "opencode.db")) {
					continue // The environment-selected database is a separate namespace.
				}
				return ConnectorInfo{}, fmt.Errorf("%s is already registered as %s", abs, c.ID)
			}
			return ConnectorInfo{}, fmt.Errorf("%s overlaps the root of %s", abs, c.ID)
		}
	}
	x := extraRoot{ID: engine.ProfileID(harness, abs, variant), Harness: harness, Root: abs, Variant: variant}
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
	a.Sessions.Shutdown()
	a.Registry.Dispose()
	a.Engine.Jobs.Clear()
	a.Hub.Close()
	a.Store.Close()
}
