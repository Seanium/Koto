// Package core is koto's canonical data model (design §4): plain JSON-shaped data, so the
// identity of an event is the same whatever reads it.
//
// Parts, tool calls, attrs and metadata are JSON values (map[string]any, []any, string,
// float64, bool, nil) built with exactly the keys the model names: event ids hash them (ev1),
// and an id must not change with the language that computes it.
package core

import (
	"encoding/json"
	"strconv"
)

// Json is any JSON value: nil, bool, float64 (or an int built locally), string, []any, map[string]any.
type Json = any

// JsonObject is a JSON object.
type JsonObject = map[string]any

// Part is one piece of an event's content: {type: text|image|file|tool_call|tool_result|reasoning|opaque, …}.
type Part = map[string]any

// EventID is `ev1:sha256:<hex>`; ThreadID `th:<hex>`.
type (
	EventID  = string
	ThreadID = string
)

// Provenance says where an event was read.
type Provenance struct {
	Source   Source    `json:"source"`
	Native   Native    `json:"native"`
	Identity string    `json:"identity"` // native | location | content | synthetic
	Residual *Residual `json:"residual,omitempty"`
}

type Source struct {
	Connector string `json:"connector"`
	Harness   string `json:"harness"`
	Version   string `json:"version"`
	Frontend  string `json:"frontend,omitempty"`
}

type Native struct {
	Session string `json:"session"`
	ID      string `json:"id"`
	Index   *int   `json:"index,omitempty"`
	Offset  *int64 `json:"offset,omitempty"`
}

// Residual is the raw native record (its hash, and a copy when kept).
type Residual struct {
	SHA256 string     `json:"sha256"`
	Bytes  int        `json:"bytes"`
	Blob   JsonObject `json:"blob,omitempty"`
}

// Replaces is a logical replacement of a range on the path (compaction, context edit).
type Replaces struct {
	From EventID `json:"from"`
	To   EventID `json:"to"`
}

type Event struct {
	ID         EventID    `json:"id"`
	Parents    []EventID  `json:"parents"`
	Thread     ThreadID   `json:"thread"`
	Kind       string     `json:"kind"`
	Actor      string     `json:"actor"`
	Parts      []Part     `json:"parts"`
	Replaces   *Replaces  `json:"replaces,omitempty"`
	Supersedes string     `json:"supersedes,omitempty"`
	Time       *float64   `json:"time,omitempty"`
	Attrs      JsonObject `json:"attrs,omitempty"`
	Provenance Provenance `json:"provenance"`
}

// TimeMs is the observation time in ms, or 0.
func (e *Event) TimeMs() float64 {
	if e == nil || e.Time == nil {
		return 0
	}
	return *e.Time
}

// ParentRef names a draft's parent. A nil *ParentRef means "the previous event-producing record
// of the same session"; Null a root; otherwise a native record (in this or another session) or an event.
type ParentRef struct {
	Null    bool    `json:"-"`
	Native  string  `json:"native,omitempty"`
	Session string  `json:"session,omitempty"`
	Harness string  `json:"harness,omitempty"`
	Event   EventID `json:"event,omitempty"`
}

// NullParent is an explicit root.
func NullParent() *ParentRef { return &ParentRef{Null: true} }

// NativeParent points at a native record of the same session.
func NativeParent(id string) *ParentRef { return &ParentRef{Native: id} }

// DraftReplaces is a draft's replacement range: From nil = the root of To's path; ToExclusive = ends at To's parent.
type DraftReplaces struct {
	From        *ParentRef
	To          *ParentRef
	ToExclusive bool
}

type DraftEvent struct {
	NativeID string
	Parent   *ParentRef
	Kind     string
	Actor    string
	Parts    []Part
	Replaces *DraftReplaces
	Time     *float64
	Attrs    JsonObject
	Identity string
}

// NativeRef is one native session found by a connector. Session is its stable id, never a path.
type NativeRef struct {
	Harness   string     `json:"harness"`
	Connector string     `json:"connector"`
	Session   string     `json:"session"`
	Path      string     `json:"path,omitempty"`
	Size      int64      `json:"size,omitempty"`
	MtimeMs   float64    `json:"mtimeMs,omitempty"`
	Meta      JsonObject `json:"meta,omitempty"`
}

// BytePosition is where a byte-stream source stopped.
type BytePosition struct {
	Kind      string  `json:"kind"` // byte | seq
	FileID    string  `json:"fileId,omitempty"`
	ByteOff   int64   `json:"byteOff,omitempty"`
	LastStart int64   `json:"lastStart,omitempty"`
	LastHash  string  `json:"lastHash,omitempty"`
	FileSize  int64   `json:"fileSize,omitempty"`
	MtimeMs   float64 `json:"mtimeMs,omitempty"`
	Seq       int64   `json:"seq,omitempty"`
}

// SourceOffset is how far a session was read: records, the chained prefix hash and decoder state.
type SourceOffset struct {
	Position      BytePosition `json:"position"`
	Records       int          `json:"records"`
	PrefixHash    string       `json:"prefixHash"`
	Checkpoints   []Checkpoint `json:"checkpoints,omitempty"`
	FormatVersion string       `json:"formatVersion"`
	State         JsonObject   `json:"state,omitempty"`
	Witness       string       `json:"witness,omitempty"`
	WitnessStable bool         `json:"witnessStable,omitempty"`
}

// Lineage links a native session to the one it was forked, continued or spawned from.
type Lineage struct {
	Kind        string `json:"kind"` // fork | continue | spawn
	Harness     string `json:"harness"`
	Session     string `json:"session"`
	AfterNative string `json:"afterNative,omitempty"`
	Copied      bool   `json:"copied,omitempty"`
}

type BatchRecord struct {
	NativeID string
	SHA256   string
	Bytes    int
	Offset   *int64
	Index    int
	Raw      []byte
	Alias    *ParentRef
}

// SourceBatch is what one read of a session yields.
type SourceBatch struct {
	Ref       NativeRef
	Drafts    []DraftEvent
	Records   []BatchRecord
	Blobs     [][]byte
	Offset    SourceOffset
	Lineage   *Lineage
	Meta      JsonObject
	Rewritten *struct{ AtRecord int }
}

// Loss is something a delivery could not carry as it was.
type Loss struct {
	Sink  string  `json:"sink"`
	Event EventID `json:"event"`
	Field string  `json:"field"`
	Kind  string  `json:"kind"` // dropped | textualized | truncated | approximated
	Note  string  `json:"note,omitempty"`
}

// Capabilities of a connector (design §6.2), as JSON so they reach the UI unchanged.
type Capabilities struct {
	Read           ReadCaps   `json:"read"`
	Write          *WriteCaps `json:"write,omitempty"`
	WriteProfile   string     `json:"writeProfile,omitempty"`
	FormatVersions []string   `json:"formatVersions"`
}

type ReadCaps struct {
	Images     bool   `json:"images"`
	Reasoning  string `json:"reasoning"` // none | plain | sealed
	Subagents  bool   `json:"subagents"`
	Compaction bool   `json:"compaction"`
}

type WriteCaps struct {
	Mode   string `json:"mode"` // native-file | none
	Images bool   `json:"images"`
	// Reasoning: "none" | "plain" | "passthrough", or SameAPI set.
	Reasoning  string     `json:"-"`
	SameAPI    string     `json:"-"`
	Tools      string     `json:"tools"` // native | canonical | text
	Tree       bool       `json:"tree,omitempty"`
	ToolCallID *IDRule    `json:"toolCallId,omitempty"`
	ExtraJSON  JsonObject `json:"-"`
}

type IDRule struct {
	Pattern   string `json:"pattern"`
	MaxLength int    `json:"maxLength"`
}

// Checkpoint is the prefix hash after At records, kept as [at, hash].
type Checkpoint struct {
	At   int
	Hash string
}

func (c Checkpoint) MarshalJSON() ([]byte, error) {
	return []byte("[" + strconv.Itoa(c.At) + "," + JSString(c.Hash) + "]"), nil
}

func (c *Checkpoint) UnmarshalJSON(b []byte) error {
	var a []any
	if err := json.Unmarshal(b, &a); err != nil || len(a) != 2 {
		return err
	}
	n, _ := a[0].(float64)
	c.At, c.Hash = int(n), S(a[1])
	return nil
}

// Clone deep-copies an offset.
func (o SourceOffset) Clone() SourceOffset {
	c := o
	c.Checkpoints = append([]Checkpoint(nil), o.Checkpoints...)
	if o.State != nil {
		c.State = Clone(o.State).(map[string]any)
	}
	return c
}
