package koto

import (
	"crypto/rand"
	"koto/internal/store"
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
	seen := map[string]bool{}
	harness := ""
	for _, sel := range c.Sessions {
		h, id, ok := strings.Cut(sel, ":")
		if !ok || id == "" || seen[sel] || h == "th" {
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
