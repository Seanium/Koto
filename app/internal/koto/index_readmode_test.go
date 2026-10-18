package koto

import (
	"context"
	"koto/internal/core"
	"koto/internal/engine"
	"testing"
)

func TestIndexReadModePreservesNativeSummariesAndFullMaterials(t *testing.T) {
	for _, target := range []string{"pi", "dsh", "zcode", "opencode"} {
		t.Run(target, func(t *testing.T) {
			requireCopyAgent(t, "antigravity", target)
			s := newSandbox(t)
			items := materials(t)
			materialSource(t, s, items)
			a := s.app(enable("antigravity", target))
			scan(t, a)
			copied := carry(t, a, engine.CarryOptions{Selector: "antigravity:materials", To: target, Cwd: s.Root})
			c := a.Registry.Get(target)
			refs, err := c.Source.Discover(ctx)
			must(t, err)
			var ref core.NativeRef
			for _, r := range refs {
				if r.Session == copied.Plan.Target.Session {
					ref = r
				}
			}
			ok(t, ref.Session != "")
			type capture struct {
				records []core.BatchRecord
				drafts  []core.DraftEvent
				offset  core.SourceOffset
				blobs   int
			}
			read := func(ctx context.Context) capture {
				var out capture
				must(t, c.Source.Tail(ctx, ref, nil, func(b core.SourceBatch) error {
					out.records = append(out.records, b.Records...)
					out.drafts = append(out.drafts, b.Drafts...)
					out.blobs += len(b.Blobs)
					out.offset = b.Offset
					return nil
				}))
				return out
			}
			full, light := read(ctx), read(engine.WithIndexOnly(ctx))
			eq(t, light.blobs, 0)
			ok(t, full.blobs > 0)
			eq(t, len(light.records), len(full.records))
			eq(t, len(light.drafts), len(full.drafts))
			foff, loff := full.offset, light.offset
			foff.WitnessStable = false
			loff.WitnessStable = false
			jsonEq(t, loff, foff)
			for i, f := range full.records {
				l := light.records[i]
				eq(t, len(l.Raw), 0)
				f.Raw = nil
				jsonEq(t, l, f)
			}
			summary := func(ds []core.DraftEvent) []any {
				out := []any{}
				for _, d := range ds {
					var ps []any
					if d.Kind == "message" && d.Actor == "user" {
						for _, p := range d.Parts {
							v := core.JsonObject{"type": p["type"]}
							if p["type"] == "text" {
								v["text"] = p["text"]
							}
							ps = append(ps, v)
						}
					}
					out = append(out, []any{d.NativeID, d.Kind, d.Actor, d.Time, d.Parent, d.Replaces, core.Obj(d.Attrs)["model"], ps})
				}
				return out
			}
			jsonEq(t, summary(light.drafts), summary(full.drafts))
			// A light request must not mutate the connector or poison later full reads.
			again := read(ctx)
			jsonEq(t, again.drafts, full.drafts)
			eq(t, again.blobs, full.blobs)
			assertMaterialBytes(t, a, target, ref.Session, items)
		})
	}
}
