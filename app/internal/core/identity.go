package core

import (
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// IdentityInput is what an event id is computed from (profile ev1, frozen: changing anything that
// alters an id needs a new profile).
type IdentityInput struct {
	Thread     ThreadID
	Parents    []EventID
	Kind       string
	Actor      string
	Parts      []Part
	Replaces   *Replaces
	Supersedes string
}

// Identified is an id with its stored parts (long strings swapped for {"$blob":…}) and those strings.
type Identified struct {
	ID     EventID
	Stored []any
	Blobs  map[string]string
}

const blobKey = "$blob"

var crlf = regexp.MustCompile("\r\n?")

// IdentifyEv1: fields {thread, parents, kind, actor, parts, replaces, supersedes}; RFC 8785 JCS;
// every string inside parts NFC-normalized with CRLF/CR → LF; strings over 16 KiB (UTF-8, after
// normalization) replaced by {"$blob":{"sha256","bytes"}}; time, attrs and provenance never count.
func IdentifyEv1(in IdentityInput) Identified {
	blobs := map[string]string{}
	var nrm func(v any) any
	nrm = func(v any) any {
		switch x := v.(type) {
		case string:
			s := norm.NFC.String(x)
			if strings.ContainsRune(s, '\r') {
				s = crlf.ReplaceAllString(s, "\n")
			}
			if len(s) > 16384 {
				h := SHA256Hex(s)
				blobs[h] = s
				return map[string]any{blobKey: map[string]any{"sha256": h, "bytes": len(s)}}
			}
			return s
		case []any:
			out := make([]any, len(x))
			for i, e := range x {
				out[i] = nrm(e)
			}
			return out
		case []Part:
			out := make([]any, len(x))
			for i, e := range x {
				out[i] = nrm(map[string]any(e))
			}
			return out
		case map[string]any:
			out := make(map[string]any, len(x))
			for k, e := range x {
				out[k] = nrm(e)
			}
			return out
		case []string:
			out := make([]any, len(x))
			for i, e := range x {
				out[i] = nrm(e)
			}
			return out
		default:
			return v
		}
	}
	parts := in.Parts
	if parts == nil {
		parts = []Part{}
	}
	stored := nrm(parts).([]any)
	parents := make([]any, len(in.Parents))
	for i, p := range in.Parents {
		parents[i] = p
	}
	body := map[string]any{"p": "ev1", "thread": in.Thread, "parents": parents, "kind": in.Kind, "actor": in.Actor, "parts": stored}
	if in.Replaces != nil {
		body["replaces"] = map[string]any{"from": in.Replaces.From, "to": in.Replaces.To}
	}
	if in.Supersedes != "" {
		body["supersedes"] = in.Supersedes
	}
	return Identified{ID: "ev1:sha256:" + SHA256Hex(MustCanonicalize(body)), Stored: stored, Blobs: blobs}
}

// Rehydrate swaps $blob refs back for their text.
func Rehydrate(v any, load func(sha string) string) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = Rehydrate(e, load)
		}
		return out
	case map[string]any:
		if ref, ok := x[blobKey].(map[string]any); ok && len(x) == 1 {
			sha, _ := ref["sha256"].(string)
			return load(sha)
		}
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = Rehydrate(e, load)
		}
		return out
	}
	return v
}

// ThreadIDFor a native session that starts a thread of its own.
func ThreadIDFor(harness, session string) ThreadID {
	return "th:" + SHA256Hex("thread\x00" + harness + "\x00" + session)[:32]
}
