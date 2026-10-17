package core

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math"
	"strings"
)

// Str is v as a string, or "" and false.
func Str(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// S is v as a string, or "".
func S(v any) string {
	s, _ := v.(string)
	return s
}

// Num is v as a finite number.
func Num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		if math.IsInf(x, 0) || math.IsNaN(x) {
			return 0, false
		}
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

// Obj is v as an object, or an empty one (never nil, so lookups are safe).
func Obj(v any) map[string]any {
	if m, ok := v.(map[string]any); ok && m != nil {
		return m
	}
	return map[string]any{}
}

// Arr is v as an array, or nil.
func Arr(v any) []any {
	a, _ := v.([]any)
	return a
}

// ParseJSON decodes like JSON.parse: numbers become float64.
func ParseJSON(b []byte) (any, error) {
	var v any
	err := json.Unmarshal(b, &v)
	return v, err
}

// ToJSON encodes like JSON.stringify: object keys in insertion order are not kept by Go maps, so
// keys come out sorted; strings are not HTML-escaped.
func ToJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "null"
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// Parts converts a JSON array of objects to []Part.
func Parts(v any) []Part {
	switch x := v.(type) {
	case []Part:
		return x
	case []any:
		out := make([]Part, 0, len(x))
		for _, e := range x {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

// PartsJSON converts []Part to a JSON array.
func PartsJSON(ps []Part) []any {
	out := make([]any, len(ps))
	for i, p := range ps {
		out[i] = map[string]any(p)
	}
	return out
}

// Clone deep-copies a JSON value.
func Clone(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = Clone(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = Clone(e)
		}
		return out
	}
	return v
}

// SameJSON compares two JSON values structurally.
func SameJSON(a, b any) bool {
	sa, err1 := Canonicalize(a)
	sb, err2 := Canonicalize(b)
	return err1 == nil && err2 == nil && sa == sb
}

// Base64 decodes leniently, as Node's Buffer.from(s, 'base64') does (padding optional, URL-safe
// alphabet accepted, whitespace ignored).
func Base64(s string) []byte {
	s = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\n', '\r', '\t':
			return -1
		case '-':
			return '+'
		case '_':
			return '/'
		}
		return r
	}, s)
	s = strings.TrimRight(s, "=")
	b, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil {
		// keep what decodes, as Node does with trailing garbage
		for n := len(s) - 1; n > 0; n-- {
			if b, err = base64.RawStdEncoding.DecodeString(s[:n]); err == nil {
				return b
			}
		}
		return nil
	}
	return b
}
