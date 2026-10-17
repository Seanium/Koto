package koto

import (
	"runtime"
	"strings"
	"testing"

	"koto/internal/hub"
)

func TestDshResumeReadsExplicitStdinTask(t *testing.T) {
	s := newSandbox(t)
	a := s.app(enable("dsh"))
	v := a.WithResume(hub.Binding{Harness: "dsh", Session: "session-O'Brien"})
	if v.Resume == nil {
		t.Fatal("missing resume command")
	}
	jsonEq(t, v.Resume.Argv, []string{"dsh", "headless", "--session-id", "session-O'Brien", "-"})
	key := "Ctrl+D"
	if runtime.GOOS == "windows" {
		key = "Enter"
		if !strings.Contains(v.Resume.Shell, "Read-Host 'Message'") || !strings.Contains(v.Resume.Command, "Read-Host 'Message'") {
			t.Fatal("Windows stdin resume must prompt for a line", v.Resume)
		}
	}
	if !strings.Contains(v.Resume.Instructions, key) || strings.Contains(v.Resume.Instructions, "Ctrl+Z") {
		t.Fatal("stdin continuation must describe the platform's verified submission method", v.Resume.Instructions)
	}
	// A user-supplied TUI/web command has different input semantics.
	t.Setenv("KOTO_DSH_RESUME_CMD", "my-dsh --session {id}")
	custom := a.WithResume(hub.Binding{Harness: "dsh", Session: "session-O'Brien"})
	jsonEq(t, custom.Resume.Argv, []string{"my-dsh", "--session", "session-O'Brien"})
	eq(t, custom.Resume.Instructions, "")
	if strings.Contains(custom.Resume.Shell, "Read-Host") {
		t.Fatal("custom resume must keep its own input behavior", custom.Resume)
	}
}
