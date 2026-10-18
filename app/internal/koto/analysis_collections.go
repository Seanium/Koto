package koto

import (
	"crypto/rand"
	"koto/internal/core"
	"koto/internal/store"
	"slices"
	"strings"
)

func (a *API) Analyses() ([]store.AnalysisCollection, error) { return a.App.Store.Analyses() }
func (a *API) SaveAnalysis(c store.AnalysisCollection) (store.AnalysisCollection, error) {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" || len([]rune(c.Name)) > 120 {
		return c, apiErr(400, "name must contain 1–120 characters")
	}
	if len(c.Sessions) < 2 || len(c.Sessions) > 32 {
		return c, apiErr(400, "choose 2–32 sessions")
	}
	// Renaming a saved comparison preserves its exact missing references; new
	// references must resolve now and are never persisted as ambiguous shorthand.
	if c.ID != "" {
		existing, err := a.App.Store.Analyses()
		if err != nil {
			return c, err
		}
		for _, old := range existing {
			if old.ID == c.ID && slices.Equal(old.Sessions, c.Sessions) {
				return c, a.App.Store.SaveAnalysis(c, false)
			}
		}
	}
	seen := map[string]bool{}
	harness := ""
	for i, sel := range c.Sessions {
		h, id, explicit, ok := core.ParseSessionSelector(sel)
		if !ok || id == "" {
			return c, apiErr(400, "select distinct native sessions")
		}
		if explicit {
			conn := a.App.Registry.Get(h)
			if conn == nil {
				return c, apiErr(400, "unknown profile")
			}
			h = conn.Harness
		}
		if row, err := a.App.ResolveSession(sel); err == nil {
			c.Sessions[i] = core.SessionSelector(row.Connector, row.Harness, row.Session)
			sel = c.Sessions[i]
		} else {
			return c, AsAPIError(err)
		}
		if seen[sel] {
			return c, apiErr(400, "select distinct native sessions")
		}
		if harness != "" && h != harness {
			return c, apiErr(400, "analysis requires sessions from the same Agent")
		}
		if len(a.App.Registry.ByHarness(h)) == 0 {
			return c, apiErr(400, "unknown Agent")
		}
		harness = h
		seen[sel] = true
	}
	create := c.ID == ""
	if create {
		c.ID = rand.Text()
	}
	err := a.App.Store.SaveAnalysis(c, create)
	return c, err
}
func (a *API) DeleteAnalysis(id string) error { return a.App.Store.DeleteAnalysis(id) }
