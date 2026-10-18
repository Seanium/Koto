package koto

import (
	"context"
	"koto/internal/engine"
	"koto/internal/store"
)

// ScanIndex shares the copy lease across processes and all open views.
// A decoder change discards offsets, not visible summaries. Discovery is
// published immediately; histories are enriched in the background.
func (a *App) ScanIndex(ctx context.Context, progress func(engine.ScanProgress)) engine.ScanReport {
	report := engine.ScanReport{Errors: []engine.ScanError{}}
	decoders := map[string]string{}
	reset := map[string]bool{}
	err := a.Engine.Locked(ctx, func() error {
		a.Store.Setting("index-decoders", &decoders)
		for _, state := range a.Registry.List() {
			reset[state.Connector.ID] = decoders[state.Connector.ID] != store.DecoderFingerprint(state.Connector)
			if state.Status == "failed" {
				_ = a.Registry.SetEnabled(state.Connector.ID, true)
			}
		}
		return nil
	})
	if err != nil {
		report.Errors = append(report.Errors, engine.ScanError{Error: err.Error()})
		return report
	}
	report = a.Sessions.ScanIncremental(ctx, progress, reset, func(fn func() error) error { return a.Engine.Locked(ctx, fn) })
	failed := map[string]bool{}
	for _, e := range report.Errors {
		failed[e.Connector] = true
	}
	if ctx.Err() == nil {
		err = a.Engine.Locked(ctx, func() error {
			// Merge another process's completed connectors instead of replacing them.
			latest := map[string]string{}
			a.Store.Setting("index-decoders", &latest)
			for _, c := range a.Registry.Sources() {
				if !failed[c.ID] {
					latest[c.ID] = store.DecoderFingerprint(c)
				}
			}
			a.Store.SetSetting("index-decoders", latest)
			if len(report.Errors) == 0 {
				wanted := store.IndexFingerprint(a.Registry)
				a.Store.SetMeta("native-index-fingerprint", &wanted)
			}
			return nil
		})
	} else {
		err = ctx.Err()
	}
	if err != nil && len(report.Errors) == 0 {
		report.Errors = append(report.Errors, engine.ScanError{Error: err.Error()})
	}
	return report
}

// RebuildIndex replaces only derived list rows/offsets. User settings, saved
// comparison selections and native files are untouched.
func (a *API) RebuildIndex(ctx context.Context) (engine.ScanReport, error) {
	a.scanMu.Lock()
	defer a.scanMu.Unlock()
	report := engine.ScanReport{Errors: []engine.ScanError{}}
	err := a.App.Engine.Locked(ctx, func() error {
		var err error
		report, err = a.App.Sessions.Rebuild(ctx, nil)
		return err
	})
	if err == nil {
		a.emit(Event{"type": "cycle", "report": report})
	}
	return report, err
}
