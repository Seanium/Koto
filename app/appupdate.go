package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"koto/internal/update"
)

type UpdateState struct {
	Revision   uint64 `json:"revision"`
	Phase      string `json:"phase"`
	Current    string `json:"current"`
	Latest     string `json:"latest"`
	Prerelease bool   `json:"prerelease"`
	URL        string `json:"url"`
	Error      string `json:"error"`
	ErrorCode  string `json:"errorCode"`
	ErrorStage string `json:"errorStage"`
	RetryAt    int64  `json:"retryAt"`
	Downloaded int64  `json:"downloaded"`
	Total      int64  `json:"total"`
}

// One worker per app; navigating settings never starts a second download or
// retains webview listeners. Network operations end when the app shuts down.
type appUpdater struct {
	mu      sync.Mutex
	state   UpdateState
	result  *update.Result
	checked time.Time
	busy    bool
	ctx     context.Context
	cancel  context.CancelFunc
	client  updateClient
	emit    func(UpdateState)
	quit    func()
}

type updateClient interface {
	Check(context.Context, string, update.Product, string, string, string) (*update.Result, error)
	Prepare(context.Context, *update.Result, update.Location, func(int64, int64)) (*update.Prepared, error)
}

func updateFailed(s *UpdateState, err error, stage string) {
	s.Phase, s.Error, s.ErrorStage = "error", err.Error(), stage
	s.ErrorCode, s.RetryAt = "", 0
	s.URL = update.ReleasesPage
	if errors.Is(err, update.ErrStaleFeed) {
		s.ErrorCode = "STALE_FEED"
	}
	var request *update.RequestError
	if errors.As(err, &request) {
		s.ErrorCode = request.Code()
		if !request.RetryAt.IsZero() {
			s.RetryAt = request.RetryAt.UnixMilli()
		}
	}
}

func newAppUpdater(emit func(UpdateState), quit func()) *appUpdater {
	ctx, cancel := context.WithCancel(context.Background())
	return &appUpdater{ctx: ctx, cancel: cancel, client: update.NewClient(), emit: emit, quit: quit,
		state: UpdateState{Phase: "idle", Current: strings.TrimSpace(appVersionFile)}}
}

func (u *appUpdater) snapshot() UpdateState {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.state
}

func (u *appUpdater) publish(fn func(*UpdateState)) {
	u.mu.Lock()
	fn(&u.state)
	u.state.Revision++
	state := u.state
	u.mu.Unlock()
	if u.emit != nil && u.ctx.Err() == nil {
		u.emit(state)
	}
}

func (u *appUpdater) check(force bool) UpdateState {
	u.mu.Lock()
	if u.busy || u.state.RetryAt > time.Now().UnixMilli() || (!force && u.state.RetryAt == 0 && u.state.ErrorCode != "STALE_FEED" && !u.checked.IsZero() && time.Since(u.checked) < time.Hour) {
		state := u.state
		u.mu.Unlock()
		return state
	}
	u.busy = true
	u.state.Phase = "checking"
	u.state.Revision++
	u.state.Error = ""
	u.state.ErrorCode, u.state.ErrorStage, u.state.RetryAt = "", "", 0
	state := u.state
	u.mu.Unlock()
	go func() {
		result, err := u.client.Check(u.ctx, state.Current, update.App, runtime.GOOS, runtime.GOARCH, "auto")
		u.publish(func(s *UpdateState) {
			u.result = result
			u.checked = time.Now()
			u.busy = false
			if err != nil {
				s.Latest, s.Prerelease = "", false
				var unpublished *update.NoPublishedReleaseError
				if errors.As(err, &unpublished) {
					s.Phase, s.Error, s.ErrorStage, s.RetryAt = "unpublished", "", "", 0
					s.ErrorCode, s.URL = "NO_PUBLISHED_RELEASE", update.ReleasesPage
					if unpublished.Channel == "stable" {
						s.ErrorCode = "NO_PUBLISHED_STABLE_RELEASE"
					}
				} else {
					updateFailed(s, err, "check")
				}
				return
			}
			s.Latest, s.Prerelease = result.Latest, result.Prerelease
			s.URL = result.URL
			s.Phase = "current"
			if result.Available {
				s.Phase = "available"
			} else if update.Newer(s.Current, result.Latest) {
				s.Phase = "ahead"
			}
		})
	}()
	return state
}

func (u *appUpdater) install() error {
	u.mu.Lock()
	if u.busy {
		u.mu.Unlock()
		return fmt.Errorf("an update operation is already running")
	}
	if u.result == nil || !u.result.Available {
		u.mu.Unlock()
		return fmt.Errorf("check for a newer version first")
	}
	result := *u.result
	u.busy = true
	u.mu.Unlock()
	u.publish(func(s *UpdateState) {
		s.Phase = "downloading"
		s.Error = ""
		s.ErrorCode, s.ErrorStage, s.RetryAt = "", "", 0
		s.Downloaded = 0
		s.Total = result.Asset.Size
	})
	go func() {
		err := u.apply(&result)
		if err == nil {
			return
		}
		u.publish(func(s *UpdateState) { u.busy = false; updateFailed(s, err, "install") })
	}()
	return nil
}

func (u *appUpdater) apply(result *update.Result) error {
	location, err := update.Locate(update.App)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(u.ctx, 6*time.Minute)
	defer cancel()
	last := time.Time{}
	prepared, err := u.client.Prepare(ctx, result, location, func(n, total int64) {
		if n != total && time.Since(last) < 200*time.Millisecond {
			return
		}
		last = time.Now()
		u.publish(func(s *UpdateState) { s.Downloaded = n; s.Total = total })
	})
	if err != nil {
		return err
	}
	defer prepared.Close()
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = prepared.InstallContext(ctx); err != nil {
		return err
	}
	if err = prepared.Restart(); err != nil {
		if rollback := prepared.Rollback(); rollback != nil {
			return fmt.Errorf("%v; %w", err, rollback)
		}
		return err
	}
	u.publish(func(s *UpdateState) { s.Phase = "restarting" })
	u.quit()
	return nil
}

func (s *AppService) UpdateState() UpdateState {
	if s.updates == nil {
		return UpdateState{Phase: "idle", Current: strings.TrimSpace(appVersionFile)}
	}
	return s.updates.check(false)
}

func (s *AppService) CheckUpdates() UpdateState { return s.updates.check(true) }
func (s *AppService) InstallUpdate() error      { return s.updates.install() }
