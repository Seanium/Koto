package planner

import (
	"koto/internal/core"
	"testing"
)

func TestAttachmentSnapshotUsesLatestContextEdit(t *testing.T) {
	for _, removed := range []bool{false, true} {
		question := &core.Event{ID: "q", Kind: "message", Actor: "user", Parts: []core.Part{{"type": "text", "text": "question"}}, Provenance: core.Provenance{Source: core.Source{Harness: "claude-code"}}}
		attachment := &core.Event{ID: "f", Parents: []string{"q"}, Kind: "attachment", Actor: "user", Parts: []core.Part{{"type": "text", "text": "original snapshot"}}}
		parts := []core.Part{{"type": "text", "text": "edited snapshot"}}
		if removed {
			parts = nil
		}
		edit := &core.Event{ID: "edit", Kind: "x-context-edit", Replaces: &core.Replaces{To: "f"}, Parts: parts}
		p := Project([]*core.Event{question, attachment, edit}, Options{SinkHarness: "claude-code", Level: "L0"})
		if len(p.Items) != 1 {
			t.Fatal("extra human turn", p.Items)
		}
		want := "question\nedited snapshot"
		if removed {
			want = "question"
		}
		if textOf(p.Items[0].Parts) != want {
			t.Fatal(textOf(p.Items[0].Parts))
		}
		if !removed && p.Items[0].Verbatim {
			t.Fatal("raw original question omitted attachment edit")
		}
	}
}

func TestPartialAttachmentReportsSnapshotBoundary(t *testing.T) {
	q := &core.Event{ID: "q", Kind: "message", Actor: "user", Parts: []core.Part{{"type": "text", "text": "question"}}}
	note := "[File snapshot: note.txt, lines 5–6 of 20. Only this recorded range is included.]"
	f := &core.Event{ID: "f", Kind: "attachment", Actor: "user", Parents: []string{"q"}, Parts: []core.Part{{"type": "text", "text": note}, {"type": "file", "name": "note.txt", "mediaType": "text/plain", "blob": core.JsonObject{"sha256": "snapshot"}}}, Attrs: core.JsonObject{"attachmentPartial": note}}
	p := Project([]*core.Event{q, f}, Options{Level: "L1"})
	if len(p.Items) != 1 || len(p.Items[0].Parts) != 3 || len(p.Losses) != 1 || p.Losses[0].Note != note || p.Losses[0].Kind != "approximated" {
		t.Fatalf("partial snapshot boundary lost: %+v", p)
	}
}
