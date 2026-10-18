package workbuddy

import (
	"context"
	"os"
	"reflect"
	"testing"

	"koto/internal/core"
	"koto/internal/engine"
)

func TestIndexReadKeepsSummaryAndDoesNotMaterializeAttachments(t *testing.T) {
	for _, edition := range []string{"cn", "international"} {
		t.Run(edition, func(t *testing.T) {
			c := fixture(t, edition)
			in := input(c, "index-only")
			plan, err := c.Plan(*in)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = c.Apply(context.Background(), plan); err != nil {
				t.Fatal(err)
			}
			refs, err := c.Discover(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var ref core.NativeRef
			for _, r := range refs {
				if r.Session == plan.Target.Session {
					ref = r
				}
			}
			if ref.Session == "" {
				t.Fatal("missing fixture")
			}
			readMode := func(ctx context.Context) ([]core.SourceBatch, error) {
				var out []core.SourceBatch
				err := c.Tail(ctx, ref, nil, func(b core.SourceBatch) error { out = append(out, b); return nil })
				return out, err
			}
			full, err := readMode(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			light, err := readMode(engine.WithIndexOnly(context.Background()))
			if err != nil {
				t.Fatal(err)
			}
			if len(full) != len(light) {
				t.Fatal("batch boundaries changed")
			}
			blobs := 0
			for i, b := range full {
				l := light[i]
				blobs += len(b.Blobs)
				if len(l.Blobs) != 0 || !reflect.DeepEqual(b.Meta, l.Meta) || !reflect.DeepEqual(b.Offset, l.Offset) || len(b.Drafts) != len(l.Drafts) {
					t.Fatal("index mode changed native summary or retained material")
				}
				for j, r := range b.Records {
					x := l.Records[j]
					if r.NativeID != x.NativeID || r.SHA256 != x.SHA256 || r.Index != x.Index || len(x.Raw) != 0 {
						t.Fatal("index record identity changed or raw bytes retained")
					}
				}
				for j, d := range b.Drafts {
					x := l.Drafts[j]
					if d.NativeID != x.NativeID || d.Kind != x.Kind || d.Actor != x.Actor || !reflect.DeepEqual(d.Time, x.Time) || !reflect.DeepEqual(d.Attrs, x.Attrs) {
						t.Fatal("index draft summary changed")
					}
					if d.Actor == "user" {
						shape := func(parts []core.Part) []string {
							var out []string
							for _, p := range parts {
								value := core.S(p["type"])
								if value == "text" {
									value += ":" + core.S(p["text"])
								}
								out = append(out, value)
							}
							return out
						}
						if !reflect.DeepEqual(shape(d.Parts), shape(x.Parts)) {
							t.Fatal("user prompt or turn classification changed")
						}
					}
				}
			}
			if blobs == 0 {
				t.Fatal("fixture did not exercise material")
			}
			for _, raw := range core.Arr(plan.Extra["attachments"]) {
				if err = os.Remove(core.S(core.Obj(raw)["path"])); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = readMode(engine.WithIndexOnly(context.Background())); err != nil {
				t.Fatalf("list unnecessarily opened missing attachment: %v", err)
			}
			if _, err = readMode(context.Background()); err == nil {
				t.Fatal("full read stopped validating missing attachment after index read")
			}
		})
	}
}
