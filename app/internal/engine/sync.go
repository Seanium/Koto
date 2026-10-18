package engine

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"koto/internal/core"
	"koto/internal/hub"
	"koto/internal/planner"
	"koto/internal/platform"
)

// Logger takes warnings and notes.
type Logger interface {
	Info(msg string)
	Warn(msg string)
}

type silent struct{}

func (silent) Info(string) {}
func (silent) Warn(string) {}

// SilentLogger drops everything.
var SilentLogger Logger = silent{}

// ScanError is one session or connector that could not be read.
type ScanError struct {
	Connector string `json:"connector"`
	Session   string `json:"session,omitempty"`
	Error     string `json:"error"`
}

// ScanReport sums up a scan.
type ScanReport struct {
	Sessions int         `json:"sessions"`
	Changed  int         `json:"changed"`
	Added    int         `json:"added"`
	Deduped  int         `json:"deduped"`
	Skipped  int         `json:"skipped"`
	Errors   []ScanError `json:"errors"`
}

// ScanProgress is where a scan stands.
type ScanProgress struct {
	Connector string `json:"connector"`
	Harness   string `json:"harness"`
	Done      int    `json:"done"`
	Total     int    `json:"total"`
	Added     int    `json:"added"`
}

// ScanOptions narrow and report a scan.
type ScanOptions struct {
	Connectors []string
	Filter     func(core.NativeRef) bool
	OnProgress func(ScanProgress)
	ProgressMs int
}

// CarryOptions say what to copy and where.
type CarryOptions struct {
	EventIDs        []string
	ExcludeEventIDs []string
	RequestID       string
	Selector        string
	Thread          core.ThreadID
	Head            core.EventID
	To              string
	TargetProfile   string
	TargetAccount   string
	Level           string
	DryRun          bool
	MaxTokens       int
	KeepTurns       int
	FullHistory     bool
	Cwd             string
	Title           string
	Summarizer      planner.Summarizer
	DeferIncomplete *bool
}

// CarryResult is a plan and whether it was applied.
type CarryResult struct {
	Plan    *DeliveryPlan   `json:"plan"`
	Applied bool            `json:"applied"`
	Receipt core.JsonObject `json:"receipt,omitempty"`
	Resume  string          `json:"resume,omitempty"`
}

// Locker is the lock every koto process on this data shares (koto.sqlite).
type Locker interface {
	TryLock(name string, ttl time.Duration) (busy bool, err error)
	Unlock(name string)
}

// Engine runs operations over one working copy, one at a time, and one at a time across processes.
type Engine struct {
	Hub      *hub.Hub
	Registry *Registry
	Log      Logger
	LockWait time.Duration
	Locks    Locker
	Jobs     *CopyJobs
	// Filter limits scans to the sessions in this working copy.
	Filter func(core.NativeRef) bool
	mu     sync.Mutex
}

// New makes an engine with the defaults (live 120 s, lock wait 120 s, lease 24 h).
func New(h *hub.Hub, r *Registry) *Engine {
	return &Engine{Jobs: NewCopyJobs(), Hub: h, Registry: r, Log: SilentLogger, LockWait: 120 * time.Second}
}

// exclusive runs fn as one operation: no other operation of this engine, nor of another process on this data, interleaves.
func (e *Engine) exclusive(ctx context.Context, fn func() error) error {
	for !e.mu.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.Locks != nil {
		ttl := 60 * time.Second
		deadline := time.Now().Add(e.LockWait)
		wait := 20 * time.Millisecond
		for {
			busy, err := e.Locks.TryLock("engine", ttl)
			if err != nil {
				return err
			}
			if !busy {
				break
			}
			if time.Now().After(deadline) {
				return &CodedError{Code: "HUB_BUSY", Msg: "another koto process is busy with this data; try again"}
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			if wait *= 2; wait > 500*time.Millisecond {
				wait = 500 * time.Millisecond
			}
		}
		stop, stopped := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(stopped)
			t := time.NewTicker(ttl / 3)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-t.C:
					_, _ = e.Locks.TryLock("engine", ttl)
				}
			}
		}()
		defer func() { close(stop); <-stopped; e.Locks.Unlock("engine") }()
	}
	return fn()
}

// Locked runs fn as one engine operation shared across processes.
func (e *Engine) Locked(ctx context.Context, fn func() error) error { return e.exclusive(ctx, fn) }

// ------------------------------------------------------------------ ingest

// Scan reads every enabled source (stat-only for unchanged files).
func (e *Engine) Scan(ctx context.Context, opts ScanOptions) (ScanReport, error) {
	var r ScanReport
	err := e.exclusive(ctx, func() error { r = e.ScanLocked(ctx, opts); return nil })
	return r, err
}

// ScanLocked is Scan inside an operation.
func (e *Engine) ScanLocked(ctx context.Context, opts ScanOptions) ScanReport {
	if opts.Filter == nil {
		opts.Filter = e.Filter
	}
	report := ScanReport{Errors: []ScanError{}}
	for _, c := range e.Registry.Sources() {
		if len(opts.Connectors) > 0 && !containsStr(opts.Connectors, c.ID) {
			continue
		}
		if err := e.scanConnector(ctx, c, &report, opts); err != nil {
			e.Registry.MarkFailed(c.ID, err)
			report.Errors = append(report.Errors, ScanError{Connector: c.ID, Error: err.Error()})
			e.Log.Warn(fmt.Sprintf("connector %s failed: %v", c.ID, err))
		}
	}
	return report
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func (e *Engine) scanConnector(ctx context.Context, c *Connector, report *ScanReport, opts ScanOptions) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	all, err := c.Source.Discover(ctx)
	if err != nil {
		return err
	}
	var refs []core.NativeRef
	for _, r := range all {
		r.Connector = c.ID
		r.Meta = ProfileMeta(c, r.Meta)
		if opts.Filter == nil || opts.Filter(r) {
			refs = append(refs, r)
		}
	}
	// Parents before children, so forks find their lineage base (mtime as a proxy; a child whose
	// parent is still missing is retried once at the end).
	sort.SliceStable(refs, func(i, j int) bool { return refs[i].MtimeMs < refs[j].MtimeMs })
	known := map[[2]string]bool{}
	for _, r := range refs {
		known[[2]string{r.Harness, r.Session}] = true
	}
	var retry []core.NativeRef
	done := 0
	every := time.Duration(opts.ProgressMs) * time.Millisecond
	if every == 0 {
		every = 500 * time.Millisecond
	}
	last, reported := time.Now(), false
	progress := func(force bool) {
		if opts.OnProgress == nil || (!force && time.Since(last) < every) {
			return
		}
		last, reported = time.Now(), true
		opts.OnProgress(ScanProgress{Connector: c.ID, Harness: c.Harness, Done: done, Total: len(refs), Added: report.Added})
	}
	errDefer := errors.New("defer")
	runOne := func(ref core.NativeRef, allowDefer bool) {
		report.Sessions++
		from, ok := e.Hub.Offset(c.ID, ref.Session)
		var fromp *core.SourceOffset
		if ok {
			fromp = &from
		}
		// Unchanged content can still carry new metadata from beside the file (a Codex rename).
		if Unchanged(ref, fromp) {
			if ref.Meta != nil {
				e.Hub.MergeMeta(ref.Harness, ref.Session, ref.Meta, c.ID)
			}
			return
		}
		first := true
		contentChanged := !ok
		err := c.Source.Tail(ctx, ref, fromp, func(b core.SourceBatch) error {
			if first && allowDefer && b.Lineage != nil && e.Hub.Binding(b.Lineage.Harness, b.Lineage.Session, c.ID) == nil && known[[2]string{b.Lineage.Harness, b.Lineage.Session}] {
				return errDefer
			}
			first = false
			if !ok || b.Offset.Records != from.Records || b.Offset.PrefixHash != from.PrefixHash {
				contentChanged = true
			}
			r := e.Hub.Ingest(b, hub.ConnectorInfo{ID: c.ID, Harness: c.Harness, Version: c.Version})
			report.Added += r.Added
			report.Deduped += r.Deduped
			report.Skipped += r.Skipped
			return nil
		})
		if errors.Is(err, errDefer) {
			retry = append(retry, ref)
			report.Sessions--
			return
		}
		if err != nil {
			report.Errors = append(report.Errors, ScanError{Connector: c.ID, Session: ref.Session, Error: err.Error()})
			e.Log.Warn(fmt.Sprintf("%s/%s: %v", c.ID, ref.Session, err))
			return
		}
		if ref.Meta != nil {
			e.Hub.MergeMeta(ref.Harness, ref.Session, ref.Meta, c.ID)
		}
		if contentChanged {
			report.Changed++
		}

	}
	for _, ref := range refs {
		if ctx.Err() != nil {
			return nil
		}
		runOne(ref, true)
		done++
		progress(false)
	}
	for _, ref := range retry {
		runOne(ref, false)
	}
	if reported {
		progress(true)
	}
	return nil
}

// ------------------------------------------------------------------ carry

// Resolve finds the thread and head a carry starts from.
func (e *Engine) Resolve(o CarryOptions) (core.ThreadID, core.EventID, error) {
	if o.Thread != "" && o.Head != "" {
		return o.Thread, o.Head, nil
	}
	if o.Head != "" {
		ev := e.Hub.GetEvent(o.Head)
		if ev == nil {
			return "", "", fmt.Errorf("unknown event %s", o.Head)
		}
		return ev.Thread, ev.ID, nil
	}
	sel := o.Selector
	if sel == "" {
		sel = o.Thread
	}
	if sel == "" {
		return "", "", errors.New("carry: need a selector (event id, thread id, or harness:session)")
	}
	t, h, err := e.Hub.ResolveHead(sel)
	if err != nil {
		return "", "", &CodedError{Code: "AMBIGUOUS", Msg: err.Error()}
	}
	if h == "" {
		return "", "", fmt.Errorf("nothing matches %s", sel)
	}
	return t, h, nil
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// ResolveTarget always allocates a new native session. A caller-supplied request ID makes
// preview, apply, and retries identify one copy while the copy dialog stays open.
func (e *Engine) ResolveTarget(sink *Connector, thread core.ThreadID, head core.EventID, o CarryOptions, level string) (DeliveryTarget, error) {
	key := rand.Text()
	if o.RequestID != "" {
		key = e.Jobs.seed + "\x00" + o.RequestID
	}
	key = sink.ID + "\x00" + key

	return DeliveryTarget{Mode: "create", Session: sink.Sink.NewSessionID(key), Cwd: o.Cwd, Title: o.Title, Profile: sink.ID, Account: o.TargetAccount, Root: sink.Root}, nil
}

// Planned is a plan with its projection and sink.
type Planned struct {
	Plan       *DeliveryPlan
	Projection *planner.Projection
	Sink       *Connector
}

// Plan decides what a carry would write.
func (e *Engine) Plan(ctx context.Context, o CarryOptions) (*Planned, error) {
	var p *Planned
	err := e.exclusive(ctx, func() error { var err error; p, err = e.planLocked(ctx, o); return err })
	return p, err
}

// HarnessNames for notes the model reads.
var HarnessNames = map[string]string{"claude-code": "Claude Code", "pi": "pi", "codex": "Codex", "dsh": "DeepSeek Harness", "antigravity": "Antigravity CLI", "opencode": "OpenCode"}

func (e *Engine) planLocked(ctx context.Context, o CarryOptions) (*Planned, error) {
	// Refresh and resolve the source before choosing a default destination profile.
	e.ScanLocked(ctx, ScanOptions{})
	thread, head, err := e.Resolve(o)
	if err != nil {
		return nil, err
	}
	if o.Cwd == "" {
		if source := e.Hub.GetEvent(head); source != nil {
			if b := e.Hub.Binding(source.Provenance.Source.Harness, source.Provenance.Native.Session, source.Provenance.Source.Connector); b != nil {
				o.Cwd = core.CurrentDirectory(b.Meta)
			}
		}
	}
	to := o.To
	if o.TargetProfile != "" {
		to = o.TargetProfile
	} else if source := e.Hub.GetEvent(head); source != nil && source.Provenance.Source.Harness == o.To {
		to = source.Provenance.Source.Connector
	}
	sink, err := e.Registry.Require(to)
	if err != nil {
		return nil, err
	}
	if o.TargetProfile != "" && o.To != "" && o.To != sink.ID && o.To != sink.Harness {
		return nil, fmt.Errorf("target profile does not belong to %s", o.To)
	}
	if sink.Sink == nil {
		return nil, fmt.Errorf("%s cannot be a delivery target", o.To)
	}
	if !e.Registry.Enabled(sink.ID) {
		return nil, fmt.Errorf("%s is disabled", sink.ID)
	}
	if accounts, ok := sink.Sink.(TargetAccounts); ok {
		available, err := accounts.TargetAccounts(ctx)
		if err != nil {
			return nil, err
		}
		if o.TargetAccount == "" {
			if source := e.Hub.GetEvent(head); source != nil && source.Provenance.Source.Connector == sink.ID {
				if b := e.Hub.Binding(source.Provenance.Source.Harness, source.Provenance.Native.Session, sink.ID); b != nil {
					o.TargetAccount = uniqueStorageAccount(core.ReadIdentity(b.Meta).Claims, available)
				}
			}
			if o.TargetAccount == "" && len(available) == 1 {
				o.TargetAccount = available[0].ID
			}
		}
		valid := false
		for _, candidate := range available {
			if candidate.Kind == "account" && candidate.ID == o.TargetAccount {
				valid = true
			}
		}
		if !valid {
			return nil, fmt.Errorf("choose an existing account in target profile %s", sink.ID)
		}
	} else if o.TargetAccount != "" {
		return nil, fmt.Errorf("%s does not support selecting a target account; choose its storage profile", sink.ID)
	}
	path := e.Hub.Path(head)
	attachments := core.AttachmentQuestions(path)
	if o.EventIDs != nil && o.ExcludeEventIDs != nil {
		return nil, fmt.Errorf("give selected or excluded messages, not both")
	}
	// A default-all selection only sends exceptions, keeping old pages out of the UI heap.
	// The caller freezes Head on entering selection, so later source turns cannot join the copy.
	if o.ExcludeEventIDs != nil {
		excluded := map[string]bool{}
		for _, id := range o.ExcludeEventIDs {
			excluded[id] = true
		}
		for attachment, question := range attachments {
			if excluded[question] {
				excluded[attachment] = true
			}
		}
		o.EventIDs = []string{}
		for _, ev := range path {
			if excluded[ev.ID] {
				delete(excluded, ev.ID)
			} else if ev.Kind != "context" {
				o.EventIDs = append(o.EventIDs, ev.ID)
			}
		}
		if len(excluded) > 0 {
			return nil, fmt.Errorf("excluded messages are no longer on this session's path; refresh and select again")
		}
	}
	if o.EventIDs != nil {
		selected := map[string]bool{}
		for _, id := range o.EventIDs {
			selected[id] = true
		}
		if o.ExcludeEventIDs == nil {
			for attachment, question := range attachments {
				if selected[question] {
					selected[attachment] = true
				}
			}
		}
		if len(selected) == 0 {
			return nil, fmt.Errorf("select at least one message")
		}
		filtered := []*core.Event{}
		for _, ev := range path {
			if selected[ev.ID] {
				// Like full-path copying, an excerpt never carries source system context.
				// Native context may sit inside a visible turn; ignore it without dropping
				// the selected conversation, while still validating every requested ID.
				if ev.Kind != "context" {
					filtered = append(filtered, ev)
				}
				delete(selected, ev.ID)
			}
		}
		if len(selected) > 0 {
			return nil, fmt.Errorf("selected messages are no longer on this session's path; refresh and select again")
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("select at least one message")
		}
		path = filtered
		head = path[len(path)-1].ID
		// An excerpt may cut tool-call pairs; project it through the canonical normalizer.
		if o.Level == "" || o.Level == "L0" {
			o.Level = "L1"
		}
		no := false
		o.DeferIncomplete = &no
	}

	same := len(path) > 0
	for _, ev := range path {
		if ev.Provenance.Source.Harness != sink.Harness {
			same = false
			break
		}
	}
	level := o.Level
	if level == "" {
		level = planner.DefaultLevel(sink.Capabilities, same)
	}
	target, err := e.ResolveTarget(sink, thread, head, o, level)
	if err != nil {
		return nil, err
	}
	requestKey := core.SHA256Hex(core.ToJSON([]any{thread, head, o.EventIDs, o.ExcludeEventIDs, level, o.Cwd, o.Title, o.MaxTokens, sink.ID, o.TargetAccount, sink.Root}))
	if o.RequestID != "" {
		if job := e.Jobs.get(o.RequestID); job != nil {
			if job.fingerprint != requestKey || job.plan.Sink != sink.ID {
				return nil, fmt.Errorf("copy request changed; start a new copy")
			}
			return &Planned{Plan: job.plan, Sink: sink}, nil
		}
	}
	proj := planner.Project(path, planner.Options{
		ReadBlob: e.Hub.Blob,
		Sink:     sink.ID, SinkHarness: sink.Harness, Capabilities: sink.Capabilities, Vocabulary: sink.Vocabulary, Level: level,
		SourceVocabulary: func(h string) core.ToolVocabulary {
			for _, c := range e.Registry.Sources() {
				if c.Harness == h {
					return c.Vocabulary
				}
			}
			return nil
		},
		MaxTokens: o.MaxTokens, KeepTurns: o.KeepTurns, FullHistory: o.FullHistory, Summarizer: o.Summarizer, DeferIncomplete: o.DeferIncomplete,
	})
	// Current application project membership is an explicit default, distinct
	// from the historical cwd on each event. An explicit user target still wins.
	for i := len(path) - 1; i >= 0; i-- {
		if b := e.Hub.Binding(path[i].Provenance.Source.Harness, path[i].Provenance.Native.Session, path[i].Provenance.Source.Connector); b != nil {
			if cwd := core.CurrentDirectory(b.Meta); cwd != "" {
				proj.Hints.Cwd = cwd
				break
			}
		}
	}
	// Harnesses that keep the working directory only in a session header put it in the binding.
	if proj.Hints.Cwd == "" {
		for i := len(path) - 1; i >= 0; i-- {
			if b := e.Hub.Binding(path[i].Provenance.Source.Harness, path[i].Provenance.Native.Session, path[i].Provenance.Source.Connector); b != nil {
				if c, ok := b.Meta["cwd"].(string); ok && c != "" {
					proj.Hints.Cwd = c
					break
				}
			}
		}
	}
	effective := head
	if len(proj.Deferred) > 0 {
		for i, ev := range path {
			if ev.ID == proj.Deferred[0] {
				if i > 0 {
					effective = path[i-1].ID
				}
				break
			}
		}
	}
	// Native Codex on Windows can persist \\?\C:\... as cwd. Target Agents
	// encode a normal project path into a directory name; retaining the namespace
	// marker would put an illegal '?' in that name. Source metadata stays intact.
	target.Cwd = platform.ProjectPath(target.Cwd)
	proj.Hints.Cwd = platform.ProjectPath(proj.Hints.Cwd)
	readCtx, closeReads := withRecordReads(ctx)
	defer closeReads()
	plan, err := sink.Sink.Plan(PlanInput{Thread: thread, Head: effective, Level: level, Projection: proj, Target: target, Hub: e.Hub, Residual: func(ev *core.Event) []byte { return e.residual(readCtx, ev) }})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	if plan.Notes == nil {
		plan.Notes = []string{}
	}
	if plan.Losses == nil {
		plan.Losses = []core.Loss{}
	}
	for _, ev := range path {
		if miss, ok := ev.Attrs["kotoMissingBlobs"].([]any); ok {
			plan.Losses = append(plan.Losses, core.Loss{Sink: sink.ID, Event: ev.ID, Field: "parts", Kind: "dropped", Note: fmt.Sprintf("content unreadable (%d blobs)", len(miss))})
		}
	}
	if o.RequestID != "" {
		e.Jobs.put(o.RequestID, &copyJob{fingerprint: requestKey, plan: plan})
	}
	return &Planned{Plan: plan, Projection: proj, Sink: sink}, nil
}

// A historical source can have multiple identity claims. Only one distinct
// native storage owner can be a default; claim order is never a selection rule.
func uniqueStorageAccount(claims, available []core.IdentityClaim) string {
	matches := map[string]bool{}
	for _, claim := range claims {
		if claim.Kind != "account" || (claim.Scope != "session" && claim.Scope != "storage") {
			continue
		}
		for _, candidate := range available {
			if candidate.Kind == "account" && candidate.ID == claim.ID {
				matches[claim.ID] = true
			}
		}
	}
	if len(matches) == 1 {
		for id := range matches {
			return id
		}
	}
	return ""
}

// Carry plans and (unless a dry run, noop or reject) applies a copy.
func (e *Engine) Carry(ctx context.Context, o CarryOptions) (*CarryResult, error) {
	var r *CarryResult
	err := e.exclusive(ctx, func() error { var err error; r, err = e.CarryLocked(ctx, o); return err })
	return r, err
}

// CarryLocked is Carry inside an operation.
func (e *Engine) CarryLocked(ctx context.Context, o CarryOptions) (*CarryResult, error) {
	p, err := e.planLocked(ctx, o)
	if err != nil {
		return nil, err
	}
	if o.DryRun || p.Plan.Mode == "reject" || p.Plan.Mode == "noop" {
		return &CarryResult{Plan: p.Plan}, nil
	}
	if o.RequestID != "" {
		if job := e.Jobs.get(o.RequestID); job != nil && job.result != nil {
			return job.result, nil
		}
	}
	result, err := e.ApplyPlan(ctx, p.Sink, p.Plan)
	if err == nil && o.RequestID != "" {
		e.Jobs.finish(o.RequestID, result)
	}
	return result, err
}

// ApplyPlan writes only a new native session. No copy history is stored.
func (e *Engine) ApplyPlan(ctx context.Context, sink *Connector, plan *DeliveryPlan) (*CarryResult, error) {
	if sink == nil || sink.Sink == nil {
		return nil, fmt.Errorf("connector cannot be a delivery target")
	}
	if current := e.Registry.Get(sink.ID); current != sink || current.Sink == nil {
		return nil, fmt.Errorf("delivery target changed or is no longer writable")
	}
	if plan == nil || plan.Mode != "create" {
		return nil, fmt.Errorf("only new-session copies are supported")
	}
	receipt, err := sink.Sink.Apply(ctx, plan)
	if err != nil {
		return nil, err
	}
	session := plan.Target.Session
	if id := core.S(receipt["session"]); id != "" {
		session = id
	}
	result := &CarryResult{Plan: plan, Applied: true, Receipt: receipt}
	if argv := ResumeArgv(sink, plan.Harness, session); argv != nil {
		result.Resume = ShellCommand(argv, "", ResumeEnvironment(sink, plan.Harness, session))
	}
	return result, nil
}

// Residual re-reads a native record for L0 replay and checks its hash.
func (e *Engine) Residual(ev *core.Event) []byte {
	return e.residual(context.Background(), ev)
}

func (e *Engine) residual(ctx context.Context, ev *core.Event) []byte {
	if ctx.Err() != nil {
		return nil
	}
	r := ev.Provenance.Residual
	if r == nil {
		return nil
	}
	c := e.Registry.Get(ev.Provenance.Source.Connector)
	if c == nil {
		for _, x := range e.Registry.ByHarness(ev.Provenance.Source.Harness) {
			if _, ok := x.Source.(RecordReader); ok {
				c = x
				break
			}
		}
	}
	if c == nil {
		return nil
	}
	rr, ok := c.Source.(RecordReader)
	if !ok {
		return nil
	}
	idx := 0
	if ev.Provenance.Native.Index != nil {
		idx = *ev.Provenance.Native.Index
	}
	var raw []byte
	var err error
	if contextual, ok := c.Source.(ContextRecordReader); ok {
		raw, err = contextual.ReadRecordContext(ctx, ev.Provenance.Native.Session, idx, ev.Provenance.Native.Offset)
	} else {
		raw, err = rr.ReadRecord(ev.Provenance.Native.Session, idx, ev.Provenance.Native.Offset)
	}
	if err != nil || raw == nil || core.SHA256Hex(raw) != r.SHA256 {
		return nil
	}
	return raw
}

// Read runs fn with no operation of this engine in progress (reads of the working copy).
func (e *Engine) Read(fn func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	fn()
}
