package engine

import (
	"testing"

	"koto/internal/core"
)

func TestSameProfileAccountDefaultRequiresUniqueStorageOwner(t *testing.T) {
	available := []core.IdentityClaim{{Kind: "account", ID: "a"}, {Kind: "account", ID: "b"}}
	claim := func(id, scope string) core.IdentityClaim {
		return core.IdentityClaim{Kind: "account", ID: id, Scope: scope}
	}
	for _, test := range []struct {
		name   string
		claims []core.IdentityClaim
		want   string
	}{
		{"unknown", nil, ""},
		{"creator-is-not-storage-owner", []core.IdentityClaim{claim("a", "creator")}, ""},
		{"unique", []core.IdentityClaim{claim("a", "storage")}, "a"},
		{"same-owner-two-evidence-scopes", []core.IdentityClaim{claim("a", "storage"), claim("a", "session")}, "a"},
		{"two-owners", []core.IdentityClaim{claim("a", "storage"), claim("b", "session")}, ""},
		{"reverse-order", []core.IdentityClaim{claim("b", "session"), claim("a", "storage")}, ""},
		{"unknown-destination", []core.IdentityClaim{claim("elsewhere", "storage")}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := uniqueStorageAccount(test.claims, available); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}
