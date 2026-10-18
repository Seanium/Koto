package store

import (
	"strings"
	"testing"

	"koto/internal/core"
)

func TestFirstPromptUsesExplicitNativeDisplayText(t *testing.T) {
	for _, tc := range []struct {
		name  string
		attrs core.JsonObject
		want  string
	}{
		{"native", core.JsonObject{"displayText": "  只显示\n原始提问  "}, "只显示 原始提问"},
		{"empty-native", core.JsonObject{"displayText": ""}, ""},
		{"bounded-native", core.JsonObject{"displayText": strings.Repeat("字", 201)}, strings.Repeat("字", 200)},
		{"without-hint", nil, "<native_context>keep for model</native_context> user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := core.DraftEvent{Kind: "message", Actor: "user", Attrs: tc.attrs, Parts: []core.Part{{"type": "text", "text": "<native_context>keep for model</native_context> user"}, {"type": "image", "blob": core.JsonObject{"sha256": "image"}}}}
			before := core.ToJSON(d)
			var row SessionRow
			note(&row, &d)
			if row.FirstPrompt != tc.want || row.Turns != 1 || core.ToJSON(d) != before {
				t.Fatalf("row=%+v; native event changed=%v", row, core.ToJSON(d) != before)
			}
		})
	}
}
