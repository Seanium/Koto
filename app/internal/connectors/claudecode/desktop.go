package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"koto/internal/core"
)

var localJSON = regexp.MustCompile(`^local_.*\.json$`)

// ReadDesktopIndex: cliSessionId → Claude Desktop metadata (Code tab titles), from
// claude-code-sessions/<account>/<org>/local_<id>.json of every Desktop profile.
func ReadDesktopIndex(dirs []string) map[string]core.JsonObject {
	out := map[string]core.JsonObject{}
	for _, d := range dirs {
		walk(filepath.Join(d, "claude-code-sessions"), 2, func(f string) {
			if !localJSON.MatchString(filepath.Base(f)) {
				return
			}
			b, err := os.ReadFile(f)
			if err != nil {
				return
			}
			var j map[string]any
			if json.Unmarshal(b, &j) != nil {
				return // partial write by Desktop: skip this round
			}
			id, ok := j["cliSessionId"].(string)
			if !ok {
				return
			}
			out[id] = core.JsonObject{"frontend": "claude-desktop", "desktopSessionId": strOrNil(j["sessionId"]), "desktopTitle": strOrNil(j["title"]),
				"desktopArchived": j["isArchived"] == true, "desktopProfile": filepath.Base(d)}
		})
	}
	return out
}

// Root is a Claude Code storage root found elsewhere (a Cowork session).
type Root struct{ ID, Root string }

var profileRe = regexp.MustCompile(`(?i)^Claude-(.+)$`)
var unsafe = regexp.MustCompile(`[^\w.-]`)

// DiscoverCoworkRoots: local-agent-mode-sessions/<account>/<org>/local_<id>/.claude.
func DiscoverCoworkRoots(dirs []string) []Root {
	var out []Root
	for _, d := range dirs {
		profile := ""
		if m := profileRe.FindStringSubmatch(filepath.Base(d)); m != nil {
			profile = unsafe.ReplaceAllString(m[1], "_")
		}
		suffix := ""
		if profile != "" {
			suffix = "-" + profile
		}
		walkDirs(filepath.Join(d, "local-agent-mode-sessions"), 3, func(p string) {
			name := filepath.Base(p)
			if strings.HasPrefix(name, "local_") && exists(filepath.Join(p, ".claude")) {
				out = append(out, Root{ID: "claude-code@cowork" + suffix + ":" + name[6:], Root: filepath.Join(p, ".claude")})
			}
		})
	}
	return out
}

func walk(dir string, depth int, fn func(string)) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if depth > 0 {
				walk(p, depth-1, fn)
			}
		} else {
			fn(p)
		}
	}
}

func walkDirs(dir string, depth int, fn func(string)) {
	if depth < 0 {
		return
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name())
		fn(p)
		walkDirs(p, depth-1, fn)
	}
}
