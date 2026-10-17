package koto

import (
	"os"
	"path/filepath"
	"testing"

	"koto/internal/hub"
)

func TestResumePreservesInheritedStorageRoot(t *testing.T) {
	for _, agent := range []string{"claude-code", "pi", "codex", "dsh"} {
		t.Run(agent, func(t *testing.T) {
			s := newSandbox(t)
			roots := map[string]string{"claude-code": s.Claude, "pi": s.Pi, "codex": s.Codex, "dsh": s.Dsh}
			key := rootEnv[agent]
			t.Setenv(key, roots[agent])
			// This source identity must never become part of a resume command.
			t.Setenv("KOTO_SESSION", "pi:unrelated-source")
			a := s.app(enable(agent))
			v := a.WithResume(hub.Binding{Harness: agent, Session: "new-target"})
			if v.Resume == nil {
				t.Fatal("missing resume command")
			}
			eq(t, v.Resume.Env[key], roots[agent], "inherited root must survive a fresh terminal")
			eq(t, len(v.Resume.Env), 1, "only the target storage override belongs in resume.env")

			extra := filepath.Join(s.Root, "目标 root O'Brien & copy")
			must(t, os.MkdirAll(extra, 0o755))
			info, err := a.AddRoot(agent, extra)
			must(t, err)
			v = a.WithResume(hub.Binding{Connector: info.ID, Harness: agent, Session: "other-target"})
			eq(t, v.Resume.Env[key], extra, "selected target root wins over inherited source root")
			eq(t, len(v.Resume.Env), 1, "source identity is not carried to another target")
		})
	}
}

func TestResumeOpenCodeUsesSelectedDatabase(t *testing.T) {
	s := newSandbox(t)
	t.Setenv("XDG_DATA_HOME", filepath.Join(s.Root, "unrelated-data"))
	t.Setenv("OPENCODE_DB", filepath.Join(s.Root, "unrelated-source.db"))
	a := s.app(enable("opencode"))
	v := a.WithResume(hub.Binding{Harness: "opencode", Session: "ses_target"})
	if v.Resume == nil {
		t.Fatal("missing resume command")
	}
	eq(t, v.Resume.Env["OPENCODE_DB"], filepath.Join(s.OpenCode, "opencode.db"), "exact target database survives cleared XDG/OPENCODE_DB")
	eq(t, len(v.Resume.Env), 1, "do not inherit another source database")
}
