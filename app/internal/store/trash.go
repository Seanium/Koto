package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/hub"
)

// TrashError carries a code the app maps to a status: NOT_FOUND, SESSION_OPEN, TRASH_BLOCKED, RESTORE_CONFLICT.
type TrashError struct {
	Code   string
	Msg    string
	Detail core.JsonObject
}

func (e *TrashError) Error() string { return e.Msg }

// Trash moves a conversation's native sessions out of their harnesses, and back. Nothing here ever
// deletes a session: files are moved under <data>/trash/<id>/ by rename (same volume), or by copy +
// verify + remove (another volume), so every step leaves one complete copy. A harness with its own
// archive (Codex) archives instead; a read-only one is only hidden in koto.
type Trash struct {
	Store    *Store
	Registry *engine.Registry
	Dir      string
	// Rename stands in for os.Rename (tests force the cross-volume path).
	Rename func(from, to string) error
}

func lexists(p string) bool { _, err := os.Lstat(p); return err == nil }

// treeHash is a content hash of a file or directory tree (names, symlink targets, bytes).
func treeHash(p string) (string, error) {
	h := sha256.New()
	var walk func(x, rel string) error
	walk = func(x, rel string) error {
		st, err := os.Lstat(x)
		if err != nil {
			return err
		}
		switch {
		case st.Mode()&os.ModeSymlink != 0:
			t, _ := os.Readlink(x)
			fmt.Fprintf(h, "l\x00%s\x00%s\x00", rel, t)
		case st.IsDir():
			fmt.Fprintf(h, "d\x00%s\x00", rel)
			ents, _ := os.ReadDir(x)
			names := make([]string, len(ents))
			for i, e := range ents {
				names[i] = e.Name()
			}
			sort.Strings(names)
			for _, n := range names {
				if err := walk(filepath.Join(x, n), rel+"/"+n); err != nil {
					return err
				}
			}
		default:
			fmt.Fprintf(h, "f\x00%s\x00%d\x00", rel, st.Size())
			f, err := os.Open(x)
			if err != nil {
				return err
			}
			_, err = io.Copy(h, f)
			f.Close()
			return err
		}
		return nil
	}
	if err := walk(p, ""); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyTree(from, to string) error {
	st, err := os.Lstat(from)
	if err != nil {
		return err
	}
	switch {
	case st.Mode()&os.ModeSymlink != 0:
		t, err := os.Readlink(from)
		if err != nil {
			return err
		}
		return os.Symlink(t, to)
	case st.IsDir():
		if err := os.Mkdir(to, st.Mode().Perm()); err != nil {
			return err
		}
		ents, _ := os.ReadDir(from)
		for _, e := range ents {
			if err := copyTree(filepath.Join(from, e.Name()), filepath.Join(to, e.Name())); err != nil {
				return err
			}
		}
	default:
		in, err := os.Open(from)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, st.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
	}
	_ = os.Chtimes(to, st.ModTime(), st.ModTime())
	return nil
}

// move never leaves a file missing from both places.
func (t *Trash) move(from, to string) error {
	if lexists(to) {
		return &TrashError{Code: "RESTORE_CONFLICT", Msg: to + " already exists", Detail: core.JsonObject{"paths": []any{to}}}
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	rename := t.Rename
	if rename == nil {
		rename = os.Rename
	}
	err := rename(from, to)
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copyTree(from, to); err != nil {
		_ = os.RemoveAll(to)
		return err
	}
	a, e1 := treeHash(from)
	b, e2 := treeHash(to)
	if e1 != nil || e2 != nil || a != b {
		_ = os.RemoveAll(to) // our own incomplete copy; the original is untouched
		return fmt.Errorf("copy of %s did not verify; nothing moved", from)
	}
	return os.RemoveAll(from)
}

// TitleOf is the first title a conversation's sessions carry.
func TitleOf(bs []hub.Binding) *string {
	for _, b := range bs {
		for _, k := range []string{"title", "desktopTitle", "aiTitle", "name"} {
			if s, ok := b.Meta[k].(string); ok && s != "" {
				return &s
			}
		}
	}
	return nil
}

func randHex() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Context is the conversation being trashed: its working copy and the engine that guards writes.
type Context struct {
	Selector string
	Hub      *hub.Hub
	Engine   *engine.Engine
	Thread   string
}

// Put moves a conversation's sessions (or only one harness's) to the trash. Refuses while any of
// them is open in its harness or has a torn koto write waiting for repair.
func (t *Trash) Put(ctx context.Context, c Context, harness string) (*TrashEntry, error) {
	var entry *TrashEntry
	err := c.Engine.Locked(ctx, func() error {
		thread := c.Thread
		var bs []hub.Binding
		for _, b := range c.Hub.Bindings(thread) {
			if (harness == "" || b.Harness == harness) && (c.Selector == "" || strings.HasPrefix(c.Selector, "th:") || c.Selector == b.Harness+":"+b.Session) {
				bs = append(bs, b)
			}
		}
		if len(bs) == 0 {
			msg := thread + " has no session outside the trash"
			if harness != "" {
				msg = fmt.Sprintf("%s has no %s session outside the trash", thread, harness)
			}
			return &TrashError{Code: "NOT_FOUND", Msg: msg}
		}
		var open []string
		for _, b := range bs {
			conn := t.connector(b.Connector, b.Harness)
			if conn != nil && conn.Sink != nil {
				if oc, ok := conn.Sink.(engine.OpenChecker); ok {
					if why := oc.OpenBy(engine.SessionRef{Session: b.Session, Path: b.Path, Meta: b.Meta}); why != "" {
						open = append(open, fmt.Sprintf("%s:%s: %s", b.Harness, b.Session, why))
					}
				}
			}
		}
		if len(open) > 0 {
			return &TrashError{Code: "SESSION_OPEN", Msg: "close it in its harness first: " + strings.Join(open, "; "), Detail: core.JsonObject{"open": toAny(open)}}
		}
		trashID := "tr_" + strconv.FormatInt(time.Now().UnixMilli(), 36) + randHex()
		dir := filepath.Join(t.Dir, trashID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		var items []TrashItem
		manifest := func() {
			var h any = nil
			if harness != "" {
				h = harness
			}
			b, _ := json.MarshalIndent(map[string]any{"trashId": trashID, "thread": thread, "harness": h, "items": items}, "", "  ")
			_ = os.WriteFile(filepath.Join(dir, "manifest.json"), append(b, '\n'), 0o644)
		}
		undo := func() {
			for i := len(items) - 1; i >= 0; i-- {
				it := items[i]
				for _, f := range it.Files {
					if lexists(f.To) && !lexists(f.From) {
						_ = t.move(f.To, f.From)
					}
				}
				if it.Via == "archive" && it.Archive != nil {
					if conn := t.connector(it.Connector, it.Harness); conn != nil {
						if a, ok := conn.Trash.(engine.Archiver); ok {
							p := ""
							if it.OriginalPath != nil {
								p = *it.OriginalPath
							}
							_ = a.Unarchive(ctx, engine.SessionRef{Session: it.Session, Path: p, Meta: core.JsonObject{}}, it.Archive)
						}
					}
				}
			}
			_ = os.Remove(filepath.Join(dir, "manifest.json"))
			removeEmptyDirs(dir)
		}
		for n, b := range bs {
			conn := t.connector(b.Connector, b.Harness)
			var op *string
			if b.Path != "" {
				p := b.Path
				op = &p
			}
			items = append(items, TrashItem{Harness: b.Harness, Session: b.Session, Connector: b.Connector, OriginalPath: op, Via: "hub", Files: []TrashFile{}})
			it := &items[len(items)-1]
			ref := engine.SessionRef{Session: b.Session, Path: b.Path, Meta: b.Meta}
			var arch engine.Archiver
			var files engine.Trasher
			if conn != nil {
				arch, _ = conn.Trash.(engine.Archiver)
				files, _ = conn.Trash.(engine.Trasher)
			}
			switch {
			case arch != nil:
				it.Via = "archive"
				manifest()
				r, err := arch.Archive(ctx, ref)
				if err != nil {
					undo()
					return err
				}
				it.Archive = r
				if r["alreadyArchived"] == true {
					s := fmt.Sprintf("already archived in %s; stays archived on restore", b.Harness)
					it.Note = &s
				}
			case files != nil:
				fs := files.Files(ref)
				if len(fs) == 0 {
					s := "its native files were already gone"
					it.Note = &s
					continue
				}
				it.Via = "move"
				p := fs[0]
				it.OriginalPath = &p
				for k, from := range fs {
					it.Files = append(it.Files, TrashFile{From: from, To: filepath.Join(dir, strconv.Itoa(n), strconv.Itoa(k), filepath.Base(from))})
				}
				manifest() // written before the moves: a crash leaves a map back to the originals
				for _, f := range it.Files {
					if err := t.move(f.From, f.To); err != nil {
						undo()
						return err
					}
				}
			default:
				s := fmt.Sprintf("%s is read-only for koto: only hidden here, its files are untouched", b.Harness)
				if conn == nil {
					s = b.Connector + " is not registered: only hidden here"
				}
				it.Note = &s
			}
		}
		manifest()
		var h *string
		if harness != "" {
			h = &harness
		}
		e := TrashEntry{TrashID: trashID, Thread: thread, Harness: h, Title: TitleOf(bs), Items: items, TrashedAt: time.Now().UnixMilli()}
		if len(bs) == 1 {
			e.Thread = bs[0].Harness + ":" + bs[0].Session
		}
		if err := t.Store.AddTrash(e); err != nil {
			return err
		}
		entry = &e
		return nil
	})
	return entry, err
}

func toAny(xs []string) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}

func (t *Trash) connector(id, harness string) *engine.Connector {
	if c := t.Registry.Get(id); c != nil {
		return c
	}
	if cs := t.Registry.ByHarness(harness); len(cs) > 0 {
		return cs[0]
	}
	return nil
}

// Restore puts an entry's sessions back where they were; refuses when something occupies a path.
func (t *Trash) Restore(ctx context.Context, id string) (*TrashEntry, error) {
	e, ok := t.Store.TrashEntry(id)
	if !ok || e.RestoredAt != nil {
		return nil, &TrashError{Code: "NOT_FOUND", Msg: id + " is not in the trash"}
	}
	var taken, lost []string
	for _, it := range e.Items {
		for _, f := range it.Files {
			if lexists(f.From) && lexists(f.To) {
				taken = append(taken, f.From)
			}
			if !lexists(f.From) && !lexists(f.To) {
				lost = append(lost, f.To)
			}
		}
	}
	if len(taken) > 0 {
		return nil, &TrashError{Code: "RESTORE_CONFLICT", Msg: "something is already at " + strings.Join(taken, ", ") + "; move it away first", Detail: core.JsonObject{"paths": toAny(taken)}}
	}
	if len(lost) > 0 {
		return nil, &TrashError{Code: "RESTORE_CONFLICT", Msg: "the trash copy is missing: " + strings.Join(lost, ", "), Detail: core.JsonObject{"missing": toAny(lost)}}
	}
	for _, it := range e.Items {
		if it.Via != "archive" || it.Archive == nil {
			continue
		}
		conn := t.connector(it.Connector, it.Harness)
		var a engine.Archiver
		if conn != nil {
			a, _ = conn.Trash.(engine.Archiver)
		}
		if a == nil {
			return nil, &TrashError{Code: "TRASH_BLOCKED", Msg: fmt.Sprintf("%s is not available to unarchive %s", it.Connector, it.Session)}
		}
		p := ""
		if it.OriginalPath != nil {
			p = *it.OriginalPath
		}
		if err := a.Unarchive(ctx, engine.SessionRef{Session: it.Session, Path: p, Meta: core.JsonObject{}}, it.Archive); err != nil {
			return nil, err
		}
	}
	for _, it := range e.Items {
		for _, f := range it.Files {
			if lexists(f.To) {
				if err := t.move(f.To, f.From); err != nil {
					return nil, err
				}
			}
		}
	}
	t.Store.RestoreTrash(id)
	dir := filepath.Join(t.Dir, id)
	_ = os.Remove(filepath.Join(dir, "manifest.json"))
	removeEmptyDirs(dir)
	r, _ := t.Store.TrashEntry(id)
	return &r, nil
}

// removeEmptyDirs removes a tree that holds nothing but empty directories (never a file).
func removeEmptyDirs(dir string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if e.IsDir() {
			removeEmptyDirs(filepath.Join(dir, e.Name()))
		}
	}
	_ = os.Remove(dir)
}
