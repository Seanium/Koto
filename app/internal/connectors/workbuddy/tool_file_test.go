package workbuddy

import (
	"context"
	"os"
	"strings"
	"testing"

	"koto/internal/connectors/claudecode"
	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/hub"
	"koto/internal/planner"
)

func TestToolResultFileHasIndependentBytesAcrossCopies(t *testing.T) {
	for _, edition := range []string{"cn", "international"} {
		for _, withImage := range []bool{false, true} {
			name := edition + "/text"
			if withImage {
				name = edition + "/image-blocks"
			}
			t.Run(name, func(t *testing.T) {
				c := fixture(t, edition)
				in := input(c, "tool-file")
				data := []byte("工具文件原始字节\x00🐙\n")
				blob := in.Hub.PutBlob(data)
				children := []core.Part{{"type": "text", "text": "tool prefix\n"}, {"type": "file", "name": "结果 [1].bin", "mediaType": "application/octet-stream", "blob": blob}}
				if withImage {
					children = append(children, core.Part{"type": "image", "name": "tool.png", "mediaType": "image/png", "blob": in.Hub.PutBlob([]byte("tool image bytes"))})
				}
				in.Projection.Items[2].Parts[0]["parts"] = core.PartsJSON(children)
				p, err := c.Plan(*in)
				if err != nil {
					t.Fatal(err)
				}
				hasLoss := false
				for _, loss := range p.Losses {
					hasLoss = hasLoss || loss.Field == "file" && loss.Kind == "referenced"
				}
				if !hasLoss {
					t.Fatal("file representation change not disclosed")
				}
				if _, err = c.Apply(context.Background(), p); err != nil {
					t.Fatal(err)
				}
				batches := read(t, c, p.Target.Session)
				h := hub.New()
				found := false
				for _, batch := range batches {
					h.Ingest(batch, hub.ConnectorInfo{ID: c.id, Harness: c.harness, Version: Version})
					for _, d := range batch.Drafts {
						for _, part := range d.Parts {
							if part["type"] == "tool_result" {
								for _, child := range core.Parts(part["parts"]) {
									found = found || child["type"] == "file" && core.S(core.Obj(child["blob"])["sha256"]) == core.SHA256Hex(data)
								}
							}
						}
					}
				}
				if !found {
					t.Fatal("tool file reference was not restored to complete material")
				}
				second := fixture(t, edition)
				binding := h.Binding(c.harness, p.Target.Session, c.id)
				projection := planner.Project(h.Path(binding.Leaf), planner.Options{Sink: second.id, SinkHarness: second.harness, Capabilities: Capabilities, Vocabulary: claudecode.Vocabulary{}, Level: "L0", ReadBlob: h.Blob})
				p2, err := second.Plan(engine.PlanInput{Hub: h, Target: engine.DeliveryTarget{Mode: "create", Session: second.NewSessionID("second"), Cwd: second.root}, Projection: projection})
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(p2.Content, c.root) {
					t.Fatal("second copy still depends on source assets")
				}
				if _, err = second.Apply(context.Background(), p2); err != nil {
					t.Fatal(err)
				}
				if err = os.RemoveAll(strings.TrimSuffix(p.Target.Path, ".jsonl") + ".assets"); err != nil {
					t.Fatal(err)
				}
				found, foundImage, foundContext := false, false, false
				for _, batch := range read(t, second, p2.Target.Session) {
					for _, b := range batch.Blobs {
						found = found || core.SHA256Hex(b) == core.SHA256Hex(data)
						foundImage = foundImage || core.SHA256Hex(b) == core.SHA256Hex([]byte("tool image bytes"))
					}
					for _, draft := range batch.Drafts {
						for _, part := range draft.Parts {
							if part["type"] == "tool_result" {
								for _, child := range core.Parts(part["parts"]) {
									foundContext = foundContext || child["type"] == "text" && strings.Contains(core.S(child["text"]), "tool prefix")
								}
							}
						}
					}
				}
				if !found || withImage && !foundImage || !foundContext {
					t.Fatalf("second copy lost independent bytes: losses=%v content=%s", p2.Losses, p2.Content)
				}
			})
		}
	}
}
