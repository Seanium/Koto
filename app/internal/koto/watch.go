package koto

import (
	"context"
	"time"
)

// Watch keeps the index fresh while the app runs: an index pass is stat-only for unchanged files,
// so polling every few seconds costs little and needs no per-directory watches. Nothing is ever
// written on its own: copies are manual.
func (a *API) Watch(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 3 * time.Second
	}
	a.Reindex(ctx)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.scanWake():
			a.Reindex(ctx)
		case <-t.C:
			a.App.Engine.Jobs.Sweep()
			a.App.Sessions.SweepViews(time.Now())
			a.Reindex(ctx)
		}
	}
}
