package cursor

import (
	"context"
	"reflect"
	"testing"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/hub"
	"koto/internal/planner"
)

func assertIndexRead(t *testing.T, source engine.Source, ref core.NativeRef) {
	t.Helper()
	read := func(ctx context.Context) core.SourceBatch {
		var b core.SourceBatch
		if err := source.Tail(ctx, ref, nil, func(x core.SourceBatch) error { b = x; return nil }); err != nil {
			t.Fatal(err)
		}
		return b
	}
	full, light := read(context.Background()), read(engine.WithIndexOnly(context.Background()))
	if len(full.Blobs) == 0 || len(light.Blobs) != 0 {
		t.Fatal("index kept attachment bytes")
	}
	if !reflect.DeepEqual(full.Meta, light.Meta) || !reflect.DeepEqual(full.Offset, light.Offset) || !reflect.DeepEqual(full.Lineage, light.Lineage) {
		t.Fatal("list metadata changed")
	}
	if len(full.Drafts) != len(light.Drafts) || len(full.Records) != len(light.Records) {
		t.Fatal("list record count changed")
	}
	for i, d := range full.Drafts {
		l := light.Drafts[i]
		if d.NativeID != l.NativeID || d.Kind != l.Kind || d.Actor != l.Actor || !reflect.DeepEqual(d.Time, l.Time) || !reflect.DeepEqual(d.Attrs, l.Attrs) {
			t.Fatal("event metadata changed")
		}
		if d.Actor == "user" && !reflect.DeepEqual(indexPrompt(d.Parts), indexPrompt(l.Parts)) {
			t.Fatalf("prompt %d changed: %#v vs %#v", i, indexPrompt(d.Parts), indexPrompt(l.Parts))
		}
	}
	for i, r := range full.Records {
		r.Raw = nil
		if !reflect.DeepEqual(r, light.Records[i]) {
			t.Fatal("record identity changed")
		}
	}
	if after := read(context.Background()); !reflect.DeepEqual(full, after) {
		t.Fatal("index altered subsequent full material read")
	}
}

func TestNativeIndexSummaryMatchesFullWithoutMaterial(t *testing.T) {
	c, f := setup(t)
	assertIndexRead(t, c, core.NativeRef{Session: f.ID})
}

func TestCLIIndexExactFileExpansionWithoutAttachmentReads(t *testing.T) {
	for _, authored := range []string{"", "Normal prompt", "[Attached file: note.txt]\nFILE_BYTES", "[Attached file: note.txt]\nOrdinary text"} {
		t.Run(authored, func(t *testing.T) {
			c := NewCLI(CLIOptions{Root: t.TempDir()}).Source.(*cliConnector)
			h := hub.New()
			u := projected("user", authored)
			if authored == "" {
				u.Parts = nil
			}
			for _, name := range []string{"note.txt", "same-content.txt"} {
				u.Parts = append(u.Parts, core.Part{"type": "file", "name": name, "mediaType": "text/plain", "blob": h.PutBlob([]byte("FILE_BYTES"))})
			}
			p, err := c.Plan(engine.PlanInput{Hub: h, Projection: &planner.Projection{Items: []planner.Item{u, projected("assistant", "reply")}}, Target: engine.DeliveryTarget{Mode: "create", Session: c.NewSessionID("index"), Cwd: c.root}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = c.Apply(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			assertIndexRead(t, c, core.NativeRef{Session: p.Target.Session, Path: p.Target.Path})
		})
	}
}

func indexPrompt(parts []core.Part) []any {
	visible, first := false, ""
	for _, p := range parts {
		if p["type"] == "text" || p["type"] == "image" || p["type"] == "file" || p["type"] == "opaque" {
			visible = true
		}
	}
	for _, p := range parts {
		if p["type"] == "text" {
			first = core.S(p["text"])
			break
		}
	}
	return []any{visible, first}
}
