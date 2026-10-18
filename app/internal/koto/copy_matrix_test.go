package koto

import (
	"path/filepath"
	"testing"

	"koto/internal/core"
)

// Exercise the app API, including same-Agent copies and repeating “new session”. Codex uses its
// actual app-server in an isolated HOME; no model is called and no real Agent directory is used.
func TestCopyAPIMatrix(t *testing.T) {
	for _, from := range copyAgents {
		for _, to := range copyAgents {
			t.Run(from+"-to-"+to, func(t *testing.T) {
				requireCopyAgent(t, from, to)
				s := newSandbox(t)
				var file, session string
				switch from {
				case "claude-code":
					file, session = writeCcSession(t, s.Claude, "", false), CCSession
				case "pi":
					file, session = writePiSession(t, s.Pi, ""), PISession
				case "codex":
					file, session = writeCodexRollout(t, s.Codex, "", nil), CodexThread
				case "antigravity":
					session = "agy-source"
					file = writeAgy(t, s.Agy, session, []agyStep{userStep(1, "你好 · matrix question", 0), modelStep(2, "matrix answer")})
				case "dsh":
					file, session = writeDsh(t, s.Dsh, dshV4), "demo"
				case "cursor":
					file, session = writeCursorNativeFixture(t, filepath.Join(s.Root, "cursor"))
				}
				a := s.app(enable(copyAgents...))
				api := &API{App: a}
				if from == "opencode" || from == "zcode" || from == "workbuddy" || from == "workbuddy-ai" {
					file, session = linearMatrixSource(t, s, api, from)
				}
				before := nativeSessionSnapshot(t, file, from, session)
				api.Reindex(ctx)
				v, err := a.OpenThread(ctx, from+":"+session)
				must(t, err)
				head := binding(t, v.Hub, from, session).Leaf
				seen := map[string]bool{}
				for i := 0; i < 2; i++ {
					req := CarryRequest{From: head, To: to, Cwd: s.Root, Title: "copy " + itoa(i), DryRun: true}
					preview, err := api.Carry(ctx, req)
					must(t, err)
					eq(t, preview.Plan.Mode, "create")
					req.DryRun = false
					r, err := api.Carry(ctx, req)
					must(t, err)
					ok(t, r.Applied, "copy must really be applied")
					native := core.S(r.Receipt["session"])
					if native == "" {
						native = r.Plan.Target.Session
					}
					ok(t, native != session || from != to, "source session must not be reused")
					ok(t, !seen[native], "each new-session action creates a distinct session")
					seen[native] = true
					eq(t, nativeSessionSnapshot(t, file, from, session), before, "source session unchanged")
					out, err := api.Thread(ctx, to+":"+native, 0, "")
					must(t, err)
					users, answers := 0, 0
					for _, ev := range out.Events {
						if ev.Kind == "message" && ev.Actor == "user" {
							users++
						}
						if ev.Kind == "message" && ev.Actor == "assistant" {
							answers++
						}
					}
					ok(t, users > 0 && answers > 0, "destination must contain both questions and answers")
				}
			})
		}
	}
}
