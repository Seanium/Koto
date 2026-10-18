package core

import (
	"encoding/json"
	"net/url"
	"strings"
)

// IdentityClaim is an identity stated by native metadata. Scope distinguishes
// the creator of a session from its storage namespace or an individual message.
// Credentials and the currently logged-in user are never historical evidence.
type IdentityClaim struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Label    string `json:"label,omitempty"`
	Scope    string `json:"scope"`
	Evidence string `json:"evidence"`
}

type ProviderIdentity struct {
	ID       string `json:"id"`
	Model    string `json:"model,omitempty"`
	BaseURL  string `json:"baseUrl,omitempty"`
	Scope    string `json:"scope"`
	Evidence string `json:"evidence"`
}

type SessionIdentity struct {
	Status    string             `json:"status"`
	Claims    []IdentityClaim    `json:"claims"`
	Providers []ProviderIdentity `json:"providers"`
}

type SourceProfile struct {
	ID      string `json:"id"`
	Harness string `json:"harness"`
	Root    string `json:"root"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Variant string `json:"variant"`
	Default bool   `json:"default"`
}

// IdentityMeta makes a JSON-shaped value suitable for NativeRef.Meta["identity"].
func IdentityMeta(identity SessionIdentity) JsonObject {
	if identity.Claims == nil {
		identity.Claims = []IdentityClaim{}
	}
	if identity.Providers == nil {
		identity.Providers = []ProviderIdentity{}
	}
	identity.Status = "unknown"
	if len(identity.Claims) != 0 {
		identity.Status = "known"
	}
	for i := range identity.Providers {
		identity.Providers[i].BaseURL = SafeProviderURL(identity.Providers[i].BaseURL)
	}
	b, _ := json.Marshal(identity)
	var out JsonObject
	_ = json.Unmarshal(b, &out)
	return out
}

func ReadIdentity(meta JsonObject) SessionIdentity {
	var out SessionIdentity
	b, _ := json.Marshal(meta["identity"])
	_ = json.Unmarshal(b, &out)
	if len(out.Claims) == 0 {
		out.Status = "unknown"
	} else {
		out.Status = "known"
	}
	return out
}

func MergeIdentity(a, b SessionIdentity) SessionIdentity {
	seen := map[string]bool{}
	claims := []IdentityClaim{}
	for _, c := range append(append([]IdentityClaim{}, a.Claims...), b.Claims...) {
		key := ToJSON(c)
		if !seen[key] {
			claims = append(claims, c)
			seen[key] = true
		}
	}
	seen = map[string]bool{}
	providers := []ProviderIdentity{}
	for _, p := range append(append([]ProviderIdentity{}, a.Providers...), b.Providers...) {
		p.BaseURL = SafeProviderURL(p.BaseURL)
		key := ToJSON(p)
		if !seen[key] {
			providers = append(providers, p)
			seen[key] = true
		}
	}
	return SessionIdentity{Claims: claims, Providers: providers}
}

// IdentityOfDraft uses only metadata already decoded from the native message.
// It does not read authentication or the product's current default configuration.
func IdentityOfDraft(d *DraftEvent) SessionIdentity {
	model := Obj(d.Attrs["model"])
	provider, id := S(model["provider"]), S(model["id"])
	if id == "" {
		id = S(d.Attrs["model"])
	}
	if id == "" && provider == "" {
		return SessionIdentity{}
	}
	return SessionIdentity{Providers: []ProviderIdentity{{ID: provider, Model: id, Scope: "message", Evidence: "native message model"}}}
}

// SafeProviderURL removes credentials, query strings and fragments. Historical
// endpoints are optional; invalid or non-HTTP endpoints are not exposed.
func SafeProviderURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}

// SessionSelector keeps the native ID intact while explicitly choosing a profile.
func SessionSelector(connector, harness, session string) string {
	if connector == "" {
		connector = harness
	}
	return "@" + connector + ":" + session
}

// ParseSessionSelector returns a profile for canonical selectors and a harness
// for human-friendly agent:session shorthand. Shorthand must resolve uniquely
// across profiles before it can select a session.
func ParseSessionSelector(selector string) (namespace, session string, explicit, ok bool) {
	explicit = strings.HasPrefix(selector, "@")
	namespace, session, ok = strings.Cut(strings.TrimPrefix(selector, "@"), ":")
	ok = ok && namespace != "" && session != "" && namespace != "th"
	return
}

func MatchesSessionSelector(selector, connector, harness, session string) bool {
	ns, id, explicit, ok := ParseSessionSelector(selector)
	return ok && strings.HasPrefix(session, id) && ((explicit && ns == connector) || (!explicit && ns == harness))
}
