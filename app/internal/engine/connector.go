// Package engine holds the connector contract, the generic JSONL reader, and the operations over
// a working copy: reading native sessions in, planning and applying copies, the trash.
package engine

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"koto/internal/core"
	"koto/internal/hub"
	"koto/internal/planner"
)

// Source reads a harness's sessions.
type Source interface {
	Discover(ctx context.Context) ([]core.NativeRef, error)
	// Tail reads from `from` (nil = the beginning), handing each batch to yield.
	Tail(ctx context.Context, ref core.NativeRef, from *core.SourceOffset, yield func(core.SourceBatch) error) error
	// WatchRoots are paths worth watching for change signals.
	WatchRoots() []string
}

// RecordReader fetches one raw native record (L0 replay).
type RecordReader interface {
	ReadRecord(session string, index int, offset *int64) ([]byte, error)
}

// ContextRecordReader allows cancellation and request-scoped streaming for L0 replay.
type ContextRecordReader interface {
	ReadRecordContext(ctx context.Context, session string, index int, offset *int64) ([]byte, error)
}

// DeliveryTarget is where a delivery lands: a new native session.
type DeliveryTarget struct {
	Mode    string `json:"mode"` // create
	Session string `json:"session"`
	Cwd     string `json:"cwd,omitempty"`
	Title   string `json:"title,omitempty"`
	Path    string `json:"path,omitempty"`
	Profile string `json:"profile,omitempty"`
	Account string `json:"account,omitempty"`
	Root    string `json:"root,omitempty"`
}

// PlanRecord is one record a plan writes and the node it stands for.
type PlanRecord = hub.DeliveryRecord

// DeliveryPlan is what a sink will write, decided before anything is written.
type DeliveryPlan struct {
	ID      string          `json:"id"`
	Sink    string          `json:"sink"`
	Harness string          `json:"harness"`
	Thread  core.ThreadID   `json:"thread"`
	Head    core.EventID    `json:"head"`
	Level   string          `json:"level"`
	Mode    string          `json:"mode"` // create | noop | reject
	Target  DeliveryTarget  `json:"target"`
	Records []PlanRecord    `json:"records"`
	Content string          `json:"content,omitempty"`
	Losses  []core.Loss     `json:"losses"`
	Notes   []string        `json:"notes"`
	Extra   core.JsonObject `json:"extra,omitempty"`
}

// PlanInput is what a sink plans from.
type PlanInput struct {
	Thread     core.ThreadID
	Head       core.EventID
	Level      string
	Projection *planner.Projection
	Target     DeliveryTarget
	Hub        *hub.Hub
	// Residual resolves a raw native record for L0 replay.
	Residual func(e *core.Event) []byte
}

// SessionRef is a native session as sinks and the trash see it.
type SessionRef struct {
	Session string
	Path    string
	Meta    core.JsonObject
}

// Sink writes into a harness.
type Sink interface {
	Plan(in PlanInput) (*DeliveryPlan, error)
	// Apply must be idempotent: re-applying a plan that already landed is a no-op.
	Apply(ctx context.Context, plan *DeliveryPlan) (core.JsonObject, error)
	// NewSessionID is a deterministic id for a new native session, from an opaque key.
	NewSessionID(key string) string
	// ResumeCommand is the command that continues a session ({id} stands for its id), "" for none.
	ResumeCommand() string
}

// ResumeIDer: the id the harness resumes by, when the session key differs (a Codex rollout continuation).
type ResumeIDer interface{ ResumeID(session string) string }

// TargetAccounts is implemented only by products whose native writer can select
// a historical storage owner. A provider or the creator of a session is not enough.
type TargetAccounts interface {
	TargetAccounts(context.Context) ([]core.IdentityClaim, error)
}

// OpenChecker: is this native session open in a running harness process right now? Returns why, or "".
type OpenChecker interface{ OpenBy(s SessionRef) string }

// Trasher moves a native session to the trash (never deletes it): its files, or the harness's own archive.
type Trasher interface {
	Files(s SessionRef) []string
}

// Archiver archives with the harness's own API.
type Archiver interface {
	Archive(ctx context.Context, s SessionRef) (core.JsonObject, error)
	Unarchive(ctx context.Context, s SessionRef, receipt core.JsonObject) error
}

// Connector is one harness storage root: a source, maybe a sink and a trash.
type Connector struct {
	ID           string
	Harness      string
	Root         string
	ProfileLabel string
	ProfileKind  string
	Variant      string
	Default      bool
	Version      string
	Capabilities core.Capabilities
	Vocabulary   core.ToolVocabulary
	Source       Source
	Sink         Sink
	Trash        any // Trasher and/or Archiver
	Dispose      func()
}

// ConnectorState is a registered connector and how it is doing.
type ConnectorState struct {
	Connector *Connector
	Status    string // ok | failed | disabled
	Error     string
}

// Registry hosts connectors; one failing marks only that one failed.
type Registry struct {
	mu    sync.Mutex
	items map[string]*ConnectorState
	order []string
}

func NewRegistry() *Registry { return &Registry{items: map[string]*ConnectorState{}} }

func (r *Registry) Register(c *Connector) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[c.ID]; ok {
		return fmt.Errorf("connector %s already registered", c.ID)
	}
	r.items[c.ID] = &ConnectorState{Connector: c, Status: "ok"}
	r.order = append(r.order, c.ID)
	return nil
}

func (r *Registry) Unregister(id string) {
	r.mu.Lock()
	s := r.items[id]
	delete(r.items, id)
	for i, x := range r.order {
		if x == id {
			r.order = append(r.order[:i:i], r.order[i+1:]...)
			break
		}
	}
	r.mu.Unlock()
	if s != nil && s.Connector.Dispose != nil {
		s.Connector.Dispose()
	}
}

func (r *Registry) Get(id string) *Connector {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s := r.stateLocked(id); s != nil {
		return s.Connector
	}
	return nil
}

// Product names are input aliases only; all stored references use actual IDs.
func (r *Registry) stateLocked(id string) *ConnectorState {
	if s := r.items[id]; s != nil {
		return s
	}
	var found *ConnectorState
	for _, s := range r.items {
		if s.Connector.Harness == id && s.Connector.Default {
			if found != nil {
				return nil
			}
			found = s
		}
	}
	return found
}

func (r *Registry) Require(id string) (*Connector, error) {
	if c := r.Get(id); c != nil {
		return c, nil
	}
	r.mu.Lock()
	known := append([]string(nil), r.order...)
	r.mu.Unlock()
	sort.Strings(known)
	return nil, fmt.Errorf("unknown connector %s; known: %v", id, known)
}

func (r *Registry) List() []ConnectorState {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ConnectorState, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, *r.items[id])
	}
	return out
}

// Sources are the connectors that read and are not disabled or failed.
func (r *Registry) Sources() []*Connector {
	var out []*Connector
	for _, s := range r.List() {
		if s.Status == "ok" && s.Connector.Source != nil {
			out = append(out, s.Connector)
		}
	}
	return out
}

func (r *Registry) ByHarness(h string) []*Connector {
	var out []*Connector
	for _, s := range r.List() {
		if s.Connector.Harness == h {
			out = append(out, s.Connector)
		}
	}
	return out
}

func (r *Registry) SetEnabled(id string, on bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.stateLocked(id)
	if s == nil {
		return fmt.Errorf("unknown connector %s", id)
	}
	if on {
		s.Status, s.Error = "ok", ""
	} else {
		s.Status = "disabled"
	}
	return nil
}

func (r *Registry) Enabled(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.stateLocked(id)
	return s == nil || s.Status != "disabled"
}

func (r *Registry) MarkFailed(id string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s := r.items[id]; s != nil && s.Status != "disabled" {
		s.Status, s.Error = "failed", err.Error()
	}
}

func (r *Registry) MarkOK(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s := r.items[id]; s != nil && s.Status != "disabled" {
		s.Status, s.Error = "ok", ""
	}
}

func (r *Registry) Dispose() {
	for _, s := range r.List() {
		r.Unregister(s.Connector.ID)
	}
}
