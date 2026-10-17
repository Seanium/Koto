package main

// The core runs in this process: koto.App over the data dir (the user's, or the sandbox in
// development), its index kept fresh by polling (stat-only for unchanged files), and the API the
// frontend calls through AppService. Events go to the frontend as "koto:event"; the state as "koto:status".

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"koto/internal/koto"
)

type CoreStatus struct {
	State   string `json:"state"` // starting | ready | error
	Code    string `json:"code,omitempty"`
	DataDir string `json:"hubDir,omitempty"`
	Error   string `json:"error,omitempty"`
}

type Core struct {
	OnEvent  func(raw string)
	OnStatus func(CoreStatus)
	LiveMs   func() *int64

	mu     sync.Mutex
	app    *koto.App
	api    *koto.API
	status CoreStatus
	cancel context.CancelFunc
}

// configFromEnv: KOTO_APP_SANDBOX puts every root under one folder (development and previews).
func configFromEnv(live *int64) koto.Config {
	cfg := koto.Config{LiveMs: live}
	if ms := os.Getenv("KOTO_APP_LIVE_MS"); ms != "" {
		n, _ := strconv.ParseInt(ms, 10, 64)
		cfg.LiveMs = &n
	}
	if s := os.Getenv("KOTO_APP_SANDBOX"); s != "" {
		j := func(d string) string { return filepath.Join(s, d) }
		cfg.DataDir, cfg.ClaudeRoot, cfg.PiRoot, cfg.CodexHome = j("hub"), j("claude"), j("pi"), j("codex")
		cfg.OpenCodeRoot = j("opencode")
		cfg.CodexShadow, cfg.DshHome, cfg.AntigravityRoot, cfg.NoDesktop = j("codex-shadow"), j("dsh"), j("antigravity"), true
	}
	return cfg
}

func (c *Core) Status() CoreStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *Core) setStatus(s CoreStatus) {
	c.mu.Lock()
	c.status = s
	c.mu.Unlock()
	if c.OnStatus != nil {
		c.OnStatus(s)
	}
}

func (c *Core) emit(e koto.Event) {
	if c.OnEvent != nil {
		b, _ := json.Marshal(e)
		c.OnEvent(string(b))
	}
}

// Start opens the data and starts keeping the index fresh.
func (c *Core) Start() {
	c.setStatus(CoreStatus{State: "starting"})
	var live *int64
	if c.LiveMs != nil {
		live = c.LiveMs()
	}
	app, err := koto.New(configFromEnv(live))
	if err != nil {
		c.setStatus(CoreStatus{State: "error", Error: err.Error()})
		return
	}
	api := &koto.API{App: app, Emit: c.emit}
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.app, c.api, c.cancel = app, api, cancel
	c.mu.Unlock()
	c.setStatus(CoreStatus{State: "ready", DataDir: app.DataDir})
	go api.Watch(ctx, 3*time.Second)
}

// Stop lets go of the data.
func (c *Core) Stop() {
	c.mu.Lock()
	app, cancel := c.app, c.cancel
	c.app, c.api, c.cancel = nil, nil, nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if app != nil {
		app.Close()
	}
}

// Restart reopens (after a preference change or a failure).
func (c *Core) Restart() {
	c.Stop()
	c.Start()
}

// API is the running core's API, or nil while it starts.
func (c *Core) API() *koto.API {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.api
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
