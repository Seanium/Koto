package cursor

import (
	"fmt"
	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/planner"
)

func (c *connector) prepareAttachments(in engine.PlanInput, assetDir string) (engine.PlanInput, []any, error) {
	// Cursor's provider accepts PDF file parts on user messages; text files
	// are expanded into native context. Other files stay complete on disk,
	// using the same checked attachment links as the other native adapters.
	// Validate first so the shared conversion cannot hide missing material.
	var check func([]core.Part) error
	check = func(parts []core.Part) error {
		for _, part := range parts {
			if core.S(part["type"]) == "file" {
				if in.Hub == nil || core.S(core.Obj(part["blob"])["sha256"]) == "" {
					return fmt.Errorf("Cursor requires materialized attachment bytes")
				}
				if _, _, err := c.material(in, part); err != nil {
					return err
				}
			}
			if core.S(part["type"]) == "tool_result" {
				if err := check(core.Parts(part["parts"])); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if in.Projection == nil {
		return in, nil, fmt.Errorf("missing Cursor projection")
	}
	projection := *in.Projection
	projection.Items = append([]planner.Item{}, in.Projection.Items...)
	in.Projection = &projection
	for i, item := range projection.Items {
		if item.Role != "assistant" {
			continue
		}
		projection.Items[i].Parts = append([]core.Part{}, item.Parts...)
		for n, part := range item.Parts {
			if core.S(part["type"]) == "image" {
				copy := core.Part{}
				for key, value := range part {
					copy[key] = value
				}
				copy["type"] = "file"
				projection.Items[i].Parts[n] = copy
			}
		}
	}
	for _, item := range in.Projection.Items {
		if item.Event == nil {
			return in, nil, fmt.Errorf("missing Cursor source event")
		}
		if err := check(item.Parts); err != nil {
			return in, nil, err
		}
	}
	in, attachments := shared.PrepareAttachments(in, assetDir, func(part core.Part, role string) bool {
		raw, media, err := c.material(in, part)
		return err == nil && role == "user" && (media == "application/pdf" || inlineFile(raw, media))
	})

	return in, attachments, nil
}
