package hub

import (
	"testing"

	"koto/internal/core"
)

func TestBlobReferencesIncludeTypedNestedToolParts(t *testing.T) {
	for _, typed := range []bool{false, true} {
		child := []core.Part{{"type": "file", "blob": core.JsonObject{"sha256": "file-hash"}}, {"type": "image", "blob": core.JsonObject{"sha256": "image-hash"}}}
		var nested any = child
		if !typed {
			nested = core.PartsJSON(child)
		}
		parts := []core.Part{{"type": "tool_result", "parts": nested}}
		found := CollectBlobRefs(core.PartsJSON(parts))
		if len(found) != 2 || !found["file-hash"] || !found["image-hash"] {
			t.Fatalf("typed=%v: %v", typed, found)
		}
	}
}
