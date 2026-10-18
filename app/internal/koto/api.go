package koto

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/store"
)

// APIError is what a call returns on failure: a status like HTTP's (400, 404, 409, 503…), a code
// for the app to branch on, and detail.
type APIError struct {
	Status int             `json:"status"`
	Code   string          `json:"code,omitempty"`
	Msg    string          `json:"error"`
	Detail core.JsonObject `json:"detail,omitempty"`
}

func (e *APIError) Error() string { return e.Msg }

func apiErr(status int, msg string) *APIError { return &APIError{Status: status, Msg: msg} }

// AsAPIError maps any error to an APIError.
func AsAPIError(err error) *APIError {
	var a *APIError
	if errors.As(err, &a) {
		return a
	}
	var te *store.TrashError
	if errors.As(err, &te) {
		status := 409
		if te.Code == "NOT_FOUND" {
			status = 404
		}
		return &APIError{Status: status, Code: te.Code, Msg: te.Msg, Detail: te.Detail}
	}
	var ce *engine.CodedError
	if errors.As(err, &ce) {
		status := 400
		switch ce.Code {
		case "HUB_BUSY":
			status = 503
		case "SESSION_OPEN":
			status = 409
		}
		return &APIError{Status: status, Code: ce.Code, Msg: ce.Msg}
	}
	var nf store.ErrNotFound
	if errors.As(err, &nf) {
		return &APIError{Status: 404, Code: "NOT_FOUND", Msg: nf.Error()}
	}
	var tc *engine.TargetChangedError
	if errors.As(err, &tc) {
		return &APIError{Status: 409, Code: "TARGET_CHANGED", Msg: tc.Msg}
	}
	return &APIError{Status: 500, Msg: err.Error()}
}

// Event goes to the app: {type:'progress', …} during a long index pass, {type:'cycle', report}
// after each pass, {type:'clash', thread, files, harnesses, at}.
type Event = map[string]any

// API is the operations the app calls (and the CLI's serve-less commands): conversations from the
// index, native conversation views, copying and trash.
type API struct {
	App  *App
	Emit func(Event)

	scanMu    sync.Mutex
	scanning  bool
	stateMu   sync.Mutex
	scanState ScanStatus
	wakeOnce  sync.Once
	wake      chan struct{}
}

type ScanStatus struct {
	Scanning bool                 `json:"scanning"`
	Progress *engine.ScanProgress `json:"progress,omitempty"`
	Report   *engine.ScanReport   `json:"report,omitempty"`
}

func (a *API) ScanStatus() ScanStatus {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.scanState
}

func (a *API) scanWake() chan struct{} {
	a.wakeOnce.Do(func() { a.wake = make(chan struct{}, 1) })
	return a.wake
}

func (a *API) requestScan() {
	select {
	case a.scanWake() <- struct{}{}:
	default:
	}
}

// Reindex re-reads the index (stat-only for unchanged files), one pass at a time.
func (a *API) Reindex(ctx context.Context) engine.ScanReport {
	a.scanMu.Lock()
	defer a.scanMu.Unlock()
	a.stateMu.Lock()
	a.scanState = ScanStatus{Scanning: true}
	a.stateMu.Unlock()
	r := a.App.ScanIndex(ctx, func(p engine.ScanProgress) {
		a.stateMu.Lock()
		a.scanState = ScanStatus{Scanning: true, Progress: &p}
		a.stateMu.Unlock()
		a.emit(Event{"type": "progress", "connector": p.Connector, "harness": p.Harness, "done": p.Done, "total": p.Total, "added": p.Added})
	})
	a.stateMu.Lock()
	a.scanState = ScanStatus{Report: &r}
	a.stateMu.Unlock()
	a.emit(Event{"type": "cycle", "report": r})
	return r
}

func (a *API) emit(e Event) {
	if a.Emit != nil {
		a.Emit(e)
	}
}

// open is the working copy a selector names: a conversation (th:…, harness:session), or a node of one already open.
func (a *API) open(ctx context.Context, sel string) (*store.OpenThread, error) {
	if strings.HasPrefix(sel, "ev") {
		has := func(v *store.OpenThread) (hit bool) {
			v.Engine.Read(func() {
				_, h, _ := v.Hub.ResolveHead(sel)
				hit = h != ""
			})
			return
		}
		for _, v := range a.App.Sessions.Views() {
			if has(v) {
				return v, nil
			}
		}
		// The app got this node from a conversation opened earlier whose working copy has gone since
		// (a change on disk, or others opened after it): open those again.
		for _, r := range a.App.Sessions.Recent() {
			if v, err := a.App.OpenThread(ctx, r); err == nil && has(v) {
				return v, nil
			}
		}
		return nil, apiErr(404, "nothing matches "+sel)
	}
	v, err := a.App.OpenThread(ctx, sel)
	if err != nil {
		return nil, AsAPIError(err)
	}
	return v, nil
}

// headIn is the node a selector stands for inside a working copy (a conversation: its current head).
func headIn(v *store.OpenThread, sel string) (string, error) {
	if strings.HasPrefix(sel, "th:") {
		if h := v.Hub.ThreadHead(v.Thread); h != "" {
			return h, nil
		}
	} else if _, h, _ := v.Hub.ResolveHead(sel); h != "" {
		return h, nil
	}
	return "", apiErr(404, "nothing matches "+sel)
}

func jsLen(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xffff {
			n++
		}
	}
	return n
}

func jsSlice(s string, n int) string {
	var b strings.Builder
	units := 0
	for _, r := range s {
		width := 1
		if r > 0xffff {
			width = 2
		}
		if units+width > n {
			if units < n {
				b.WriteRune('\ufffd')
			}
			return b.String()
		}
		b.WriteRune(r)
		units += width
	}
	return s
}

// brief cuts tool output and reasoning to what a timeline can show (the working copy keeps everything).
func brief(e *core.Event) *core.Event {
	cut := func(t string, n int) string {
		if l := jsLen(t); l > n {
			return fmt.Sprintf("%s\n…（还有 %d 个字符）", jsSlice(t, n), l-n)
		}
		return t
	}
	var part func(p core.Part, inResult bool) core.Part
	part = func(p core.Part, inResult bool) core.Part {
		switch core.S(p["type"]) {
		case "text":
			if inResult {
				c := copyPart(p)
				c["text"] = cut(core.S(p["text"]), 2000)
				return c
			}
		case "reasoning":
			if t := core.S(p["text"]); t != "" {
				c := copyPart(p)
				c["text"] = cut(t, 20000)
				return c
			}
		case "tool_result":
			c := copyPart(p)
			var ps []any
			for _, x := range core.Parts(p["parts"]) {
				ps = append(ps, map[string]any(part(x, true)))
			}
			if ps == nil {
				ps = []any{}
			}
			c["parts"] = ps
			return c
		}
		return p
	}
	c := *e
	c.Parts = make([]core.Part, len(e.Parts))
	for i, p := range e.Parts {
		c.Parts[i] = part(p, false)
	}
	return &c
}

func copyPart(p core.Part) core.Part {
	c := core.Part{}
	for k, v := range p {
		c[k] = v
	}
	return c
}

// ------------------------------------------------------------------ calls

// Health is a liveness check.
func (a *API) Health() map[string]any { return map[string]any{"ok": true} }

// Connectors with their state.
func (a *API) Connectors() []ConnectorInfo { return a.App.Connectors() }

// AddRoot registers an extra storage root and re-reads the index.
func (a *API) AddRoot(harness, root string, variant ...string) (ConnectorInfo, error) {
	r, err := a.App.AddRoot(harness, root, variant...)
	if err != nil {
		return r, apiErr(400, err.Error())
	}
	a.requestScan()
	return r, nil
}

// RemoveRoot removes a root added with AddRoot.
func (a *API) RemoveRoot(ctx context.Context, id string) error {
	if a.App.Registry.Get(id) == nil {
		return apiErr(404, "unknown connector "+id)
	}
	if err := a.App.RemoveRoot(ctx, id); err != nil {
		return apiErr(400, err.Error())
	}
	return nil
}

// SetConnectorEnabled enables or disables a connector.
func (a *API) SetConnectorEnabled(id string, enabled bool) (ConnectorInfo, error) {
	if a.App.Registry.Get(id) == nil {
		return ConnectorInfo{}, apiErr(404, "unknown connector "+id)
	}
	return a.App.SetConnectorEnabled(id, enabled)
}

// Threads is the conversation list from the index: nothing is parsed until one is opened.
func (a *API) Threads(limit int) []ThreadListing {
	l := a.App.ListThreads()
	if limit > 0 && len(l) > limit {
		l = l[:limit]
	}
	return l
}

// ThreadDetail is a native session's path (or a page of it) with the conversation's sessions.
type ThreadDetail struct {
	Thread        string        `json:"thread"`
	Head          string        `json:"head"`
	Bindings      []BindingView `json:"bindings"`
	Events        []*core.Event `json:"events"`
	Offset        *int          `json:"offset,omitempty"`
	Total         *int          `json:"total,omitempty"`
	Turns         *int          `json:"turns,omitempty"`
	CompleteTurns int           `json:"completeTurns"`
}

// Thread is a native conversation path; with turns > 0 a page from the end (before: the page before that node),
// since a long session's whole path can be hundreds of MB.
func (a *API) Thread(ctx context.Context, sel string, turns int, before string) (*ThreadDetail, error) {
	v, err := a.open(ctx, sel)
	if err != nil {
		return nil, err
	}
	var out *ThreadDetail
	v.Engine.Read(func() {
		head, e := headIn(v, sel)
		if e != nil {
			err = e
			return
		}
		bs := a.App.Bindings(v.Hub, v.Thread)
		if _, _, _, native := core.ParseSessionSelector(sel); native {
			for _, b := range bs {
				if core.MatchesSessionSelector(sel, b.Connector, b.Harness, b.Session) {
					bs = []BindingView{b}
					break
				}
			}
		}
		if bs == nil {
			bs = []BindingView{}
		}
		out = &ThreadDetail{Thread: v.Group.ID, Head: head, Bindings: bs}
		out.CompleteTurns = v.Hub.TurnsAt(head)
		if out.CompleteTurns > 0 && !store.LastTurnComplete(v.Hub, head) {
			out.CompleteTurns--
		}
		if turns > 0 {
			if turns > 500 {
				turns = 500
			}
			evs, off, total := v.Hub.PathTail(head, turns, before, 0)
			out.Events = make([]*core.Event, 0, len(evs))
			for _, e := range evs {
				out.Events = append(out.Events, brief(e))
			}
			t := v.Hub.TurnsAt(head)
			out.Offset, out.Total, out.Turns = &off, &total, &t
			return
		}
		out.Events = v.Hub.Path(head)
	})
	return out, err
}

// Image is an image (or other blob) of a conversation as a data: URL, for the timeline. sel names
// the conversation or a node in it; sha is the blob's sha256 from the part.
func (a *API) Image(ctx context.Context, sel, sha string) (string, error) {
	v, err := a.open(ctx, sel)
	if err != nil {
		return "", err
	}
	var b []byte
	var ok bool
	v.Engine.Read(func() { b, ok = v.Hub.Blob(sha) })
	if !ok {
		return "", apiErr(404, "no blob "+sha)
	}
	if len(b) > 25<<20 {
		return "", apiErr(413, "attachment exceeds the 25 MiB preview limit")
	}
	return "data:" + http.DetectContentType(b) + ";base64," + base64.StdEncoding.EncodeToString(b), nil
}

// CarryRequest is a copy: from a node (or a selector), to a connector.
type CarryRequest struct {
	EventIDs        []string `json:"eventIds,omitempty"`
	ExcludeEventIDs []string `json:"excludeEventIds,omitempty"`
	Selector        string   `json:"selector,omitempty"`
	From            string   `json:"from,omitempty"`
	To              string   `json:"to"`
	TargetProfile   string   `json:"targetProfile,omitempty"`
	TargetAccount   string   `json:"targetAccount,omitempty"`
	Level           string   `json:"level,omitempty"`
	DryRun          bool     `json:"dryRun,omitempty"`
	MaxTokens       int      `json:"maxTokens,omitempty"`
	Cwd             string   `json:"cwd,omitempty"`
	Title           string   `json:"title,omitempty"`
	RequestID       string   `json:"requestId,omitempty"`
}

// Carry plans and (unless a dry run) applies a copy.
func (a *API) Carry(ctx context.Context, req CarryRequest) (*engine.CarryResult, error) {
	if req.From != "" && req.Selector != "" {
		return nil, apiErr(400, "give selector or from, not both")
	}
	sel := req.From
	if sel == "" {
		sel = req.Selector
	}
	if sel == "" {
		return nil, apiErr(400, "from must be a non-empty string")
	}
	if req.To == "" {
		return nil, apiErr(400, "to must be a non-empty string")
	}
	if len(req.RequestID) > 128 {
		return nil, apiErr(400, "requestId is too long")
	}
	v, err := a.open(ctx, sel)
	if err != nil {
		return nil, err
	}
	var head string
	v.Engine.Read(func() { head, err = headIn(v, sel) })
	if err != nil {
		return nil, err
	}
	if len([]rune(req.Title)) > 200 {
		req.Title = string([]rune(req.Title)[:200])
	}
	r, err := v.Engine.Carry(ctx, engine.CarryOptions{Thread: v.Thread, Head: head, To: req.To, TargetProfile: req.TargetProfile, TargetAccount: req.TargetAccount, Level: req.Level, DryRun: req.DryRun, RequestID: req.RequestID, EventIDs: req.EventIDs, ExcludeEventIDs: req.ExcludeEventIDs, MaxTokens: req.MaxTokens, Cwd: req.Cwd, Title: req.Title})
	if err != nil {
		return nil, AsAPIError(err)
	}
	if r.Applied {
		session := r.Plan.Target.Session
		if actual := core.S(r.Receipt["session"]); actual != "" {
			session = actual
		}
		if err := a.App.Sessions.RefreshSession(ctx, r.Plan.Sink, session, func(fn func() error) error { return a.App.Engine.Locked(ctx, fn) }); err != nil {
			r.Plan.Notes = append(r.Plan.Notes, "Copied session is saved; list refresh will retry: "+err.Error())
			a.requestScan()
		} else {
			a.emit(Event{"type": "cycle", "report": engine.ScanReport{Sessions: 1, Changed: 1, Errors: []engine.ScanError{}}})
		}

		for _, item := range a.App.ListThreads() {
			for _, b := range item.Bindings {
				if b.Connector == r.Plan.Sink && b.Harness == r.Plan.Harness && b.Session == session && b.Resume != nil {
					r.Resume = b.Resume.Shell
				}
			}
		}
	}
	return r, nil
}

// ForgetCopy drops an active operation after its dialog closes.
func (a *API) ForgetCopy(requestID string) { a.App.Engine.Jobs.Forget(requestID) }

// TrashThread moves a conversation's sessions (or one harness's) to the trash.
func (a *API) TrashThread(ctx context.Context, sel, harness string) (*store.TrashEntry, error) {
	if harness != "" && len(a.App.Registry.ByHarness(harness)) == 0 {
		return nil, apiErr(400, "unknown harness "+harness)
	}
	v, err := a.open(ctx, sel)
	if err != nil {
		return nil, err
	}
	e, err := a.App.Trash.Put(ctx, store.Context{Hub: v.Hub, Engine: v.Engine, Thread: v.Thread, Selector: sel}, harness)
	if err != nil {
		return nil, AsAPIError(err)
	}
	a.App.Sessions.Close() // working copies hold the moved sessions: rebuild on the next open
	a.App.Sessions.Invalidate()
	a.Reindex(ctx)
	return e, nil
}

// TrashList is what is in the trash now.
func (a *API) TrashList() []store.TrashEntry {
	l := a.App.Store.Trash(false)
	if l == nil {
		l = []store.TrashEntry{}
	}
	return l
}

// Restore puts a trash entry back.
func (a *API) Restore(ctx context.Context, id string) (*store.TrashEntry, error) {
	e, err := a.App.Trash.Restore(ctx, id)
	if err != nil {
		return nil, AsAPIError(err)
	}
	a.App.Sessions.Close()
	a.App.Sessions.Invalidate()
	a.Reindex(ctx)
	return e, nil
}
