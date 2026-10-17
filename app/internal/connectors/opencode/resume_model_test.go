package opencode

import (
	"context"
	"encoding/json"
	"testing"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/planner"
)

func TestImportedUserSelectsTargetModelAndPreservesAssistantModel(t *testing.T) {
	for _, source := range []core.JsonObject{nil, {"provider": "foreign-provider", "id": "foreign-model"}} {
		c := fixture(t)
		user := &core.Event{ID: "u", Attrs: core.JsonObject{"model": source}}
		assistant := &core.Event{ID: "a", Attrs: core.JsonObject{"model": core.JsonObject{"provider": "original-provider", "id": "original-model"}}}
		p, err := c.Plan(engine.PlanInput{Target: engine.DeliveryTarget{Mode: "create", Session: c.NewSessionID("model"), Cwd: c.root}, Projection: &planner.Projection{Items: []planner.Item{
			{Key: "u", Event: user, Role: "user", Parts: []core.Part{{"type": "text", "text": "Remember the code."}}},
			{Key: "a", Event: assistant, Role: "assistant", Parts: []core.Part{{"type": "text", "text": "Remembered."}}},
		}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = c.Apply(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		db, err := c.open(context.Background(), false)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := db.Query(`SELECT data FROM message WHERE session_id=? ORDER BY time_created`, p.Target.Session)
		if err != nil {
			db.Close()
			t.Fatal(err)
		}
		var messages []core.JsonObject
		for rows.Next() {
			var raw string
			var message core.JsonObject
			if err = rows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal([]byte(raw), &message); err != nil {
				t.Fatal(err)
			}
			messages = append(messages, message)
		}
		err = rows.Err()
		rows.Close()
		db.Close()
		if err != nil || len(messages) != 2 {
			t.Fatalf("native readback: %d messages, %v", len(messages), err)
		}
		model := core.Obj(messages[0]["model"])
		if core.S(model["providerID"]) != "fixture" || core.S(model["modelID"]) != "fixture" {
			t.Fatalf("invalid TUI continuation model: %#v", model)
		}
		if core.S(messages[1]["providerID"]) != "original-provider" || core.S(messages[1]["modelID"]) != "original-model" {
			t.Fatalf("assistant model provenance changed: %#v", messages[1])
		}
	}
}
