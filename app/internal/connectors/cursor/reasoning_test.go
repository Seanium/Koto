package cursor

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"koto/internal/connectors/claudecode"
	"koto/internal/connectors/codex"
	"koto/internal/connectors/pi"
	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/hub"
	"koto/internal/planner"
)

func TestNativeRedactedReasoningPreservesWireAndCrossAgentLoss(t *testing.T) {
	// Ordinary Cursor 3.25.9 stores this SDK object in its model checkpoint.
	// The data here is synthetic; no readable reasoning is inferred from it.
	raw := core.JsonObject{"type": "redacted-reasoning", "data": "opaque-test-reasoning", "providerOptions": core.JsonObject{"cursor": core.JsonObject{"modelName": "cursor-grok-4.6-medium"}}}
	for _, variant := range []string{"ide", "cli"} {
		t.Run(variant, func(t *testing.T) {
			base, _ := setup(t)
			ec := New(Options{Root: base.root})
			if variant == "cli" {
				ec = NewCLI(CLIOptions{Root: t.TempDir()})
			}
			caps := ec.Capabilities
			h := hub.New()
			defer h.Close()
			parts, err := modelParts([]any{raw, core.JsonObject{"type": "text", "text": "visible answer"}}, h.PutBlob)
			if err != nil {
				t.Fatal(err)
			}
			user := &core.Event{ID: "u", Kind: "message", Actor: "user", Parts: []core.Part{{"type": "text", "text": "native question"}}}
			answer := &core.Event{ID: "a", Kind: "message", Actor: "assistant", Parts: parts, Attrs: core.JsonObject{"model": map[string]any{"api": modelAPI}, "stopReason": "stop"}}
			user.Provenance.Source.Harness = "cursor"
			answer.Provenance.Source.Harness = "cursor"
			disabled := false
			for round := 0; round < 3; round++ {
				pr := planner.Project([]*core.Event{user, answer}, planner.Options{Sink: ec.ID, SinkHarness: "cursor", Capabilities: caps, Level: "L1", DeferIncomplete: &disabled})
				if len(pr.Losses) != 0 {
					t.Fatal(pr.Losses)
				}
				p, err := ec.Sink.Plan(engine.PlanInput{Projection: pr, Hub: h, Target: engine.DeliveryTarget{Mode: "create", Session: ec.Sink.NewSessionID(variant + string(rune('0'+round))), Cwd: ec.Root}})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = ec.Sink.Apply(context.Background(), p); err != nil {
					t.Fatal(err)
				}
				var events []core.DraftEvent
				var blobs [][]byte
				refs, err := ec.Source.Discover(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, ref := range refs {
					if ref.Session == p.Target.Session {
						found = true
						err = ec.Source.Tail(context.Background(), ref, nil, func(b core.SourceBatch) error {
							events = append(events, b.Drafts...)
							blobs = append(blobs, b.Blobs...)
							return nil
						})
					}
				}
				if !found {
					t.Fatal("target missing")
				}
				if err != nil || len(events) != 2 {
					t.Fatal(len(events), err)
				}
				for _, b := range blobs {
					h.PutBlob(b)
				}
				if len(events[1].Parts) != 2 || events[1].Parts[0]["text"] != nil {
					t.Fatal("redaction fabricated plaintext", events[1].Parts)
				}
				sealed := core.Obj(events[1].Parts[0]["sealed"])
				b, ok := h.Blob(core.S(core.Obj(sealed["blob"])["sha256"]))
				want, _ := json.Marshal(raw)
				if !ok || !bytes.Equal(b, want) {
					t.Fatal("native reasoning object changed")
				}
				answer.Parts, answer.Attrs = events[1].Parts, events[1].Attrs
			}
			for _, target := range []*engine.Connector{claudecode.New(claudecode.Options{Root: t.TempDir()}), pi.New(pi.Options{Root: t.TempDir()}), codex.New(codex.Options{Home: t.TempDir()})} {
				pr := planner.Project([]*core.Event{user, answer}, planner.Options{Sink: target.ID, SinkHarness: target.Harness, Capabilities: target.Capabilities, Level: "L1", DeferIncomplete: &disabled})
				if target.Harness == "pi" {
					// Pi stores source-API signatures for its own model transformer.
					// It must retain the sealed object, never expose it as plaintext.
					if len(pr.Losses) != 0 || len(pr.Items) != 2 || len(pr.Items[1].Parts) != 2 || pr.Items[1].Parts[0]["text"] != nil || core.S(core.Obj(pr.Items[1].Parts[0]["sealed"])["by"]) != modelAPI {
						t.Fatal("Pi passthrough changed the opaque object", pr)
					}
					continue
				}
				if len(pr.Losses) != 1 || pr.Losses[0].Field != "reasoning" || pr.Losses[0].Kind != "dropped" {
					t.Fatalf("%s: missing explicit encrypted reasoning loss: %+v", target.Harness, pr.Losses)
				}
				if len(pr.Items) != 2 || len(pr.Items[1].Parts) != 1 || pr.Items[1].Parts[0]["text"] != "visible answer" || strings.Contains(core.ToJSON(pr.Items), "opaque-test") {
					t.Fatalf("%s: encrypted data leaked or visible content lost", target.Harness)
				}
			}
		})
	}
}

func TestRedactedReasoningIndexAndValidation(t *testing.T) {
	raw := []byte(`{"role":"assistant","content":[{"type":"redacted-reasoning","data":"opaque","providerOptions":{"cursor":{"modelName":"cursor-grok-4.6-medium"}}},{"type":"text","text":"visible"}]}`)
	role, parts, err := indexModelMessage(raw)
	if err != nil || role != "assistant" || len(parts) != 0 {
		t.Fatal(role, parts, err)
	}
	if _, err = modelParts([]any{core.JsonObject{"type": "redacted-reasoning", "data": 17}}, hub.New().PutBlob); err == nil {
		t.Fatal("invalid redacted data accepted")
	}
}
