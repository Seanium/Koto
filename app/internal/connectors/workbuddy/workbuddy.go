// Package workbuddy reads and creates the local WorkBuddy App history format.
// The domestic and international apps have separate roots, identities and URL
// schemes. Their inspected 5.8.x local history schemas match; cloud tasks are not
// treated as complete local transcripts.
package workbuddy

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"

	"koto/internal/connectors/claudecode"
	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
	_ "modernc.org/sqlite"
)

const Version = "workbuddy-connector@0.2.0"

type Options struct{ Root, ID, Edition, UserID string }
type connector struct {
	root, id, harness, db, userID string
	files                         *shared.PathIndex
}

var Capabilities = core.Capabilities{Read: core.ReadCaps{Images: true, Reasoning: "plain", Subagents: false, Compaction: true}, Write: &core.WriteCaps{Mode: "native-file", Images: true, Reasoning: "plain", Tools: "canonical", Tree: true}, WriteProfile: "workbuddy-app@1", FormatVersions: []string{"5.8-local"}}
var uuid = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

func New(o Options) *engine.Connector {
	h := "workbuddy"
	if o.Edition == "international" {
		h = "workbuddy-ai"
	}
	if o.ID == "" {
		o.ID = h
	}
	if o.Root == "" {
		home, _ := os.UserHomeDir()
		o.Root = filepath.Join(home, "."+h)
	}
	c := &connector{root: o.Root, id: o.ID, harness: h, db: filepath.Join(o.Root, "workbuddy.db"), userID: o.UserID, files: shared.NewPathIndex()}
	return &engine.Connector{ID: c.id, Harness: h, Root: c.root, Version: Version, Capabilities: Capabilities, Vocabulary: claudecode.Vocabulary{}, Source: c, Sink: c, Trash: c}
}
func (c *connector) open(write bool) (*sql.DB, error) {
	if _, err := os.Stat(c.db); err != nil {
		return nil, err
	}
	mode := "ro"
	if write {
		mode = "rw"
	}
	u := shared.FileURL(c.db)
	q := u.Query()
	q.Set("mode", mode)
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err == nil {
		db.SetMaxOpenConns(1)
	}
	return db, err
}
func (c *connector) owner(ctx context.Context, db *sql.DB, requested ...string) (string, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT user_id FROM sessions WHERE user_id != ''`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	owners := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		owners[id] = true
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	selected := c.userID
	if len(requested) != 0 && requested[0] != "" {
		selected = requested[0]
	}
	if selected != "" {
		if !owners[selected] {
			return "", fmt.Errorf("WorkBuddy target account is not present in this app database")
		}
		return selected, nil
	}
	if len(owners) != 1 {
		return "", fmt.Errorf("WorkBuddy target account is ambiguous; open a local task in the target app and select its account")
	}
	for id := range owners {
		return id, nil
	}
	panic("unreachable")
}

func (c *connector) TargetAccounts(ctx context.Context) ([]core.IdentityClaim, error) {
	db, err := c.open(false)
	if os.IsNotExist(err) {
		return []core.IdentityClaim{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT user_id FROM sessions WHERE user_id != '' ORDER BY user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := []core.IdentityClaim{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		accounts = append(accounts, core.IdentityClaim{Kind: "account", ID: id, Scope: "storage", Evidence: c.harness + ":sessions.user_id"})
	}
	return accounts, rows.Err()
}
func (c *connector) Discover(ctx context.Context) ([]core.NativeRef, error) {
	db, err := c.open(false)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT id,cwd,coalesce(custom_title,title,''),coalesce(model,''),status,user_id FROM sessions WHERE deleted_at IS NULL AND transport='local' ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.NativeRef{}
	for rows.Next() {
		var id, cwd, title, model, status, owner string
		if err := rows.Scan(&id, &cwd, &title, &model, &status, &owner); err != nil {
			return nil, err
		}
		if !uuid.MatchString(id) {
			return nil, fmt.Errorf("invalid WorkBuddy session ID")
		}
		p := c.transcript(cwd, id)
		st, err := os.Stat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("WorkBuddy transcript is not a file")
		}
		c.files.Set(id, p)
		identity := core.SessionIdentity{}
		if owner != "" {
			identity.Claims = []core.IdentityClaim{{Kind: "account", ID: owner, Scope: "storage", Evidence: c.harness + ":sessions.user_id"}}
		}
		out = append(out, core.NativeRef{Harness: c.harness, Connector: c.id, Session: id, Path: p, Size: st.Size(), MtimeMs: engine.MtimeMs(st), Meta: core.JsonObject{"cwd": cwd, "title": title, "modelId": model, "status": status, "frontend": "desktop", "edition": c.harness, "userId": owner, "identity": core.IdentityMeta(identity)}})
	}
	return out, rows.Err()
}
func (c *connector) transcript(cwd, id string) string {
	return filepath.Join(c.root, "projects", ProjectKey(cwd), id+".jsonl")
}

// ProjectKey matches WorkBuddy's UTF-8 path compression, including long and Windows paths.
func ProjectKey(cwd string) string {
	s := strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(cwd)
	s = strings.Trim(s, "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	if len(s) <= 255 {
		return s
	}
	var hash uint32 = 5381
	for _, b := range []byte(s) {
		hash = hash*33 ^ uint32(b)
	}
	n := 180
	for !utf8.ValidString(s[:n]) {
		n--
	}
	return s[:n] + "-" + strconv.FormatUint(uint64(hash), 36)
}
func (c *connector) WatchRoots() []string {
	return []string{c.db, filepath.Join(c.root, "projects"), filepath.Join(c.root, "blobs")}
}
func (c *connector) Tail(ctx context.Context, ref core.NativeRef, from *core.SourceOffset, yield func(core.SourceBatch) error) error {
	return engine.TailJSONL(ctx, ref, from, decoder{root: c.root}, func(b core.SourceBatch) error {
		if b.Meta == nil {
			b.Meta = core.JsonObject{}
		}
		for k, v := range ref.Meta {
			if _, ok := b.Meta[k]; !ok {
				b.Meta[k] = v
			}
		}
		return yield(b)
	}, 0, false)
}
func (c *connector) ReadRecord(session string, index int, offset *int64) ([]byte, error) {
	p, ok := c.files.Get(session)
	if !ok {
		_, err := c.Discover(context.Background())
		if err != nil {
			return nil, err
		}
		p, ok = c.files.Get(session)
	}
	if !ok || offset == nil {
		return nil, nil
	}
	return engine.ReadRecordAt(p, *offset)
}
func (c *connector) NewSessionID(key string) string { return core.UUID5(c.harness + "\x00" + key) }
func (c *connector) ResumeCommand() string          { return "" }
func (c *connector) hasDefaultRoot(defaultRoot string) bool {
	root, err := filepath.Abs(c.root)
	if err != nil {
		return false
	}
	def, err := filepath.Abs(defaultRoot)
	if err != nil {
		return false
	}
	if filepath.Clean(root) == filepath.Clean(def) {
		return true
	}
	a, err := os.Stat(root)
	if err != nil {
		return false
	}
	b, err := os.Stat(def)
	return err == nil && os.SameFile(a, b)
}
func (c *connector) NativeResume(session string) ([]string, map[string]string) {
	if !uuid.MatchString(session) {
		return nil, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || !c.hasDefaultRoot(filepath.Join(home, "."+c.harness)) {
		// The official URL scheme cannot address a custom profile. It can route
		// to another running instance, so it is not a valid resume instruction.
		return nil, nil
	}
	u := c.harness + "://chat/" + url.PathEscape(session)
	switch runtime.GOOS {
	case "darwin":
		return []string{"open", u}, nil
	case "windows":
		return []string{"rundll32.exe", "url.dll,FileProtocolHandler", u}, nil
	default:
		return []string{"xdg-open", u}, nil
	}
}
func (c *connector) Files(s engine.SessionRef) []string { return nil }
