package main

import (
	"context"
	"os"
	"strings"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/koto"
)

// These are current process markers set by the official hosts. A transcript's
// historical entrypoint cannot identify the host that is resuming it now.
func claudeHostVariant(entrypoint string) string {
	switch entrypoint {
	case "claude-desktop", "claude-desktop-3p":
		return "desktop"
	case "cli", "sdk-cli":
		return "cli"
	default:
		return ""
	}
}

// Desktop and Code may bind the same physical transcript. The native host
// marker chooses its surface, never an account, project, or most recent copy.
func claudeHostConnector(ctx context.Context, app *koto.App, selector string, getenv func(string) string) (string, error) {
	_, id, _, _ := core.ParseSessionSelector(selector)
	entrypoint := getenv("CLAUDE_CODE_ENTRYPOINT")
	variant := claudeHostVariant(entrypoint)
	root := strings.TrimSpace(getenv("CLAUDE_CONFIG_DIR"))
	rootFound := root == ""
	var matches []nativeSession
	var candidates []nativeSession
	for _, c := range app.Registry.Sources() {
		if c.Harness != "claude-code" || root != "" && engine.NormalizeRoot(c.Root) != engine.NormalizeRoot(root) {
			continue
		}
		rootFound = true
		refs, err := c.Source.Discover(ctx)
		if err != nil {
			return "", handoffErr("SOURCE_UNREADABLE", "Cannot discover the calling Claude session.", "Check this Agent's storage path and permissions.")
		}
		for _, ref := range refs {
			if ref.Session != id {
				continue
			}
			native := nativeSession{c, ref}
			matches = append(matches, native)
			if engine.ProfileVariant(c) == variant && claudeHostProfile(entrypoint, ref.Meta) {
				candidates = append(candidates, native)
			}
		}
	}
	if !rootFound {
		return "", handoffErr("CURRENT_PROFILE_UNVERIFIED", "The calling Claude storage root is not an enabled profile.", "Use the exact Agent storage root; no other root was substituted.")
	}
	if len(candidates) == 0 {
		return "", handoffErr("SOURCE_NOT_FOUND", "The exact session is not present in the calling Claude host's enabled profiles.", "Check the Desktop account directory or Claude Code storage root. No other host was substituted.")
	}
	ambiguous := func() (string, error) {
		choices := make([]string, 0, len(matches))
		for _, match := range matches {
			choices = append(choices, core.SessionSelector(match.connector.ID, match.ref.Harness, match.ref.Session))
		}
		return "", handoffErr("SOURCE_AMBIGUOUS", "The session identity matches multiple native profiles or files.", "Choose an exact profile: "+strings.Join(choices, ", "))
	}
	if len(candidates) != 1 {
		return ambiguous()
	}
	// A host marker alone cannot choose among independent files with the same
	// UUID. Only aliases/bindings of this exact file may be discarded here.
	if len(matches) > 1 {
		first, err := os.Stat(candidates[0].ref.Path)
		if err != nil {
			return "", handoffErr("SOURCE_UNREADABLE", "Cannot verify the current Claude transcript file.", "Check the source file and permissions.")
		}
		for _, match := range matches {
			other, err := os.Stat(match.ref.Path)
			if err != nil {
				return "", handoffErr("SOURCE_UNREADABLE", "Cannot verify the current Claude transcript file.", "Check the source file and permissions.")
			}
			if !os.SameFile(first, other) {
				return ambiguous()
			}
		}
	}
	return candidates[0].connector.ID, nil
}

func claudeHostProfile(entrypoint string, meta core.JsonObject) bool {
	// The official third-party Desktop has its own app-data namespace. Its
	// marker must not claim an account binding from the subscription App.
	thirdParty := strings.EqualFold(core.S(meta["desktopProfile"]), "Claude-3p")
	switch entrypoint {
	case "claude-desktop-3p":
		return thirdParty
	case "claude-desktop":
		return !thirdParty
	default:
		return true
	}
}
