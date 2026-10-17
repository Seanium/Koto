package core

import (
	"strings"
	"time"
)

// O is a JSON object that keeps its key order (records koto writes mirror the harness's own
// key order). Later Set of an existing key replaces it in place.
type O []KV

// KV is one member of O.
type KV struct {
	K string
	V any
}

// Set adds or replaces a key.
func (o O) Set(k string, v any) O {
	for i := range o {
		if o[i].K == k {
			o[i].V = v
			return o
		}
	}
	return append(o, KV{k, v})
}

// Get a key's value.
func (o O) Get(k string) (any, bool) {
	for _, kv := range o {
		if kv.K == k {
			return kv.V, true
		}
	}
	return nil, false
}

// Stringify is JSON.stringify: O keeps its order, plain maps come out with sorted keys, strings
// and numbers as ECMAScript writes them.
func Stringify(v any) string {
	var b strings.Builder
	if err := writeJSON(&b, v); err != nil {
		return "null"
	}
	return b.String()
}

func writeJSON(b *strings.Builder, v any) error {
	switch x := v.(type) {
	case O:
		b.WriteByte('{')
		for i, kv := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			WriteJSString(b, kv.K)
			b.WriteByte(':')
			if err := writeJSON(b, kv.V); err != nil {
				return err
			}
		}
		b.WriteByte('}')
		return nil
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeJSON(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
		return nil
	case []O:
		arr := make([]any, len(x))
		for i, e := range x {
			arr[i] = e
		}
		return writeJSON(b, arr)
	case map[string]any:
		// sorted keys, but values may hold O
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sortUTF16(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			WriteJSString(b, k)
			b.WriteByte(':')
			if err := writeJSON(b, x[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
		return nil
	}
	return writeCanonical(b, v)
}

func sortUTF16(keys []string) {
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && utf16Less(keys[j], keys[j-1]); j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
}

// ParseTime is Date.parse for the ISO timestamps harnesses write: ms since epoch, or nil (and nil
// for 0, which JavaScript code treats as "no time").
func ParseTime(s string) *float64 {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000Z07:00", "2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05", "2006-01-02T15:04:05.000", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			ms := float64(t.UnixMilli())
			if ms == 0 {
				return nil
			}
			return &ms
		}
	}
	return nil
}

// F is a pointer to a float (for optional times).
func F(f float64) *float64 { return &f }
