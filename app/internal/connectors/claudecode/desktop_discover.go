package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"sort"

	"koto/internal/core"
	"koto/internal/engine"
)

// Desktop metadata already identifies both the transcript and its project. Do
// not walk every CLI transcript again for each account/organization namespace.
func (c *cc) discoverDesktop(ctx context.Context) ([]core.NativeRef, error) {
	profile := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(c.desktopScope))))
	index, err := readDesktopScopeContext(ctx, c.desktopScope, profile, c.identity)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(index))
	for id := range index {
		if sessionFile.MatchString(id + ".jsonl") {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var out []core.NativeRef
	var unresolved []string
	seen := map[string]bool{}
	add := func(id, path string) bool {
		if seen[path] {
			return true
		}
		st, e := os.Stat(path)
		if e != nil || !st.Mode().IsRegular() {
			return false
		}
		seen[path] = true
		meta := core.JsonObject{"projectDir": filepath.Base(filepath.Dir(path)), "identity": core.IdentityMeta(c.identity)}
		for k, v := range index[id] {
			meta[k] = v
		}
		c.files.Set(id, path)
		out = append(out, core.NativeRef{Harness: "claude-code", Connector: c.id, Session: id, Path: path, Size: st.Size(), MtimeMs: engine.MtimeMs(st), Meta: meta})
		return true
	}
	for _, id := range ids {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		meta := index[id]
		bindings := []any{meta}
		if b := core.Arr(meta["desktopBindings"]); len(b) > 0 {
			bindings = b
		}
		found := false
		for _, binding := range bindings {
			for _, key := range []string{"desktopCwd", "desktopOriginCwd"} {
				if cwd := core.S(core.Obj(binding)[key]); cwd != "" {
					if add(id, filepath.Join(c.projects, ProjectDir(cwd), id+".jsonl")) {
						found = true
					}
				}
			}
		}
		if !found {
			unresolved = append(unresolved, id)
		}
	}
	// Older Desktop indexes may omit cwd, or the transcript may have moved.
	// Probe those known IDs in project directories without listing or parsing
	// unrelated sessions. This fallback preserves their account binding.
	if len(unresolved) > 0 {
		dirs, _ := os.ReadDir(c.projects)
		for _, dir := range dirs {
			if e := ctx.Err(); e != nil {
				return nil, e
			}
			if !dir.IsDir() {
				continue
			}
			for _, id := range unresolved {
				if e := ctx.Err(); e != nil {
					return nil, e
				}
				add(id, filepath.Join(c.projects, dir.Name(), id+".jsonl"))
			}
		}
	}
	return out, ctx.Err()
}
