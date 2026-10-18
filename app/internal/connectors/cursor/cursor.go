// Package cursor handles Cursor local conversation stores. IDE state.vscdb and
// CLI store.db have separate indexes and resume entry points.
package cursor

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
	_ "modernc.org/sqlite"
)

const Version = "cursor-connector@0.4.2"
const modelAPI = "cursor-model-message"
const format = "cursor-ide-3.25-protobuf"
const maxBlobBytes = 64 << 20

var sessionID = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

// Root is the ordinary Cursor IDE user-data directory (the parent of
// User/globalStorage). IDE sessions cannot be resumed through the CLI store.db.
type Options struct{ Root, ID string }
type connector struct{ id, root, db, assetBase, variant string }

func New(o Options) *engine.Connector {
	if o.ID == "" {
		o.ID = "cursor-ide"
	}
	if o.Root == "" {
		home, _ := os.UserHomeDir()
		switch runtime.GOOS {
		case "darwin":
			o.Root = filepath.Join(home, "Library", "Application Support", "Cursor")
		case "windows":
			o.Root = filepath.Join(os.Getenv("APPDATA"), "Cursor")
		default:
			base := os.Getenv("XDG_CONFIG_HOME")
			if base == "" {
				base = filepath.Join(home, ".config")
			}
			o.Root = filepath.Join(base, "Cursor")
		}
	}
	if !filepath.IsAbs(o.Root) {
		if absolute, err := filepath.Abs(o.Root); err == nil {
			o.Root = absolute
		}
	}
	c := &connector{id: o.ID, root: o.Root, variant: "ide", db: filepath.Join(o.Root, "User", "globalStorage", "state.vscdb")}
	return &engine.Connector{ID: c.id, Root: c.root, Harness: "cursor", Variant: c.variant, Version: Version, Source: c, Sink: c, Vocabulary: Vocabulary{}, Capabilities: core.Capabilities{Read: core.ReadCaps{Images: true, Reasoning: "plain"}, Write: &core.WriteCaps{Mode: "native-file", Images: true, Reasoning: "plain", SameAPI: modelAPI, Tools: "native", ToolCallID: &core.IDRule{Pattern: `^call_[A-Za-z0-9_]+$`, MaxLength: 64}}, WriteProfile: "cursor-ide-write@1", FormatVersions: []string{format}}}
}

// DefaultAppProfiles discovers the ordinary Cursor App's local store.
// An absent database is not created merely by scanning the machine.
func DefaultAppProfiles() []*engine.Connector {
	c := New(Options{})
	if st, e := os.Stat(c.Source.(*connector).db); e == nil && st.Mode().IsRegular() {
		return []*engine.Connector{c}
	}
	return nil
}
func (c *connector) DatabasePath() string { return c.db }
func (c *connector) WatchRoots() []string { return []string{filepath.Dir(c.db)} }
func (c *connector) open(ctx context.Context) (*sql.DB, error) {
	if _, err := os.Stat(c.db); err != nil {
		return nil, err
	}
	p, err := filepath.Abs(c.db)
	if err != nil {
		return nil, err
	}
	u := shared.FileURL(p)
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(3000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
func (c *connector) Discover(ctx context.Context) ([]core.NativeRef, error) {
	db, err := c.open(ctx)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT composerId,coalesce(lastUpdatedAt,0),value FROM composerHeaders WHERE coalesce(isArchived,0)=0 AND coalesce(isSubagent,0)=0 ORDER BY lastUpdatedAt DESC,composerId`)
	if err != nil {
		return nil, fmt.Errorf("Cursor IDE session index: %w", err)
	}
	defer rows.Close()
	out := []core.NativeRef{}
	for rows.Next() {
		var id string
		var updated int64
		var data []byte
		if err := rows.Scan(&id, &updated, &data); err != nil {
			return nil, err
		}
		var m core.JsonObject
		if err = json.Unmarshal(data, &m); err != nil {
			return nil, err
		}
		if m["isDraft"] == true {
			continue
		}
		if !sessionID.MatchString(id) {
			return nil, fmt.Errorf("invalid Cursor IDE session ID")
		}
		workspace := core.Obj(m["workspaceIdentifier"])
		uri := core.Obj(workspace["uri"])
		cwd := core.S(uri["fsPath"])
		if cwd == "" {
			u, _ := url.Parse(core.S(uri["external"]))
			if u != nil && u.Scheme == "file" {
				cwd = filepath.FromSlash(u.Path)
			}
		}
		out = append(out, core.NativeRef{Harness: "cursor", Connector: c.id, Session: id, Path: c.db, MtimeMs: float64(updated), Meta: core.JsonObject{"title": core.S(m["name"]), "cwd": cwd, "createdAt": m["createdAt"], "updatedAt": float64(updated), "frontend": c.variant, "identity": core.IdentityMeta(core.SessionIdentity{})}})
	}
	return out, rows.Err()
}

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readKV(ctx context.Context, q querier, key string) ([]byte, error) {
	var size int
	if err := q.QueryRowContext(ctx, "SELECT length(value) FROM cursorDiskKV WHERE key=?", key).Scan(&size); err != nil {
		return nil, fmt.Errorf("Cursor record %s unavailable: %w", key, err)
	}
	if size > maxBlobBytes {
		return nil, fmt.Errorf("Cursor record exceeds 64 MiB limit")
	}
	var b []byte
	if err := q.QueryRowContext(ctx, "SELECT value FROM cursorDiskKV WHERE key=?", key).Scan(&b); err != nil {
		return nil, err
	}
	return b, nil
}
func readBlob(ctx context.Context, q querier, id []byte) ([]byte, error) {
	if len(id) != 32 {
		return nil, fmt.Errorf("invalid Cursor content reference")
	}
	sha := hex.EncodeToString(id)
	var b []byte
	var err error
	if store, ok := q.(interface {
		nativeBlob(context.Context, string) ([]byte, error)
	}); ok {
		b, err = store.nativeBlob(ctx, sha)
	} else {
		b, err = readKV(ctx, q, "agentKv:blob:"+sha)
	}
	if err != nil {
		return nil, err
	}
	if core.SHA256Hex(b) != sha {
		legacy, e := base64.StdEncoding.DecodeString(string(b))
		if e != nil || core.SHA256Hex(legacy) != sha {
			return nil, fmt.Errorf("Cursor blob integrity mismatch: %s", sha)
		}
		b = legacy
	}
	return b, nil
}
func readState(encoded string) (wireMessage, error) {
	if !strings.HasPrefix(encoded, "~") {
		return nil, fmt.Errorf("unsupported Cursor conversation-state encoding")
	}
	b, err := base64.StdEncoding.DecodeString(encoded[1:])
	if err != nil {
		return nil, err
	}
	return parseWire(b)
}
