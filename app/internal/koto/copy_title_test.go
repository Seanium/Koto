package koto

import (
	"koto/internal/engine"
	"testing"
)

func TestCopyTitle(t *testing.T) {
	s := newSandbox(t)
	writeCcSession(t, s.Claude, "", true)
	a := s.app(enable("claude-code", "pi"))
	scan(t, a)
	const title = "重构登录流程 · 第 1–2 轮"
	toPi := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi", Title: title})
	pi := readJSONL(t, toPi.Plan.Target.Path)
	eq(t, pi[1]["type"], any("session_info"))
	eq(t, pi[1]["name"], any(title))
	eq(t, pi[1]["parentId"], nil)
	eq(t, pi[2]["parentId"], pi[1]["id"])
	thread := binding(t, a.Hub, "claude-code", CCSession).Thread
	toCc := carry(t, a, engine.CarryOptions{Thread: thread, Head: binding(t, a.Hub, "claude-code", CCSession).Leaf, To: "claude-code", Title: title})
	var titles []any
	for _, r := range readJSONL(t, toCc.Plan.Target.Path) {
		if r["type"] == "custom-title" {
			titles = append(titles, r["customTitle"])
		}
	}
	jsonEq(t, titles, []any{title})
	scan(t, a)
	eq(t, binding(t, a.Hub, "pi", toPi.Plan.Target.Session).Meta["title"], any(title))
	eq(t, binding(t, a.Hub, "claude-code", toCc.Plan.Target.Session).Meta["title"], any(title))
}
