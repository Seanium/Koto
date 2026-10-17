package hub

import (
	"testing"

	"koto/internal/core"
)

func TestPendingContextLifetime(t *testing.T) {
	h := New()
	ref := core.NativeRef{Harness: "claude-code", Session: "synthetic"}
	key := sessKey{ref.Harness, ref.Session}
	conn := ConnectorInfo{ID: "claude-code", Harness: "claude-code"}
	batch := core.SourceBatch{Ref: ref,
		Records: []core.BatchRecord{{NativeID: "context", SHA256: "context", Index: 0, Bytes: 100, Raw: []byte("not retained")}},
		Drafts:  []core.DraftEvent{{NativeID: "context", Parent: core.NativeParent("future-prompt"), Kind: "context", Actor: "harness"}},
	}
	h.Ingest(batch, conn)
	for range 10 {
		h.Ingest(core.SourceBatch{Ref: ref}, conn)
	}
	if len(h.pending[key]) != 1 || h.pending[key][0].record.Raw != nil {
		t.Fatal("incomplete tails must retain only one decoded record, without raw bytes")
	}
	retained := h.RetainedBytes()
	h.Ingest(core.SourceBatch{Ref: ref, Rewritten: &struct{ AtRecord int }{0}}, conn)
	if len(h.pending) != 0 || h.RetainedBytes() >= retained {
		t.Fatal("a rewritten source must release its waiting records")
	}
	h.Ingest(batch, conn)
	h.Close()
	if h.pending != nil || h.RetainedBytes() != 0 {
		t.Fatal("closing a working copy must release pending state")
	}
}
