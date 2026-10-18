package planner

import (
	"testing"

	"koto/internal/core"
)

func TestKnownAttachmentBytesTakePriorityOverSourceURI(t *testing.T) {
	for _, uri := range []string{"file:///old-session/image.png", "https://example.invalid/image.png"} {
		for _, harness := range []string{"claude-code", "pi", "codex", "dsh", "antigravity", "opencode", "zcode", "workbuddy"} {
			t.Run(harness+"/"+uri, func(t *testing.T) {
				original := core.Part{"type": "image", "mediaType": "image/png", "uri": uri, "blob": core.JsonObject{"sha256": "known"}}
				opts := Options{SinkHarness: harness, Capabilities: core.Capabilities{Write: &core.WriteCaps{Images: true}}, ReadBlob: func(sha string) ([]byte, bool) { return []byte("native image bytes"), sha == "known" }}
				out, changed := normalizeAssets([]core.Part{original}, "user", true, opts, func(_, _, note string) { t.Fatal(note) })
				if !changed || len(out) != 1 || out[0]["type"] != "image" || out[0]["uri"] != nil || core.S(core.Obj(out[0]["blob"])["sha256"]) != "known" {
					t.Fatalf("attachment was not made independent: %#v", out)
				}
				if original["uri"] != uri {
					t.Fatal("projection modified original message")
				}
			})
		}
	}
}

func TestUnreadURIStaysAReference(t *testing.T) {
	for _, reader := range []func(string) ([]byte, bool){nil, func(string) ([]byte, bool) { return nil, false }} {
		part := core.Part{"type": "file", "name": "report.pdf", "uri": "https://example.invalid/report.pdf", "blob": core.JsonObject{"sha256": "unavailable"}}
		losses := 0
		out, _ := normalizeAssets([]core.Part{part}, "user", false, Options{SinkHarness: "pi", ReadBlob: reader}, func(_, kind, _ string) {
			losses++
			if kind != "referenced" {
				t.Fatal("unread remote attachment must remain a reference")
			}
		})
		if len(out) != 1 || out[0]["type"] != "text" || losses != 1 {
			t.Fatalf("unavailable blob was treated as loaded data: %#v", out)
		}
	}
}
