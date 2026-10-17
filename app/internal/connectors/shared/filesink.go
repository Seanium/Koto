// Package shared provides atomic native-session creation and read-only open-session probes.
package shared

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/hub"
)

// NativeLine is one record a sink writes.
type NativeLine struct {
	NativeID string
	EventID  string
	Line     string
}

// LinesToContent joins lines and lists their records.
func LinesToContent(lines []NativeLine) (string, []engine.PlanRecord) {
	var b strings.Builder
	recs := make([]engine.PlanRecord, 0, len(lines))
	for _, l := range lines {
		b.WriteString(l.Line)
		b.WriteByte('\n')
		recs = append(recs, engine.PlanRecord{NativeID: l.NativeID, EventID: l.EventID, SHA256: core.SHA256Hex(l.Line)})
	}
	return b.String(), recs
}

func decodeContent(plan *engine.DeliveryPlan) []byte {
	if core.S(plan.Extra["encoding"]) == "base64" {
		b, _ := base64.StdEncoding.DecodeString(plan.Content)
		return b
	}
	return []byte(plan.Content)
}

// ApplyFilePlan publishes a new private file without overwriting any existing session.
func ApplyFilePlan(plan *engine.DeliveryPlan) (core.JsonObject, error) {
	file := plan.Target.Path
	if file == "" {
		return nil, errors.New("file plan without path/content")
	}
	content := decodeContent(plan)
	var lastNative any
	if n := len(plan.Records); n > 0 {
		lastNative = plan.Records[n-1].NativeID

	}
	if plan.Mode != "create" {
		return nil, errors.New("only new-session copies are supported")
	}
	if err := createExclusive(file, content); err != nil {
		return nil, err
	}
	st, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	return core.JsonObject{"session": plan.Target.Session, "path": file, "size": float64(st.Size()), "mtimeMs": engine.MtimeMs(st), "lastNativeId": lastNative}, nil
}

func randHex() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// BeforeLink runs between writing a new session's temp file and linking it in place (tests).
var BeforeLink func(file string)

func createExclusive(file string, content []byte) error {
	same := func() bool { b, err := os.ReadFile(file); return err == nil && bytes.Equal(b, content) }
	if _, err := os.Stat(file); err == nil {
		if !same() {
			return &engine.TargetChangedError{Msg: file + " already exists with different content"}
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.koto-tmp-%d-%s", file, os.Getpid(), randHex())
	defer os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if BeforeLink != nil {
		BeforeLink(file)
	}
	if err := os.Link(tmp, file); err != nil { // atomic and never replaces an existing file
		if errors.Is(err, os.ErrExist) {
			if !same() {
				return &engine.TargetChangedError{Msg: file + " appeared while writing it, with different content"}
			}
			return nil
		}
		if !errors.Is(err, syscall.EPERM) && !errors.Is(err, syscall.ENOTSUP) && !errors.Is(err, syscall.EXDEV) && !errors.Is(err, syscall.ENOSYS) {
			return err
		}
		g, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // no hard links here: exclusive create
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				if !same() {
					return &engine.TargetChangedError{Msg: file + " appeared while writing it, with different content"}
				}
				return nil
			}
			return err
		}
		defer g.Close()
		if _, err := g.Write(content); err != nil {
			return err
		}
		if err := g.Sync(); err != nil {
			return err
		}
	}
	FsyncDir(filepath.Dir(file))
	return nil
}

// FsyncDir syncs a directory entry (best effort).
func FsyncDir(dir string) {
	if f, err := os.Open(dir); err == nil {
		_ = f.Sync()
		f.Close()
	}
}

// ReadRange reads len bytes at at.
func ReadRange(file string, at, n int64) ([]byte, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if n < 0 {
		n = 0
	}
	b := make([]byte, n)
	got, err := f.ReadAt(b, at)
	if got < len(b) {
		return b[:got], nil
	}
	return b, err
}

// Clock gives monotonic, deterministic timestamps for rendered records.
type Clock struct{ last float64 }

func (c *Clock) Next(e *core.Event) float64 {
	t := c.last + 1
	if e.Time != nil && *e.Time > c.last {
		t = *e.Time
	}
	c.last = t
	return t
}

func (c *Clock) Seed(t *float64) {
	if t != nil && *t > c.last {
		c.last = *t
	}
}

// PartsText is a part list as text, inlining text-like file blobs (e.g. CC tool-results).
func PartsText(h *hub.Hub, parts []core.Part, maxInline int) string {
	if maxInline == 0 {
		maxInline = 2 * 1024 * 1024
	}
	var out []string
	for _, p := range parts {
		var s string
		switch core.S(p["type"]) {
		case "text":
			s = core.S(p["text"])
		case "file":
			if strings.HasPrefix(core.S(p["mediaType"]), "text/") {
				blob := core.Obj(p["blob"])
				if b, ok := h.Blob(core.S(blob["sha256"])); ok {
					if len(b) > maxInline {
						b = b[:maxInline]
					}
					s = string(b)
				} else {
					name := core.S(p["name"])
					if name == "" {
						name = core.S(blob["sha256"])
					}
					s = "[file " + name + " unavailable]"
				}
			}
		case "image":
			s = "[image " + core.S(p["mediaType"]) + "]"
		case "tool_result":
			s = PartsText(h, core.Parts(p["parts"]), maxInline)
		}
		if s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, "\n")
}

// BlobBase64 of a stored blob.
func BlobBase64(h *hub.Hub, sha string) (string, bool) {
	b, ok := h.Blob(sha)
	if !ok {
		return "", false
	}
	return base64.StdEncoding.EncodeToString(b), true
}

// PlanID: dl:<hash of sink, session, head, mode, content>.
func PlanID(sink, session, head, mode, contentSha string) string {
	return "dl:" + core.SHA256Hex(sink + "\x00" + session + "\x00" + head + "\x00" + mode + "\x00" + contentSha)[:32]
}

// ISO is a JS toISOString of ms since epoch.
func ISO(ms float64) string {
	return time.UnixMilli(int64(ms)).UTC().Format("2006-01-02T15:04:05.000Z")
}

// ISONoColons is ISO with : and . replaced by - (file names).
func ISONoColons(ms float64) string {
	return strings.NewReplacer(":", "-", ".", "-").Replace(ISO(ms))
}

// PathIndex is session → file, shared between a connector's discover and its other calls.
type PathIndex struct {
	mu sync.Mutex
	m  map[string]string
}

func NewPathIndex() *PathIndex { return &PathIndex{m: map[string]string{}} }

func (p *PathIndex) Set(k, v string) { p.mu.Lock(); p.m[k] = v; p.mu.Unlock() }

func (p *PathIndex) Get(k string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.m[k]
	return v, ok
}
