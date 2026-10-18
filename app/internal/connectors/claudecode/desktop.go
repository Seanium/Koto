package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"koto/internal/core"
	"koto/internal/engine"
)

var localJSON = regexp.MustCompile(`^local_.*\.json$`)

// DesktopProfile names an actual on-disk account/organization namespace. Root
// may be shared with the CLI; Scope must never be merged with another account.
type DesktopProfile struct {
	ID, Root, Scope, Label, Profile string
	Identity                        core.SessionIdentity
}

// DefaultDesktopDirs covers the current Windows third-party location and the
// older roaming location. Merely finding a directory does not identify a login.
func DefaultDesktopDirs(goos, home, appData, localAppData, xdgConfig string) []string {
	switch goos {
	case "windows":
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		if localAppData == "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		dirs := []string{filepath.Join(appData, "Claude"), filepath.Join(localAppData, "Claude-3p"), filepath.Join(appData, "Claude-3p"), filepath.Join(localAppData, "Claude-Data")}
		// Official Windows Desktop 2.42304 MSIX and relocated user-data roots.
		for _, family := range []string{"Claude_pzs8sxrjxfjjc", "AnthropicPBC.Claude_fnn82j28hfe8t"} {
			dirs = append(dirs, filepath.Join(localAppData, "Packages", family, "LocalCache", "Roaming", "Claude"), filepath.Join(localAppData, "Claude-Data", strings.ToLower(family)))
		}
		return dirs
	case "darwin":
		base := filepath.Join(home, "Library", "Application Support")
		return []string{filepath.Join(base, "Claude"), filepath.Join(base, "Claude-3p")}
	default:
		if xdgConfig == "" {
			xdgConfig = filepath.Join(home, ".config")
		}
		return []string{filepath.Join(xdgConfig, "Claude"), filepath.Join(xdgConfig, "Claude-3p")}
	}
}

func scopeIdentity(profile, account, org, evidence string) core.SessionIdentity {
	identity := core.SessionIdentity{}
	if strings.EqualFold(profile, "Claude-3p") {
		// Desktop can derive these UUIDs from a gateway/project configuration. They
		// are API storage namespaces, not proof of a subscription or billing account.
		identity.Claims = []core.IdentityClaim{{Kind: "api-configuration", ID: account + "/" + org, Scope: "storage", Evidence: evidence}}
	} else {
		identity.Claims = []core.IdentityClaim{{Kind: "account", ID: account, Scope: "storage", Evidence: evidence + "/<account>"}, {Kind: "organization", ID: org, Scope: "storage", Evidence: evidence + "/<account>/<organization>"}}
	}
	return identity
}

// DiscoverDesktopProfiles reads directory names only; it does not inspect
// authstate, current settings, cookies, or cloud history.
func DiscoverDesktopProfiles(dirs []string, transcriptRoot string) []DesktopProfile {
	out := []DesktopProfile{}
	seen := map[string]bool{}
	for _, d := range dirs {
		d = filepath.Clean(d)
		base := filepath.Join(d, "claude-code-sessions")
		accounts, _ := os.ReadDir(base)
		for _, account := range accounts {
			if !account.IsDir() {
				continue
			}
			orgs, _ := os.ReadDir(filepath.Join(base, account.Name()))
			for _, org := range orgs {
				if !org.IsDir() {
					continue
				}
				scope := filepath.Join(base, account.Name(), org.Name())
				if seen[scope] {
					continue
				}
				seen[scope] = true
				profile := filepath.Base(d)
				out = append(out, DesktopProfile{ID: "claude-desktop-" + core.SHA256Hex(scope)[:16], Root: transcriptRoot, Scope: scope, Label: profile + " · " + account.Name() + " / " + org.Name(), Profile: profile, Identity: scopeIdentity(profile, account.Name(), org.Name(), profile+"/claude-code-sessions")})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Scope < out[j].Scope })
	return out
}

func readDesktopScope(scope, profile string, identity core.SessionIdentity) map[string]core.JsonObject {
	out, _ := readDesktopScopeContext(context.Background(), scope, profile, identity)
	return out
}

func readDesktopScopeContext(ctx context.Context, scope, profile string, identity core.SessionIdentity) (map[string]core.JsonObject, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	out := map[string]core.JsonObject{}
	entries, _ := os.ReadDir(scope)
	for _, entry := range entries {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !localJSON.MatchString(entry.Name()) {
			continue
		}
		path := filepath.Join(scope, entry.Name())
		st, e := entry.Info()
		if e != nil || st.Size() > 4<<20 {
			continue
		}
		b, e := os.ReadFile(path)
		if e != nil {
			continue
		}
		var j core.JsonObject
		if json.Unmarshal(b, &j) != nil {
			continue
		}
		id := core.S(j["cliSessionId"])
		if id == "" {
			continue
		}
		meta := core.JsonObject{"frontend": "claude-desktop", "desktopSessionId": strOrNil(j["sessionId"]), "desktopTitle": strOrNil(j["title"]), "desktopArchived": j["isArchived"] == true, "desktopProfile": profile, "desktopMetadataPath": path, "desktopCwd": strOrNil(j["cwd"]), "desktopOriginCwd": strOrNil(j["originCwd"]), "identity": core.IdentityMeta(identity)}
		// Even within one namespace multiple native bindings may reference a CLI
		// transcript. Keep every binding; a filename order must never choose an owner.
		if prev := out[id]; prev != nil {
			bindings := core.Arr(prev["desktopBindings"])
			if len(bindings) == 0 {
				bindings = []any{prev}
			}
			bindings = append(bindings, meta)
			out[id] = core.JsonObject{"frontend": "claude-desktop", "desktopProfile": profile, "desktopBindings": bindings, "identity": core.IdentityMeta(identity)}
		} else {
			out[id] = meta
		}
	}
	return out, ctx.Err()
}

// Root is an independent Claude Code transcript root (for example Cowork).
type Root struct {
	ID, Root, Label, Profile string
	Identity                 core.SessionIdentity
}

// NewCowork reads the transcript inside an existing native Cowork task. A new
// JSONL there is not a new Cowork task: its environment and native index are
// owned by Desktop, so this location cannot be a delivery target.
func NewCowork(root Root) *engine.Connector {
	out := newCode(Options{Root: root.Root, ID: root.ID, Identity: root.Identity})
	source := out.Source.(*cc)
	source.cowork, source.desktopProfile = true, root.Profile
	out.ProfileLabel, out.ProfileKind, out.Variant = root.Label, "cowork", "desktop"
	out.Version += "/cowork@1"
	out.Sink, out.Capabilities.Write = nil, nil
	out.Capabilities.WriteProfile = ""
	return out
}

// Recognize only the native task layout, including an explicitly configured
// root or a symlink to it. A generic directory named .claude is still Code.
func coworkRootAt(path string) (Root, bool) {
	if path == "" {
		return Root{}, false
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return Root{}, false
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	path = filepath.Clean(path)
	task := filepath.Dir(path)
	org, account := filepath.Dir(task), filepath.Dir(filepath.Dir(task))
	base := filepath.Dir(account)
	if filepath.Base(path) != ".claude" || !strings.HasPrefix(filepath.Base(task), "local_") || filepath.Base(base) != "local-agent-mode-sessions" {
		return Root{}, false
	}
	profile := filepath.Base(filepath.Dir(base))
	return Root{ID: "claude-cowork-" + core.SHA256Hex(task)[:16], Root: path,
		Label: profile + " Cowork · " + filepath.Base(account) + " / " + filepath.Base(org), Profile: profile,
		Identity: scopeIdentity(profile, filepath.Base(account), filepath.Base(org), profile+"/local-agent-mode-sessions")}, true
}

func DiscoverCoworkRoots(dirs []string) []Root {
	out := []Root{}
	seen := map[string]bool{}
	for _, d := range dirs {
		base := filepath.Join(d, "local-agent-mode-sessions")
		walkDirs(base, 3, func(p string) {
			rel, e := filepath.Rel(base, p)
			if e != nil {
				return
			}
			parts := strings.Split(rel, string(filepath.Separator))
			if len(parts) != 3 || !strings.HasPrefix(parts[2], "local_") {
				return
			}
			root := filepath.Join(p, ".claude")
			if !exists(root) || seen[root] {
				return
			}
			seen[root] = true
			profile := filepath.Base(d)
			out = append(out, Root{ID: "claude-cowork-" + core.SHA256Hex(p)[:16], Root: root, Label: profile + " Cowork · " + parts[0] + " / " + parts[1], Profile: profile, Identity: scopeIdentity(profile, parts[0], parts[1], profile+"/local-agent-mode-sessions")})
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
