package koto

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/store"
)

// Profile is a registered native storage namespace, independent of login state.
type Profile struct {
	core.SourceProfile
	Writable            bool                 `json:"writable"`
	Enabled             bool                 `json:"enabled"`
	AccountSelectable   bool                 `json:"accountSelectable"`
	Accounts            []core.IdentityClaim `json:"accounts"`
	TargetAccounts      []core.IdentityClaim `json:"targetAccounts"`
	TargetAccountsError string               `json:"targetAccountsError,omitempty"`
}

func (a *App) Profiles(ctx context.Context) []Profile {
	claims := map[string][]core.IdentityClaim{}
	seen := map[string]bool{}
	for _, row := range a.Sessions.Rows() {
		for _, claim := range core.ReadIdentity(row.Meta).Claims {
			key := row.Connector + "\x00" + claim.Kind + "\x00" + claim.ID + "\x00" + claim.Scope
			if !seen[key] {
				claims[row.Connector] = append(claims[row.Connector], claim)
				seen[key] = true
			}
		}
	}
	out := []Profile{}
	for _, state := range a.Registry.List() {
		c := state.Connector
		label, kind := c.ProfileLabel, c.ProfileKind
		if label == "" {
			label = c.Root
		}
		if kind == "" {
			kind = "directory"
		}
		accounts := claims[c.ID]
		if accounts == nil {
			accounts = []core.IdentityClaim{}
		}
		sort.Slice(accounts, func(i, j int) bool {
			return accounts[i].Kind+accounts[i].ID+accounts[i].Scope < accounts[j].Kind+accounts[j].ID+accounts[j].Scope
		})
		p := Profile{SourceProfile: core.SourceProfile{ID: c.ID, Harness: c.Harness, Root: c.Root, Label: label, Kind: kind, Variant: engine.ProfileVariant(c), Default: c.Default}, Writable: c.Sink != nil, Enabled: state.Status != "disabled", Accounts: accounts, TargetAccounts: []core.IdentityClaim{}}
		if target, ok := c.Sink.(engine.TargetAccounts); ok {
			p.AccountSelectable = true
			if values, err := target.TargetAccounts(ctx); err != nil {
				p.TargetAccountsError = err.Error()
			} else if values != nil {
				p.TargetAccounts = values
			}
		}
		out = append(out, p)
	}
	return out
}

func (a *API) Profiles(ctx context.Context) []Profile { return a.App.Profiles(ctx) }

// ResolveSession resolves native-ID shorthand only when unambiguous. Canonical
// selectors include a connector, while the native session ID stays unchanged.
func (a *App) ResolveSession(selector string) (store.SessionRow, error) {
	var match store.SessionRow
	var choices []string
	for _, r := range a.Sessions.Rows() {
		if core.MatchesSessionSelector(selector, r.Connector, r.Harness, r.Session) {
			match = r
			choices = append(choices, core.SessionSelector(r.Connector, r.Harness, r.Session))
		}
	}
	if len(choices) == 0 {
		return match, store.ErrNotFound{Selector: selector}
	}
	if len(choices) > 1 {
		sort.Strings(choices)
		return store.SessionRow{}, &engine.CodedError{Code: "AMBIGUOUS", Msg: fmt.Sprintf("%s matches multiple profiles: %s", selector, strings.Join(choices, ", "))}
	}
	return match, nil
}

func normalizedRoot(path string) string { return engine.NormalizeRoot(path) }

func (a *App) ProfileIDsForRoot(harness, root string, variants ...string) []string {
	var out []string
	for _, c := range a.Registry.Sources() {
		if c.Harness == harness && normalizedRoot(c.Root) == normalizedRoot(root) && (len(variants) == 0 || engine.ProfileVariant(c) == variants[0]) {
			out = append(out, c.ID)
		}
	}
	return out
}

// An environment-selected profile does not hide the product's ordinary local
// profile. This is a bounded list of documented locations, never a recursive
// search through the user's home or credential/configuration files. Explicit
// command-line roots keep isolated test runs isolated.
func (a *App) registerDefaultProfiles() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	candidates := []struct{ harness, explicit, root string }{
		{"claude-code", a.Cfg.ClaudeRoot, filepath.Join(home, ".claude")},
		{"codex", a.Cfg.CodexHome, filepath.Join(home, ".codex")},
		{"pi", a.Cfg.PiRoot, filepath.Join(home, ".pi", "agent")},
		{"dsh", a.Cfg.DshHome, filepath.Join(home, ".dsh")},
		{"antigravity", a.Cfg.AntigravityRoot, filepath.Join(home, ".gemini", "antigravity-cli")},
		{"opencode", a.Cfg.OpenCodeRoot, filepath.Join(home, ".local", "share", "opencode")},
		{"zcode", a.Cfg.ZCodeRoot, filepath.Join(home, ".zcode")},
		{"cursor", a.Cfg.CursorRoot, filepath.Join(home, ".cursor")},
	}
	for _, candidate := range candidates {
		if candidate.explicit != "" || !on(a.Cfg.Enable, candidate.harness) {
			continue
		}
		st, err := os.Stat(candidate.root)
		if err != nil || !st.IsDir() {
			continue
		}
		id := engine.ProfileID(candidate.harness, candidate.root, "")
		if a.Registry.Get(id) != nil {
			continue
		}
		c, err := a.makeExtra(extraRoot{ID: id, Harness: candidate.harness, Root: candidate.root})
		if err == nil {
			_ = a.Registry.Register(c)
		}
	}
}

// EnsureRoot registers an explicitly chosen directory once. It does not create
// credential files or infer an account from the directory's display name.
func (a *App) EnsureRoot(harness, root string) (ConnectorInfo, error) {
	if _, err := os.Stat(root); err != nil {
		return ConnectorInfo{}, err
	}
	if c := a.Registry.Get(engine.ProfileID(harness, root, "")); c != nil {
		return a.connectorInfo(c.ID)
	}
	return a.AddRoot(harness, root)
}
