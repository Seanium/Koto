package codex

import (
	"koto/internal/core"
	"koto/internal/engine"
	"testing"
)

func TestCreatorIdentitySurvivesModelProviderChanges(t *testing.T) {
	d := Decoder{}
	ctx := &engine.DecodeContext{State: d.InitialState()}
	out := d.Decode(core.JsonObject{"type": "session_meta", "payload": core.JsonObject{"id": "id", "creator_account_id": "created-by", "creator_user_id": "user-1", "model_provider": "original"}}, nil, ctx)
	identity := core.ReadIdentity(out.Meta)
	if len(identity.Claims) != 1 || identity.Claims[0].ID != "created-by" || identity.Claims[0].Scope != "creator" || out.Meta["creatorUserId"] != "user-1" {
		t.Fatal(out.Meta)
	}
	for i, p := range []string{"first", "second"} {
		ctx.Index++
		out = d.Decode(core.JsonObject{"type": "turn_context", "payload": core.JsonObject{"model": "model", "model_provider": p}}, nil, ctx)
		identity = core.ReadIdentity(out.Meta)
		if len(identity.Claims) != 1 || identity.Claims[0].ID != "created-by" || len(identity.Providers) != i+2 {
			t.Fatal(identity)
		}
	}
}
func TestProviderAloneDoesNotIdentifyCodexAccount(t *testing.T) {
	d := Decoder{}
	ctx := &engine.DecodeContext{State: d.InitialState()}
	out := d.Decode(core.JsonObject{"type": "session_meta", "payload": core.JsonObject{"id": "id", "model_provider": "openai"}}, nil, ctx)
	if core.ReadIdentity(out.Meta).Status != "unknown" {
		t.Fatal(out.Meta)
	}
}
