package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"koto/internal/engine"
	"koto/internal/koto"
)

// WorkBuddy's native Bash environment shares its ID names with CodeBuddy.
// The registered App root is required to distinguish products and editions.
// No transcript search, project heuristic or account credentials are involved.
func workBuddySourceSelector(app *koto.App, getenv func(string) string) (string, string, string, error) {
	if _, _, err := sourceSelector([]string{"current"}, false, getenv); err == nil {
		return "", "", "", handoffErr("CURRENT_SESSION_AMBIGUOUS", "WorkBuddy and another Agent session identity are both present.", "Use an exact agent:session-id supplied by the calling Agent or user.")
	} else {
		var known *handoffError
		if !errors.As(err, &known) || known.Code != "CURRENT_SESSION_UNKNOWN" {
			return "", "", "", err
		}
	}
	id := strings.TrimSpace(getenv("CODEBUDDY_SESSION_ID"))
	root := strings.TrimSpace(getenv("WORKBUDDY_CONFIG_DIR"))
	if id == "" || !filepath.IsAbs(root) {
		return "", "", "", handoffErr("CURRENT_SESSION_UNKNOWN", "WorkBuddy did not supply both its session ID and an absolute App storage root.", "Run koto from the WorkBuddy session's shell tool, or supply an exact agent:session-id.")
	}
	if claudeID := strings.TrimSpace(getenv("CLAUDE_SESSION_ID")); claudeID != "" && claudeID != id {
		return "", "", "", handoffErr("CURRENT_SESSION_AMBIGUOUS", "WorkBuddy's native session identities disagree.", "Use an exact session ID from the calling App; do not choose an inherited ID.")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", "", "", handoffErr("SOURCE_UNREADABLE", "The calling WorkBuddy App storage root cannot be read.", "Check WORKBUDDY_CONFIG_DIR and the App's directory permissions.")
	}
	var matches []*engine.Connector
	for _, c := range app.Registry.Sources() {
		if c.Harness != "workbuddy" && c.Harness != "workbuddy-ai" {
			continue
		}
		other, err := os.Stat(c.Root)
		if err == nil && os.SameFile(info, other) {
			matches = append(matches, c)
		}
	}
	if len(matches) == 0 {
		return "", "", "", handoffErr("SOURCE_NOT_FOUND", "The calling WorkBuddy root is not enabled in koto.", "Use --workbuddy-root for WorkBuddy or --workbuddy-ai-root for WorkBuddy AI, with the exact WORKBUDDY_CONFIG_DIR. Do not substitute another account or edition.")
	}
	if len(matches) != 1 {
		return "", "", "", handoffErr("CURRENT_SESSION_AMBIGUOUS", "The WorkBuddy storage root is registered more than once.", "Enable a single connector for the calling App root.")
	}
	c := matches[0]
	if folder := strings.TrimSpace(getenv("WORKBUDDY_DATA_FOLDER_NAME")); folder != "" && folder != "."+c.Harness {
		return "", "", "", handoffErr("CURRENT_SESSION_AMBIGUOUS", "WorkBuddy's edition does not match the registered App root.", "Check the domestic and international App root settings; their accounts and sessions are separate.")
	}
	return c.Harness + ":" + id, "WORKBUDDY_CONFIG_DIR+CODEBUDDY_SESSION_ID", c.ID, nil
}
