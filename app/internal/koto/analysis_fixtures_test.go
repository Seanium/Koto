package koto

import (
	"koto/internal/core"
	"time"
)

func tv(n int) string {
	return time.Date(2026, 10, 15, 16, 0, n, 0, time.UTC).Format("2006-01-02T15:04:05.000Z")
}
func piTurn(id, parent, say string, n int) []any {
	ms := func(s string) int64 { x, _ := time.Parse(time.RFC3339, s); return x.UnixMilli() }
	usage := map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}
	return []any{
		core.O{{K: "type", V: "message"}, {K: "id", V: id + "u"}, {K: "parentId", V: parent}, {K: "timestamp", V: tv(n)}, {K: "message", V: core.O{{K: "role", V: "user"}, {K: "content", V: say}, {K: "timestamp", V: ms(tv(n))}}}},
		core.O{{K: "type", V: "message"}, {K: "id", V: id + "a"}, {K: "parentId", V: id + "u"}, {K: "timestamp", V: tv(n + 1)}, {K: "message", V: core.O{{K: "role", V: "assistant"}, {K: "content", V: []any{map[string]any{"type": "text", "text": "re: " + say}}}, {K: "api", V: "openai-responses"}, {K: "provider", V: "acme"}, {K: "model", V: "acme-large"}, {K: "usage", V: usage}, {K: "stopReason", V: "stop"}, {K: "timestamp", V: ms(tv(n + 1))}}}},
	}
}
