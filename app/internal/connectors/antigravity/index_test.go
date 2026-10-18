package antigravity

import (
	"context"
	"reflect"
	"testing"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/hub"
	"koto/internal/planner"
)

func TestIndexModePreservesNativeSummaryWithoutBlobs(t *testing.T) {
	c := New(Options{Root: t.TempDir()}).Source.(*agy)
	h := hub.New()
	image := []byte("index image exact bytes")
	file := []byte("index attachment 中文")
	tm := float64(1790000000000)
	event := &core.Event{ID: "q", Time: &tm}
	in := engine.PlanInput{Hub: h, Target: engine.DeliveryTarget{Mode: "create", Session: c.NewSessionID("index"), Cwd: c.root, Title: "summary"}, Projection: &planner.Projection{Items: []planner.Item{
		{Key: "q", Event: event, Role: "user", Parts: []core.Part{{"type": "text", "text": "prompt 中文"}, {"type": "image", "mediaType": "image/png", "blob": h.PutBlob(image)}, {"type": "file", "name": "context.txt", "mediaType": "text/plain", "blob": h.PutBlob(file)}}},
		{Key: "a", Event: event, Role: "assistant", Parts: []core.Part{{"type": "text", "text": "answer"}, {"type": "file", "name": "report.txt", "blob": h.PutBlob(file)}}},
	}}}
	plan, err := c.Plan(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	refs, err := c.Discover(context.Background())
	if err != nil || len(refs) != 1 {
		t.Fatal(refs, err)
	}
	read := func(ctx context.Context) core.SourceBatch {
		var out core.SourceBatch
		if err := c.Tail(ctx, refs[0], nil, func(b core.SourceBatch) error { out = b; return nil }); err != nil {
			t.Fatal(err)
		}
		return out
	}
	full := read(context.Background())
	light := read(engine.WithIndexOnly(context.Background()))
	if len(full.Blobs) == 0 || len(light.Blobs) != 0 || !reflect.DeepEqual(full.Records, light.Records) || !reflect.DeepEqual(full.Offset, light.Offset) || !reflect.DeepEqual(full.Meta, light.Meta) || len(full.Drafts) != len(light.Drafts) {
		t.Fatal("index changed native list metadata or kept material")
	}
	for i, d := range full.Drafts {
		l := light.Drafts[i]
		if d.Kind != l.Kind || d.Actor != l.Actor || !reflect.DeepEqual(d.Time, l.Time) || !reflect.DeepEqual(d.Attrs, l.Attrs) {
			t.Fatal("list event semantics changed")
		}
		if d.Actor == "user" {
			for j, p := range d.Parts {
				if p["type"] != l.Parts[j]["type"] || p["text"] != l.Parts[j]["text"] {
					t.Fatal("user prompt/turn changed")
				}
			}
		} else if len(l.Parts) != 0 {
			t.Fatal("index kept non-user body")
		}
	}
	after := read(context.Background())
	if !reflect.DeepEqual(full.Blobs, after.Blobs) || !reflect.DeepEqual(full.Drafts, after.Drafts) {
		t.Fatal("index mode leaked into subsequent full read")
	}
}
