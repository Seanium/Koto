package hub

import (
	"koto/internal/core"
)

type pendingRecord struct {
	record core.BatchRecord
	draft  *core.DraftEvent
}

// Claude Code can flush attachment context before the prompt it belongs to.
// Resolve records in parent order, keeping their original file positions for
// residuals and checkpoints. A context whose parent has not been flushed yet
// waits for the next batch instead of becoming a disconnected root.
func (h *Hub) causalRecords(batch core.SourceBatch, drafts map[string]*core.DraftEvent, blobs map[string][]byte) []core.BatchRecord {
	if batch.Ref.Harness != "claude-code" {
		return batch.Records
	}
	key := sessKey{batch.Ref.Harness, batch.Ref.Session, batch.Ref.Connector}
	if batch.Rewritten != nil || (len(batch.Records) > 0 && batch.Records[0].Index == 0) {
		delete(h.pending, key)
	}
	previous := h.pending[key]
	records := make([]core.BatchRecord, 0, len(previous)+len(batch.Records))
	for _, p := range previous {
		records = append(records, p.record)
		if p.draft != nil {
			drafts[p.record.NativeID] = p.draft
		}
	}
	records = append(records, batch.Records...)
	byID := make(map[string]int, len(records))
	for i, r := range records {
		if r.NativeID != "" {
			byID[r.NativeID] = i
		}
	}
	state := make([]uint8, len(records)) // unseen, visiting, ready, waiting
	ordered := make([]core.BatchRecord, 0, len(records))
	var visit func(int) bool
	visit = func(i int) bool {
		if state[i] != 0 {
			return state[i] == 2
		}
		state[i] = 1
		r := records[i]
		d := drafts[r.NativeID]
		parent := r.Alias
		if d != nil {
			parent = d.Parent
		}
		if parent != nil && !parent.Null && parent.Native != "" && parent.Event == "" && (parent.Harness == "" || parent.Harness == key.harness) && (parent.Session == "" || parent.Session == key.session) {
			if at, found := byID[parent.Native]; found {
				if !visit(at) {
					state[i] = 3
					return false
				}
			} else if d != nil && d.Kind == "context" && d.Actor == "harness" {
				if _, exists := h.idmap[nativeKey{key.harness, key.session, parent.Native, key.connector}]; !exists {
					state[i] = 3
					return false
				}
			}
		}
		state[i] = 2
		ordered = append(ordered, r)
		return true
	}
	for i := range records {
		visit(i)
	}
	delete(h.pending, key)
	for i, r := range records {
		if state[i] == 2 {
			continue
		}
		d := drafts[r.NativeID]
		// Raw bytes can be re-read from the source; keep only the decoded event.
		r.Raw = nil
		h.pending[key] = append(h.pending[key], pendingRecord{r, d})
		if d != nil {
			for sha := range CollectBlobRefs(core.PartsJSON(d.Parts)) {
				if b, ok := blobs[sha]; ok {
					h.PutBlob(b)
				}
			}
		}
	}
	return ordered
}
