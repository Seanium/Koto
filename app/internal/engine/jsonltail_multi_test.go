package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/core"
)

type multiFixtureDecoder struct{ bad string }

func (multiFixtureDecoder) FormatVersion() string         { return "multi-test-v1" }
func (multiFixtureDecoder) InitialState() core.JsonObject { return core.JsonObject{} }
func (multiFixtureDecoder) Decode(any, []byte, *DecodeContext) DecodeOut {
	panic("multi-event decoder must not use the single-event path")
}
func (d multiFixtureDecoder) DecodeMany(v any, _ []byte, c *DecodeContext) ([]DecodeOut, error) {
	if d.bad == "panic" {
		panic("bad record")
	}
	if d.bad == "error" {
		return nil, fmt.Errorf("invalid content")
	}
	var out []DecodeOut
	for i, text := range core.Arr(v) {
		id := fmt.Sprintf("record-%d-event-%d", c.Index, i)
		if d.bad == "duplicate" {
			id = "same"
		}
		out = append(out, DecodeOut{NativeID: id, Draft: &core.DraftEvent{NativeID: id, Kind: "message", Actor: "user", Parts: []core.Part{{"type": "text", "text": text}}}})
	}
	return out, nil
}

func TestMultiRecordCheckpointsAndIncremental(t *testing.T) {
	file := filepath.Join(t.TempDir(), "multi.jsonl")
	first := "[\"one\",\"two\"]\n"
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(first + "[\"incomplete")
	var saved core.SourceOffset
	var records []core.BatchRecord
	var drafts []core.DraftEvent
	rewritten := false
	collect := func(b core.SourceBatch) error {
		saved = b.Offset
		records = append(records, b.Records...)
		drafts = append(drafts, b.Drafts...)
		rewritten = rewritten || b.Rewritten != nil
		return nil
	}
	read := func(from *core.SourceOffset) {
		t.Helper()
		if err := TailJSONL(context.Background(), core.NativeRef{Path: file}, from, multiFixtureDecoder{}, collect, 1, false); err != nil {
			t.Fatal(err)
		}
	}
	read(nil)
	sha := core.SHA256Hex(strings.TrimSuffix(first, "\n"))
	if len(records) != 2 || len(drafts) != 2 || saved.Records != 1 || saved.Position.ByteOff != int64(len(first)) || saved.PrefixHash != core.ChainPrefix(core.ZeroPrefix, sha) {
		t.Fatalf("physical record and logical events lost alignment: %+v %d %d", saved, len(records), len(drafts))
	}
	for _, record := range records {
		if record.SHA256 != sha || record.Index != 0 || record.Offset == nil || *record.Offset != 0 || string(record.Raw) != strings.TrimSuffix(first, "\n") {
			t.Fatalf("logical event lost its physical source record: %+v", record)
		}
	}
	data, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	var restored core.SourceOffset
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	write(first + "[\"three\",\"four\"]\n")
	records, drafts = nil, nil
	read(&restored)
	if len(drafts) != 2 || drafts[0].NativeID != "record-1-event-0" || saved.Records != 2 {
		t.Fatalf("incremental read repeated or lost events: %+v", drafts)
	}
	records, drafts = nil, nil
	restored = saved
	read(&restored)
	if len(records) != 0 || len(drafts) != 0 {
		t.Fatal("unchanged source emitted events")
	}
	write("[\"edited\",\"two\"]\n")
	read(&restored)
	if !rewritten || saved.Records != 1 || len(drafts) != 2 {
		t.Fatal("rewritten physical record was not rebuilt")
	}
}

func TestMultiInvalidRecordsFailAtomically(t *testing.T) {
	for _, kind := range []string{"malformed", "error", "panic", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "bad.jsonl")
			body := "[\"one\",\"two\"]\n"
			if kind == "malformed" {
				body = "{bad}\n"
			}
			if err := os.WriteFile(file, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			yielded := false
			err := TailJSONL(context.Background(), core.NativeRef{Path: file}, nil, multiFixtureDecoder{bad: kind}, func(core.SourceBatch) error { yielded = true; return nil }, 1, false)
			if err == nil || yielded {
				t.Fatalf("invalid record committed: %v, yielded=%t", err, yielded)
			}
		})
	}
}
