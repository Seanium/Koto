package koto

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/core"
)

func TestIdentityModesCopyByTargetStorage(t *testing.T) {
	s := newSandbox(t)
	ccFile := writeCcSession(t, s.Claude, "", true)
	piFile := writePiSession(t, s.Pi, "")
	codexRecords := codexRecords(t, "")
	// A reliable creation account is historical evidence, not a destination
	// login. Deliberately reuse the WorkBuddy owner ID across products.
	core.Obj(codexRecords[0]["payload"])["creator_account_id"] = "fixture-user"
	codexFile := writeCodexRollout(t, s.Codex, "", codexRecords)
	originals := map[string]string{}
	for _, file := range []string{ccFile, piFile, codexFile} {
		b, err := os.ReadFile(file)
		must(t, err)
		originals[file] = core.SHA256Hex(b)
	}
	a := s.app(enable("claude-code", "pi", "codex", "workbuddy"))
	api := newAPI(a)
	eq(t, len(api.Reindex(ctx).Errors), 0)
	sources := []struct{ name, selector string }{
		{"unknown", "claude-code:" + CCSession},
		{"api-provider", "pi:" + PISession},
		{"creation-account", "codex:" + CodexThread},
	}
	// A real account-backed native result becomes the fourth source, without
	// using authentication files or pretending the synthetic owner is logged in.
	seed, err := api.Carry(ctx, CarryRequest{Selector: sources[0].selector, To: "workbuddy", Cwd: s.Root})
	must(t, err)
	ok(t, seed.Applied)
	eq(t, seed.Plan.Target.Account, "fixture-user")
	accountSource := core.SessionSelector(seed.Plan.Target.Profile, "workbuddy", seed.Plan.Target.Session)
	sources = append(sources, struct{ name, selector string }{"storage-account", accountSource})
	accountBefore := workBuddySessionSnapshot(t, seed.Plan.Target.Path, seed.Plan.Target.Session)
	for _, source := range sources {
		for _, target := range []string{"pi", "workbuddy"} {
			t.Run(source.name+"/"+target, func(t *testing.T) {
				result, err := api.Carry(ctx, CarryRequest{Selector: source.selector, To: target, Cwd: s.Root})
				must(t, err)
				ok(t, result.Applied, "source identity must not gate content copy")
				if target == "pi" {
					eq(t, result.Plan.Target.Account, "", "accountless storage must not inherit source owner")
				} else {
					eq(t, result.Plan.Target.Account, "fixture-user", "unique valid destination owner defaults regardless of source identity")
				}
				eq(t, result.Plan.Target.Profile, a.Registry.Get(target).ID)
				v, err := a.OpenThread(ctx, core.SessionSelector(result.Plan.Target.Profile, target, result.Plan.Target.Session))
				must(t, err)
				b := v.Hub.Binding(target, result.Plan.Target.Session, result.Plan.Target.Profile)
				ok(t, b != nil && b.Leaf != "", "copied result is actually readable")
			})
		}
	}
	// Cross-root API storage is explicit, not inferred from the source account.
	otherRoot := filepath.Join(s.Root, "pi API target [中文]")
	must(t, os.MkdirAll(otherRoot, 0700))
	profile, err := a.AddRoot("pi", otherRoot)
	must(t, err)
	result, err := api.Carry(ctx, CarryRequest{Selector: accountSource, To: "pi", TargetProfile: profile.ID, Cwd: s.Root})
	must(t, err)
	ok(t, result.Applied)
	eq(t, result.Plan.Target.Root, otherRoot)
	eq(t, result.Plan.Target.Account, "")
	// More than one owner really exists in this native destination. A source's
	// matching ID in a different product does not authorize selecting one.
	db, err := sql.Open("sqlite", filepath.Join(s.Root, "workbuddy", "workbuddy.db"))
	must(t, err)
	_, err = db.Exec(`INSERT INTO sessions(id,cwd,user_id,created_at,updated_at,deleted_at) VALUES('second-owner',?,'second-user',1,1,1)`, s.Root)
	must(t, err)
	must(t, db.Close())
	for _, source := range sources[:3] {
		_, err = api.Carry(ctx, CarryRequest{Selector: source.selector, To: "workbuddy", Cwd: s.Root})
		ok(t, err != nil && strings.Contains(err.Error(), "choose an existing account"), "ambiguous destination owner must be explicit")
	}
	result, err = api.Carry(ctx, CarryRequest{Selector: sources[0].selector, To: "workbuddy", TargetAccount: "second-user", Cwd: s.Root})
	must(t, err)
	ok(t, result.Applied)
	eq(t, result.Plan.Target.Account, "second-user")
	result, err = api.Carry(ctx, CarryRequest{Selector: accountSource, To: "workbuddy", Cwd: s.Root})
	must(t, err)
	ok(t, result.Applied)
	eq(t, result.Plan.Target.Account, "fixture-user", "same profile retains the source's actual storage owner")
	eq(t, workBuddySessionSnapshot(t, seed.Plan.Target.Path, seed.Plan.Target.Session), accountBefore)
	for file, original := range originals {
		b, err := os.ReadFile(file)
		must(t, err)
		eq(t, core.SHA256Hex(b), original, "source changed")
	}
}
