package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
)

// Desktop publishes the account-scoped index last, after the transcript and all
// its assets are complete. Only new files are created; source records and login
// configuration are never copied. Replaying the same completed plan is safe.
func (c *cc) applyDesktop(ctx context.Context, plan *engine.DeliveryPlan) (core.JsonObject, error) {
	if plan.Mode != "create" || plan.Sink != c.id || plan.Harness != "claude-code" || !sessionFile.MatchString(plan.Target.Session+".jsonl") {
		return nil, fmt.Errorf("invalid Claude Desktop copy")
	}
	rel, e := filepath.Rel(c.projects, plan.Target.Path)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.Base(plan.Target.Path) != plan.Target.Session+".jsonl" {
		return nil, fmt.Errorf("Claude Desktop transcript escapes target root")
	}
	metadata := core.Obj(plan.Extra["desktopMetadata"])
	if core.S(metadata["cliSessionId"]) != plan.Target.Session || core.S(metadata["sessionId"]) != "local_"+plan.Target.Session {
		return nil, fmt.Errorf("Claude Desktop metadata does not match transcript")
	}
	// The path is derived from the selected connector, never from source metadata.
	metadataPath := filepath.Join(c.desktopScope, "local_"+plan.Target.Session+".json")
	raw, e := json.Marshal(metadata)
	if e != nil {
		return nil, e
	}
	type file struct {
		root, path string
		data       []byte
	}
	files := []file{}
	for _, value := range core.Arr(plan.Extra["attachments"]) {
		a := core.Obj(value)
		path := core.S(a["path"])
		assetRoot := strings.TrimSuffix(plan.Target.Path, ".jsonl") + string(filepath.Separator) + "attachments"
		r, e := filepath.Rel(assetRoot, path)
		if e != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("Claude Desktop attachment escapes target session")
		}
		var data []byte
		switch b := a["data"].(type) {
		case []byte:
			data = b
		case string:
			data = core.Base64(b)
		default:
			return nil, fmt.Errorf("invalid attachment bytes")
		}
		files = append(files, file{c.root, path, data})
	}
	files = append(files, file{c.root, plan.Target.Path, []byte(plan.Content)}, file{c.desktopScope, metadataPath, raw})
	type owned struct {
		root *os.Root
		path string
		info os.FileInfo
		data []byte
	}
	roots := map[string]*os.Root{}
	var created []owned
	committed := false
	defer func() {
		if !committed {
			for i := len(created) - 1; i >= 0; i-- {
				f := created[i]
				st, er := f.root.Stat(f.path)
				if er == nil && os.SameFile(st, f.info) {
					current, er := f.root.ReadFile(f.path)
					if er == nil && bytes.Equal(current, f.data) {
						_ = f.root.Remove(f.path)
					}
				}
			}
		}
		for _, r := range roots {
			r.Close()
		}
	}()
	for _, f := range files {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		r := roots[f.root]
		if r == nil {
			r, e = os.OpenRoot(f.root)
			if e != nil {
				return nil, e
			}
			roots[f.root] = r
		}
		relative, e := filepath.Rel(f.root, f.path)
		if e != nil {
			return nil, e
		}
		if old, e := r.ReadFile(relative); e == nil {
			if !bytes.Equal(old, f.data) {
				return nil, &engine.TargetChangedError{Msg: "Claude Desktop target already exists with different content"}
			}
			continue
		}
		if e = r.MkdirAll(filepath.Dir(relative), 0700); e != nil {
			return nil, e
		}
		tmp := relative + ".koto-" + core.UUID5(fmt.Sprintf("%s:%d", relative, time.Now().UnixNano()))
		staged, e := r.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return nil, e
		}
		if _, e = staged.Write(f.data); e == nil {
			e = staged.Sync()
		}
		ce := staged.Close()
		if e == nil {
			e = ce
		}
		if e != nil {
			_ = r.Remove(tmp)
			return nil, e
		}
		// Native roots on supported desktop filesystems support same-volume links.
		// If they do not, fail closed rather than publishing a partial index.
		e = r.Link(tmp, relative)
		_ = r.Remove(tmp)
		if e != nil {
			if old, er := r.ReadFile(relative); er == nil && bytes.Equal(old, f.data) {
				continue
			}
			return nil, e
		}
		st, e := r.Stat(relative)
		if e != nil {
			return nil, e
		}
		created = append(created, owned{r, relative, st, f.data})
		shared.FsyncDir(filepath.Dir(f.path))
	}
	committed = true
	c.files.Set(plan.Target.Session, plan.Target.Path)
	return core.JsonObject{"session": plan.Target.Session, "path": plan.Target.Path, "desktopMetadataPath": metadataPath}, nil
}
