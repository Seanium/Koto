package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"koto/internal/koto"
)

var proofToken = regexp.MustCompile(`^[a-f0-9]{32}$`)

func proofInvocation(token string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[/\\\s"'])koto(?:-cli(?:-(?:darwin|linux|windows)-(?:arm64|amd64))?)?(?:\.exe)?["']?\s+current\b[^\r\n]*\s--proof(?:=|\s+)["']?` + regexp.QuoteMeta(token) + `["']?(?:\s|$)`)
}

// OpenCode does not supply a session ID to its shell tools. A fresh challenge
// becomes part of the calling session's running tool record before this process
// starts. Match that exact invocation, never a recent session or a similar text.
func currentProofHint() string {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "Supply an exact agent:session-id; never choose by recency."
	}
	return fmt.Sprintf("If you are OpenCode, run koto current --proof %s --json from your shell tool, then copy the exact source.selector it returns. Keep any --opencode-root option. Otherwise supply an exact agent:session-id or KOTO_SESSION. Never choose by recency or working directory.", hex.EncodeToString(nonce[:]))
}

func resolveCurrentProof(ctx context.Context, app *koto.App, token string) (string, error) {
	if !proofToken.MatchString(token) {
		return "", handoffErr("INVALID_PROOF", "The current-session proof must be the 32-character token returned by koto current.", "Run koto current --json for a fresh proof token.")
	}
	// Limit reads to the active shell tool records. Messages and attachments from
	// other sessions are neither loaded nor included in the response.
	invocation := proofInvocation(token)
	seenDB := map[string]bool{}
	matches := map[string]string{}
	for _, c := range app.Registry.Sources() {
		if c.Harness != "opencode" {
			continue
		}
		dbSource, ok := c.Source.(interface{ DatabasePath() string })
		if !ok || seenDB[dbSource.DatabasePath()] {
			continue
		}
		path := dbSource.DatabasePath()
		seenDB[path] = true
		ids, err := findRunningProof(ctx, path, invocation)
		if err != nil {
			return "", handoffErr("CURRENT_SESSION_UNAVAILABLE", "Cannot verify the running OpenCode tool: "+err.Error(), "Check the OpenCode data directory and retry with a fresh proof.")
		}
		for _, id := range ids {
			matches[path+"\x00"+id] = id
		}
	}
	if len(matches) != 1 {
		return "", handoffErr("CURRENT_SESSION_UNVERIFIED", "The proof did not identify exactly one running OpenCode session.", "Run the proof command inside the requesting OpenCode session's shell tool; do not run it in another terminal or choose a session by recency.")
	}
	for _, id := range matches {
		return "opencode:" + id, nil
	}
	panic("unreachable")
}

func findRunningProof(ctx context.Context, file string, invocation *regexp.Regexp) ([]string, error) {
	path, err := filepath.Abs(file)
	if err != nil {
		return nil, err
	}
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(1000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, `SELECT session_id, json_extract(data, '$.state.input.command') FROM part
		WHERE time_updated >= ? AND json_extract(data, '$.type') = 'tool'
		AND json_extract(data, '$.tool') IN ('bash', 'shell')
		AND json_extract(data, '$.state.status') = 'running'`, time.Now().Add(-5*time.Minute).UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var matches []string
	for rows.Next() {
		var session string
		var command sql.NullString
		if err := rows.Scan(&session, &command); err != nil {
			return nil, err
		}
		if command.Valid && invocation.MatchString(command.String) {
			matches = append(matches, session)
		}
	}
	return matches, rows.Err()
}
