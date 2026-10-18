package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"koto/internal/engine"
)

func TestCoworkRejectsDirectWriterAndResume(t *testing.T) {
	root := Root{ID: "cowork", Root: t.TempDir(), Profile: "Claude-3p"}
	c := NewCowork(root)
	if c.Sink != nil || c.Capabilities.Write != nil || c.Capabilities.WriteProfile != "" || c.Source == nil {
		t.Fatal("Cowork must expose only a readable source")
	}
	// Even a caller holding the shared implementation cannot invoke the Code
	// writer on an existing Cowork task's transcript root.
	source := c.Source.(*cc)
	if source.ResumeCommand() != "" {
		t.Fatal("a CLI resume command does not open a native Cowork task")
	}
	target := engine.DeliveryTarget{Mode: "create", Session: "new", Path: filepath.Join(root.Root, "new.jsonl")}
	if _, err := source.Plan(engine.PlanInput{Target: target}); err == nil {
		t.Fatal("direct plan accepted a Cowork destination")
	}
	if _, err := source.Apply(context.Background(), &engine.DeliveryPlan{Mode: "create", Target: target, Content: "must not write"}); err == nil {
		t.Fatal("direct apply accepted a Cowork destination")
	}
	code := New(Options{Root: t.TempDir()})
	if code.Sink == nil || code.Capabilities.Write == nil || code.Sink.ResumeCommand() == "" {
		t.Fatal("ordinary Claude Code must remain writable")
	}
}

func TestCoworkExplicitRootAndAliasRemainReadOnly(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "Claude-3p", "local-agent-mode-sessions", "gateway", "org", "local_task", ".claude")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	check := func(path string) {
		t.Helper()
		c := New(Options{Root: path, ID: "explicit"})
		if c.ID != "explicit" || c.ProfileKind != "cowork" || c.Variant != "desktop" || c.Sink != nil || c.Capabilities.Write != nil {
			t.Fatalf("explicit root bypassed native Cowork boundary: %+v", c)
		}
		source := c.Source.(*cc)
		if source.desktopProfile != "Claude-3p" || len(source.identity.Claims) != 1 || source.identity.Claims[0].Kind != "api-configuration" {
			t.Fatalf("native storage identity lost: %+v", source.identity)
		}
	}
	check(root)
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(root, alias); err != nil {
		if runtime.GOOS == "windows" {
			t.Logf("directory symlink unavailable: %v; explicit root covered", err)
		} else {
			t.Fatal(err)
		}
	} else {
		check(alias)
	}
	for _, path := range []string{filepath.Join(base, "local_task", ".claude"), filepath.Join(base, "local-agent-mode-sessions", "local_task", ".claude")} {
		if c := New(Options{Root: path}); c.Sink == nil || c.ProfileKind == "cowork" {
			t.Fatalf("a partial directory-name match disabled ordinary Code: %s", path)
		}
	}
}
