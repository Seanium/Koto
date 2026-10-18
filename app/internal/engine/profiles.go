package engine

import (
	"koto/internal/core"
	"path/filepath"
	"runtime"
	"strings"
)

// NormalizeRoot compares native directory identities, including symlink aliases.
func NormalizeRoot(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = filepath.Clean(path)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	abs = filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	return abs
}

// ProfileMeta adds storage identity without changing native historical claims.
func ProfileMeta(c *Connector, meta core.JsonObject) core.JsonObject {
	out := core.JsonObject{}
	for k, v := range meta {
		out[k] = v
	}
	label := c.ProfileLabel
	if label == "" {
		label = c.Root
	}
	kind := c.ProfileKind
	if kind == "" {
		kind = "directory"
	}
	out["sourceProfile"] = map[string]any{"id": c.ID, "harness": c.Harness, "root": c.Root, "label": label, "kind": kind, "variant": ProfileVariant(c), "default": c.Default}
	out["identity"] = core.IdentityMeta(core.ReadIdentity(meta))
	return out
}

func ProfileVariant(c *Connector) string {
	if c.Variant != "" {
		return c.Variant
	}
	switch c.ProfileKind {
	case "desktop-account", "cowork":
		return "desktop"
	}
	return DefaultVariant(c.Harness)
}

func ProfileID(harness, root, variant string) string {
	if variant == "" {
		variant = DefaultVariant(harness)
	}
	return harness + "@" + core.SHA256Hex(variant + "\x00" + NormalizeRoot(root))[:16]
}

func DefaultVariant(harness string) string {
	switch harness {
	case "workbuddy", "workbuddy-ai":
		return "desktop"
	case "zcode":
		return "shared"
	}
	return "cli"
}
