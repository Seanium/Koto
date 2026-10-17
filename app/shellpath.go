package main

// An app opened from Finder (or a Linux desktop launcher) gets a bare PATH, so the core could not
// find codex, claude or the user's own tools. Ask the user's login shell for its PATH once at start
// and put any folders it adds after ours.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const pathMark = "__KOTO_PATH__"

func adoptLoginPath() {
	if runtime.GOOS == "windows" {
		return
	}
	sh := os.Getenv("SHELL")
	if sh == "" || !filepath.IsAbs(sh) {
		sh = "/bin/zsh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	// -i too: many people set PATH in .zshrc / .bashrc. Markers fence off anything the rc files print.
	out, err := exec.CommandContext(ctx, sh, "-ilc", `printf '`+pathMark+`%s`+pathMark+`' "$PATH"`).Output()
	if err != nil && len(out) == 0 {
		return
	}
	if p := mergePath(os.Getenv("PATH"), between(string(out), pathMark)); p != "" {
		_ = os.Setenv("PATH", p)
	}
}

func between(s, mark string) string {
	i := strings.Index(s, mark)
	if i < 0 {
		return ""
	}
	s = s[i+len(mark):]
	if j := strings.Index(s, mark); j >= 0 {
		return s[:j]
	}
	return ""
}

// mergePath keeps the current PATH first and appends the shell's folders it lacks.
func mergePath(cur, more string) string {
	seen := map[string]bool{}
	var out []string
	for _, list := range []string{cur, more} {
		for _, d := range filepath.SplitList(list) {
			if d != "" && !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}
