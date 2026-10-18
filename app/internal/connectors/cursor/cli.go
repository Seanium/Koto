package cursor

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
)

const cliFormat = "cursor-cli-sqlite-1"

// CLI stores are independent of the IDE conversation database.
type CLIOptions struct {
	Root, ID string
	// Command selects the official runtime without changing the storage identity.
	// It can contain a full executable path and explicit runtime flags.
	Command []string
}
type cliConnector struct {
	id, root string
	command  []string
}

func NewCLI(o CLIOptions) *engine.Connector {
	if o.Root == "" {
		o.Root = os.Getenv("CURSOR_CONFIG_DIR")
		if o.Root == "" {
			h, _ := os.UserHomeDir()
			o.Root = filepath.Join(h, ".cursor")
		}
	}
	if o.ID == "" {
		o.ID = "cursor-cli"
	}
	if !filepath.IsAbs(o.Root) {
		if absolute, err := filepath.Abs(o.Root); err == nil {
			o.Root = absolute
		}
	}
	if len(o.Command) == 0 {
		o.Command = []string{"cursor-agent"}
	}
	c := &cliConnector{id: o.ID, root: o.Root, command: append([]string{}, o.Command...)}
	return &engine.Connector{ID: c.id, Root: c.root, Harness: "cursor", Variant: "cli", Version: Version, Source: c, Sink: c, Vocabulary: Vocabulary{}, Capabilities: core.Capabilities{Read: core.ReadCaps{Images: true, Reasoning: "plain"}, Write: &core.WriteCaps{Mode: "native-file", Images: true, Reasoning: "plain", Tools: "native", ToolCallID: &core.IDRule{Pattern: `^call_[A-Za-z0-9_]+$`, MaxLength: 64}}, WriteProfile: "cursor-cli-write@1", FormatVersions: []string{cliFormat}}}
}
func (c *cliConnector) WatchRoots() []string           { return []string{filepath.Join(c.root, "chats")} }
func (c *cliConnector) NewSessionID(key string) string { return core.UUID5(c.id + ":" + key) }
func (c *cliConnector) ResumeCommand() string          { return "" }
func (c *cliConnector) NativeResume(session string) ([]string, map[string]string) {
	if !sessionID.MatchString(session) {
		return nil, nil
	}
	return append(append([]string{}, c.command...), "--resume", session), map[string]string{"CURSOR_CONFIG_DIR": c.root}
}

func (c *cliConnector) sessionDir(cwd, id string) string {
	h := md5.Sum([]byte(cwd))
	return filepath.Join(c.root, "chats", hex.EncodeToString(h[:]), id)
}
func openCLI(ctx context.Context, path string) (*sql.DB, error) {
	st, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("Cursor CLI database is not a regular file")
	}
	p, e := filepath.Abs(path)
	if e != nil {
		return nil, e
	}
	u := shared.FileURL(p)
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(3000)")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	var version int
	if e = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); e != nil || version != 1 {
		db.Close()
		return nil, fmt.Errorf("unsupported Cursor CLI database version %d: %v", version, e)
	}
	return db, nil
}
func cliMetadata(ctx context.Context, q querier) (core.JsonObject, []byte, error) {
	var encoded string
	var size int
	if e := q.QueryRowContext(ctx, "SELECT length(value) FROM meta WHERE key='0'").Scan(&size); e != nil {
		return nil, nil, e
	}
	if size > 2*1024*1024 {
		return nil, nil, fmt.Errorf("Cursor CLI metadata too large")
	}
	if e := q.QueryRowContext(ctx, "SELECT value FROM meta WHERE key='0'").Scan(&encoded); e != nil {
		return nil, nil, e
	}
	raw, e := hex.DecodeString(encoded)
	if e != nil {
		return nil, nil, e
	}
	var meta core.JsonObject
	e = json.Unmarshal(raw, &meta)
	return meta, raw, e
}

type cliStore struct{ *sql.Tx }

func (s *cliStore) nativeBlob(ctx context.Context, id string) ([]byte, error) {
	var size int
	if e := s.QueryRowContext(ctx, "SELECT length(data) FROM blobs WHERE id=?", id).Scan(&size); e != nil {
		return nil, e
	}
	if size > maxBlobBytes {
		return nil, fmt.Errorf("Cursor CLI blob exceeds limit")
	}
	var b []byte
	e := s.QueryRowContext(ctx, "SELECT data FROM blobs WHERE id=?", id).Scan(&b)
	return b, e
}
func cliRoot(ctx context.Context, q *cliStore, meta core.JsonObject) ([]byte, wireMessage, error) {
	id, e := hex.DecodeString(core.S(meta["latestRootBlobId"]))
	if e != nil {
		return nil, nil, e
	}
	raw, e := readBlob(ctx, q, id)
	if e != nil {
		return nil, nil, e
	}
	root, e := parseWire(raw)
	return raw, root, e
}
func (c *cliConnector) validPath(path, id string) bool {
	if !sessionID.MatchString(id) || filepath.Base(path) != "store.db" || filepath.Base(filepath.Dir(path)) != id {
		return false
	}
	rel, e := filepath.Rel(filepath.Join(c.root, "chats"), path)
	if e != nil {
		return false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 3 || len(parts[0]) != 32 {
		return false
	}
	_, e = hex.DecodeString(parts[0])
	return e == nil
}

// The ordinary CLI records the local workspace separately from its model
// checkpoint. On Windows the checkpoint can contain a server execution URI.
// Accept the explicit local path only when it belongs to this workspace index.
func cliLocalCwd(directory, workspace string) string {
	p := filepath.Join(directory, "meta.json")
	st, err := os.Lstat(p)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 64<<10 {
		return ""
	}
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	var meta struct {
		SchemaVersion int    `json:"schemaVersion"`
		Cwd           string `json:"cwd"`
	}
	b, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil || len(b) > 64<<10 || json.Unmarshal(b, &meta) != nil || meta.SchemaVersion != 1 || meta.Cwd == "" {
		return ""
	}
	hash := md5.Sum([]byte(meta.Cwd))
	if hex.EncodeToString(hash[:]) != workspace {
		return ""
	}
	return meta.Cwd
}

func (c *cliConnector) Discover(ctx context.Context) ([]core.NativeRef, error) {
	base := filepath.Join(c.root, "chats")
	workspaces, e := os.ReadDir(base)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	out := []core.NativeRef{}
	for _, w := range workspaces {
		if !w.IsDir() || len(w.Name()) != 32 {
			continue
		}
		if _, e := hex.DecodeString(w.Name()); e != nil {
			continue
		}
		sessions, e := os.ReadDir(filepath.Join(base, w.Name()))
		if e != nil {
			return nil, e
		}
		for _, s := range sessions {
			if e := ctx.Err(); e != nil {
				return nil, e
			}
			if !s.IsDir() || !sessionID.MatchString(s.Name()) {
				continue
			}
			path := filepath.Join(base, w.Name(), s.Name(), "store.db")
			db, e := openCLI(ctx, path)
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return nil, e
			}
			tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
			if e != nil {
				db.Close()
				return nil, e
			}
			meta, _, e := cliMetadata(ctx, tx)
			var root wireMessage
			if e == nil && core.S(meta["latestRootBlobId"]) != "" {
				_, root, e = cliRoot(ctx, &cliStore{tx}, meta)
			}
			tx.Rollback()
			db.Close()
			if e != nil {
				return nil, e
			}
			if core.S(meta["agentId"]) != s.Name() {
				return nil, fmt.Errorf("Cursor CLI session ID mismatch")
			}
			if len(root) == 0 {
				continue
			}
			cwd := cliLocalCwd(filepath.Dir(path), w.Name())
			if uris := root.all(9); cwd == "" && len(uris) > 0 {
				u, err := url.Parse(string(uris[0]))
				if err == nil {
					cwd, _ = shared.FileURLPath(u)
				}
			}
			st, e := os.Stat(path)
			if e != nil {
				return nil, e
			}
			updated := st.ModTime().UnixMilli()
			if wal, err := os.Stat(path + "-wal"); err == nil && wal.ModTime().UnixMilli() > updated {
				updated = wal.ModTime().UnixMilli()
			}
			out = append(out, core.NativeRef{Harness: "cursor", Connector: c.id, Session: s.Name(), Path: path, MtimeMs: float64(updated), Meta: core.JsonObject{"title": core.S(meta["name"]), "cwd": cwd, "createdAt": meta["createdAt"], "frontend": "cli", "identity": core.IdentityMeta(core.SessionIdentity{})}})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].MtimeMs != out[j].MtimeMs {
			return out[i].MtimeMs > out[j].MtimeMs
		}
		return out[i].Session < out[j].Session
	})
	return out, nil
}
func (c *cliConnector) Tail(ctx context.Context, ref core.NativeRef, from *core.SourceOffset, yield func(core.SourceBatch) error) error {
	if !c.validPath(ref.Path, ref.Session) {
		return fmt.Errorf("invalid Cursor CLI session path")
	}
	db, e := openCLI(ctx, ref.Path)
	if e != nil {
		return e
	}
	defer db.Close()
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	store := &cliStore{tx}
	meta, raw, e := cliMetadata(ctx, tx)
	if e != nil {
		return e
	}
	if core.S(meta["agentId"]) != ref.Session {
		return fmt.Errorf("Cursor CLI session ID mismatch")
	}
	root, _, e := cliRoot(ctx, store, meta)
	if e != nil {
		return e
	}
	m := core.JsonObject{"conversationState": "~" + base64.StdEncoding.EncodeToString(root), "status": "completed"}
	return tailConversation(ctx, store, ref, from, raw, m, cliFormat, false, yield)
}

type cliDelivery struct {
	Metadata core.JsonObject   `json:"metadata"`
	Blobs    map[string][]byte `json:"blobs"`
	Assets   map[string][]byte `json:"assets"`
}

func (c *cliConnector) Plan(in engine.PlanInput) (*engine.DeliveryPlan, error) {
	cwd := in.Target.Cwd
	if cwd == "" && in.Projection != nil {
		cwd = in.Projection.Hints.Cwd
	}
	if cwd == "" {
		return nil, fmt.Errorf("Cursor CLI requires a project directory")
	}
	cwd, e := filepath.Abs(cwd)
	if e != nil {
		return nil, e
	}
	in.Target.Cwd = cwd
	dir := c.sessionDir(cwd, in.Target.Session)
	writer := &connector{id: c.id, root: c.root, assetBase: dir}
	in, attachments, e := writer.prepareAttachments(in, filepath.Join(dir, "assets"))
	if e != nil {
		return nil, e
	}
	p, e := writer.Plan(in)
	if e != nil {
		return nil, e
	}
	var d ideDelivery
	if e = json.Unmarshal([]byte(p.Content), &d); e != nil {
		return nil, e
	}
	for _, value := range attachments {
		file := core.Obj(value)
		d.Assets[core.S(file["path"])] = file["data"].([]byte)
	}
	root, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(core.S(d.Metadata["conversationState"]), "~"))
	if e != nil {
		return nil, e
	}
	sha := core.SHA256Hex(root)
	d.Blobs[sha] = root
	key := make([]byte, 32)
	if _, e = rand.Read(key); e != nil {
		return nil, e
	}
	meta := core.JsonObject{"agentId": in.Target.Session, "latestRootBlobId": sha, "name": d.Metadata["name"], "mode": "default", "isRunEverything": false, "createdAt": d.Metadata["createdAt"], "blobEncryptionKey": hex.EncodeToString(key)}
	raw, e := json.Marshal(cliDelivery{meta, d.Blobs, d.Assets})
	if e != nil {
		return nil, e
	}
	p.Target.Path = filepath.Join(dir, "store.db")
	p.Content = string(raw)
	p.ID = "plan:" + core.SHA256Hex(p.Content)
	return p, nil
}
func (c *cliConnector) Apply(ctx context.Context, p *engine.DeliveryPlan) (core.JsonObject, error) {
	if p == nil || p.Mode != "create" || p.Target.Mode != "create" || p.Harness != "cursor" || p.Sink != c.id || p.Target.Path != filepath.Join(c.sessionDir(p.Target.Cwd, p.Target.Session), "store.db") || !c.validPath(p.Target.Path, p.Target.Session) || p.ID != "plan:"+core.SHA256Hex(p.Content) {
		return nil, fmt.Errorf("invalid Cursor CLI delivery")
	}
	if e := checkTargetProfile(p.Target, c.id, c.root); e != nil {
		return nil, e
	}
	var d cliDelivery
	if e := json.Unmarshal([]byte(p.Content), &d); e != nil {
		return nil, e
	}
	if core.S(d.Metadata["agentId"]) != p.Target.Session {
		return nil, fmt.Errorf("Cursor CLI identity mismatch")
	}
	meta, e := json.Marshal(d.Metadata)
	if e != nil {
		return nil, e
	}
	for sha, b := range d.Blobs {
		if b == nil || len(b) > maxBlobBytes || core.SHA256Hex(b) != sha {
			return nil, fmt.Errorf("invalid Cursor CLI blob")
		}
	}
	if _, ok := d.Blobs[core.S(d.Metadata["latestRootBlobId"])]; !ok {
		return nil, fmt.Errorf("missing Cursor CLI root")
	}
	if e = os.MkdirAll(c.root, 0700); e != nil {
		return nil, e
	}
	files, e := os.OpenRoot(c.root)
	if e != nil {
		return nil, e
	}
	defer files.Close()
	dir := filepath.Dir(p.Target.Path)
	rel, e := filepath.Rel(c.root, dir)
	if e != nil {
		return nil, e
	}
	if e = files.MkdirAll(filepath.Dir(rel), 0700); e != nil {
		return nil, e
	}
	if e = files.Mkdir(rel, 0700); os.IsExist(e) {
		if e = c.verify(ctx, p, d, meta); e != nil {
			return nil, e
		}
		return core.JsonObject{"session": p.Target.Session, "path": p.Target.Path, "noop": true}, nil
	} else if e != nil {
		return nil, e
	}
	committed := false
	var createdFiles, createdDirs []string
	defer func() {
		if !committed {
			// Never recursively remove a published session directory: a native
			// process might have begun writing its own store while we staged ours.
			for i := len(createdFiles) - 1; i >= 0; i-- {
				_ = files.Remove(createdFiles[i])
			}
			for i := len(createdDirs) - 1; i >= 0; i-- {
				_ = files.Remove(createdDirs[i])
			}
			_ = files.Remove(rel)
		}
	}()
	for file, b := range d.Assets {
		asset, e := filepath.Rel(dir, file)
		if e != nil || !strings.HasPrefix(asset, "assets"+string(filepath.Separator)) || strings.Contains(asset, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("Cursor CLI asset escapes session")
		}
		name := filepath.Join(rel, asset)
		if e = files.MkdirAll(filepath.Dir(name), 0700); e != nil {
			return nil, e
		}
		createdDirs = append(createdDirs, filepath.Join(rel, "assets"), filepath.Dir(name))
		f, e := files.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return nil, e
		}
		createdFiles = append(createdFiles, name)
		_, e = f.Write(b)
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e != nil {
			return nil, e
		}
		if ce != nil {
			return nil, ce
		}
	}
	tmp := filepath.Join(dir, ".store.db")
	f, e := files.OpenFile(filepath.Join(rel, ".store.db"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	createdFiles = append(createdFiles, filepath.Join(rel, ".store.db"))
	if e = f.Close(); e != nil {
		return nil, e
	}
	u := shared.FileURL(tmp)
	q := u.Query()
	q.Set("mode", "rw")
	q.Add("_pragma", "busy_timeout(3000)")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return nil, e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, e = db.ExecContext(ctx, "PRAGMA user_version=1; CREATE TABLE blobs(id TEXT PRIMARY KEY,data BLOB); CREATE TABLE meta(key TEXT PRIMARY KEY,value TEXT)"); e != nil {
		return nil, e
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	for sha, b := range d.Blobs {
		if _, e = tx.ExecContext(ctx, "INSERT INTO blobs VALUES(?,?)", sha, b); e != nil {
			return nil, e
		}
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO meta VALUES('0',?)", hex.EncodeToString(meta)); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	if e = db.Close(); e != nil {
		return nil, e
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	if e = publishCLIDatabase(files, rel); e != nil {
		return nil, e
	}
	committed = true
	_ = files.Remove(filepath.Join(rel, ".store.db"))
	return core.JsonObject{"session": p.Target.Session, "path": p.Target.Path}, nil
}

func publishCLIDatabase(files *os.Root, directory string) error {
	// Link publishes a fully closed database without replacing a file that a
	// native process might have created after we reserved the directory.
	return files.Link(filepath.Join(directory, ".store.db"), filepath.Join(directory, "store.db"))
}
func (c *cliConnector) verify(ctx context.Context, p *engine.DeliveryPlan, d cliDelivery, meta []byte) error {
	conflict := fmt.Errorf("Cursor CLI target already exists or changed; refusing to overwrite")
	db, e := openCLI(ctx, p.Target.Path)
	if e != nil {
		return conflict
	}
	defer db.Close()
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	_, raw, e := cliMetadata(ctx, tx)
	if e != nil || string(raw) != string(meta) {
		return conflict
	}
	var count int
	if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM blobs").Scan(&count); e != nil || count != len(d.Blobs) {
		return conflict
	}
	store := &cliStore{tx}
	for sha, want := range d.Blobs {
		got, e := store.nativeBlob(ctx, sha)
		if e != nil || core.SHA256Hex(got) != core.SHA256Hex(want) {
			return conflict
		}
	}
	for file, want := range d.Assets {
		got, e := os.ReadFile(file)
		if e != nil || core.SHA256Hex(got) != core.SHA256Hex(want) {
			return conflict
		}
	}
	return nil
}
