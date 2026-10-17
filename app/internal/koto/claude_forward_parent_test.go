package koto

import (
	"fmt"
	"os"
	"testing"

	"koto/internal/connectors/claudecode"
	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/hub"
)

// Claude Code 2.1.300 flushes attachment records before their user parent.
// Test the real file order at each batching boundary and while the file grows.
func TestClaudeForwardAttachmentParents(t *testing.T) {
	for _, size := range []int{1, 2, 3, 2000} {
		for _, incremental := range []bool{false, true} {
			t.Run(fmt.Sprintf("batch-%d/incremental-%t", size, incremental), func(t *testing.T) {
				s := newSandbox(t)
				file := writeCcSession(t, s.Claude, "", false)
				attachment := func(id, parent string) any {
					return ccRec(id, parent, 1, core.O{{K: "type", V: "attachment"}, {K: "attachment", V: map[string]any{"type": "date"}}, {K: "rendered", V: []any{map[string]any{"content": "context kept for analysis"}}}})
				}
				rows := []any{
					attachment("ctx1", "u1"), attachment("ctx2", "ctx1"), attachment("ctx3", "ctx2"),
					ccRec("u1", "", 0, ccUser("Remember my marker")),
					ccRec("a1", "ctx3", 2, ccAssistant("m1", []any{ccText("Marker remembered")}, "end_turn")),
				}
				writeLines(t, file, nil)
				h := hub.New()
				defer h.Close()
				ref := core.NativeRef{Harness: "claude-code", Connector: "claude-code", Session: CCSession, Path: file}
				var offset *core.SourceOffset
				read := func() {
					must(t, engine.TailJSONL(ctx, ref, offset, claudecode.Decoder{}, func(batch core.SourceBatch) error {
						h.Ingest(batch, hub.ConnectorInfo{ID: "claude-code", Harness: "claude-code"})
						o := batch.Offset
						offset = &o
						return nil
					}, size, false))
				}
				if incremental {
					appendLines(t, file, rows[:3])
					read()
					eq(t, binding(t, h, "claude-code", CCSession).Leaf, "", "unwritten prompt is not a disconnected root")
					for _, row := range rows[3:] {
						appendLines(t, file, []any{row})
						read()
					}
				} else {
					appendLines(t, file, rows)
					read()
				}
				b := binding(t, h, "claude-code", CCSession)
				path := h.Path(b.Leaf)
				jsonEq(t, kindsActors(path), []string{"message:user", "context:harness", "context:harness", "context:harness", "message:assistant"})
				eq(t, len(h.Leaves(b.Thread)), 1, "no stray branch")
				for _, ev := range path {
					raw, err := engine.ReadRecordAt(file, *ev.Provenance.Native.Offset)
					must(t, err)
					eq(t, core.SHA256Hex(raw), ev.Provenance.Residual.SHA256, "source offsets still match")
				}
				// No-op re-read must not insert a second chain.
				read()
				eq(t, len(h.ThreadEvents(b.Thread)), 5)
				before, err := os.ReadFile(file)
				must(t, err)
				a := s.app(enable("claude-code", "pi"))
				api := newAPI(a)
				api.Reindex(ctx)
				d, err := api.Thread(ctx, "claude-code:"+CCSession, 0, "")
				must(t, err)
				eq(t, d.CompleteTurns, 1, "complete question and reply")
				copied, err := api.Carry(ctx, CarryRequest{From: d.Head, To: "pi", RequestID: "forward-parent", Cwd: s.Root})
				must(t, err)
				out, err := api.Thread(ctx, "pi:"+copied.Plan.Target.Session, 0, "")
				must(t, err)
				jsonEq(t, kindsActors(out.Events), []string{"message:user", "message:assistant"})
				after, err := os.ReadFile(file)
				must(t, err)
				eq(t, string(after), string(before), "native source unchanged")
			})
		}
	}
}
