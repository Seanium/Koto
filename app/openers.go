package main

// Openers: which terminal runs a resume command, which editor opens a project folder. Each is a row
// in a preset table (platform_*.go): how to tell it is installed, and an argv template. Templates
// are substituted word by word, never through a shell, so a path or command with spaces or quotes
// stays one argument. Placeholders:
//
//	{cwd}     the folder to start in ("" when the command carries its own cd)
//	{cmd}     the shell line to run (cd into {cwd} first when one is given)
//	{cmd_as}  {cmd} escaped for the inside of an AppleScript string ({cwd_as} likewise)
//	{cwd_url} {cwd} escaped for a URL query
//	{path}    the folder to open (editors)
//
// A "custom" preset takes the user's own template from the preferences, split on whitespace.

import (
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type Opener struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Installed: detected on this machine (custom is always true).
	Installed bool `json:"installed"`
	// Detect: a bundle id (macOS) or a program on PATH; "" = always present.
	detect string
	argv   []string
	// clip: also copy {cmd} to the clipboard (a terminal that cannot be told what to run).
	clip bool
}

const customOpener = "custom"

// OpenersInfo is what Settings and the project menu show.
type OpenersInfo struct {
	Terminals   []Opener `json:"terminals"`
	Editors     []Opener `json:"editors"`
	Terminal    string   `json:"terminal"` // the effective choice ("" = none installed)
	Editor      string   `json:"editor"`
	FileManager string   `json:"fileManager"` // Finder / 资源管理器 / 文件管理器
}

var (
	detectMu    sync.Mutex
	detectCache = map[string]bool{}
)

// installed reports (and caches) whether a preset's app or program exists.
func installed(o Opener) bool {
	if o.detect == "" {
		return true
	}
	detectMu.Lock()
	defer detectMu.Unlock()
	v, ok := detectCache[o.detect]
	if !ok {
		v = appInstalled(o.detect)
		detectCache[o.detect] = v
	}
	return v
}

func withDetection(list []Opener) []Opener {
	out := make([]Opener, 0, len(list)+1)
	for _, o := range list {
		o.Installed = installed(o)
		out = append(out, o)
	}
	return append(out, Opener{ID: customOpener, Name: "自定义命令…", Installed: true})
}

// pick: the chosen preset if it is installed (custom only with a template), else the first installed one.
func pick(list []Opener, chosen, custom string) string {
	for _, o := range list {
		if o.ID == chosen && o.Installed && (o.ID != customOpener || strings.TrimSpace(custom) != "") {
			return o.ID
		}
	}
	for _, o := range list {
		if o.Installed && o.ID != customOpener {
			return o.ID
		}
	}
	return ""
}

func openersInfo(p Prefs) OpenersInfo {
	t, e := withDetection(terminalPresets()), withDetection(editorPresets())
	return OpenersInfo{Terminals: t, Editors: e, Terminal: pick(t, p.Terminal, p.TerminalCustom), Editor: pick(e, p.Editor, p.EditorCustom), FileManager: fileManagerName}
}

// shellQuote quotes s for a POSIX shell when it needs it.
func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("@%+=:,./-_", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// expand substitutes the placeholders into each word of a template.
func expand(argv []string, vars map[string]string) []string {
	out := make([]string, len(argv))
	for i, w := range argv {
		for k, v := range vars {
			w = strings.ReplaceAll(w, "{"+k+"}", v)
		}
		out[i] = w
	}
	return out
}

// terminalArgv: what to run to open the command in the chosen terminal (nil = nothing to run).
func terminalArgv(list []Opener, id, custom, command, cwd string) (argv []string, clip bool) {
	line := command
	if cwd != "" {
		line = "cd " + shellQuote(cwd) + " && " + command
		if command == "" {
			line = "cd " + shellQuote(cwd)
		}
	}
	dir := cwd
	if dir == "" {
		dir = homeDir()
	}
	as := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	vars := map[string]string{"cwd": dir, "cwd_as": as.Replace(dir), "cwd_url": url.QueryEscape(dir), "cmd": line, "cmd_as": as.Replace(line)}
	for _, o := range list {
		if o.ID != id {
			continue
		}
		if o.ID == customOpener {
			return expand(strings.Fields(custom), vars), false
		}
		return expand(o.argv, vars), o.clip
	}
	return nil, false
}

func editorArgv(list []Opener, id, custom, path string) []string {
	vars := map[string]string{"path": path}
	for _, o := range list {
		if o.ID != id {
			continue
		}
		if o.ID == customOpener {
			return expand(strings.Fields(custom), vars)
		}
		return expand(o.argv, vars)
	}
	return nil
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// run starts argv and reports whether it worked: a quick helper (osascript, open) must exit 0
// within a few seconds; a program still running by then (a custom terminal command) counts as started.
func run(argv []string) bool {
	if len(argv) == 0 || argv[0] == "" {
		return false
	}
	c := exec.Command(argv[0], argv[1:]...)
	if c.Start() != nil {
		return false
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	select {
	case err := <-done:
		return err == nil
	case <-time.After(3 * time.Second):
		return true
	}
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
