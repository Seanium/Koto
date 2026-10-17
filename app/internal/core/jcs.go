package core

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Canonicalize is RFC 8785 JSON Canonicalization: keys sorted by UTF-16 code units, numbers and
// strings serialized exactly as ECMAScript JSON.stringify does. nil map values are kept as null;
// callers omit a key to leave it out.
func Canonicalize(v any) (string, error) {
	var b strings.Builder
	if err := writeCanonical(&b, v); err != nil {
		return "", err
	}
	return b.String(), nil
}

// MustCanonicalize panics on values JSON cannot hold (only programming errors produce them).
func MustCanonicalize(v any) string {
	s, err := Canonicalize(v)
	if err != nil {
		panic(err)
	}
	return s
}

func writeCanonical(b *strings.Builder, v any) error {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case float64:
		s, err := ESNumber(x)
		if err != nil {
			return err
		}
		b.WriteString(s)
	case float32:
		return writeCanonical(b, float64(x))
	case int:
		return writeCanonical(b, float64(x))
	case int64:
		return writeCanonical(b, float64(x))
	case int32:
		return writeCanonical(b, float64(x))
	case uint64:
		return writeCanonical(b, float64(x))
	case string:
		WriteJSString(b, x)
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCanonical(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case []string:
		arr := make([]any, len(x))
		for i, s := range x {
			arr[i] = s
		}
		return writeCanonical(b, arr)
	case []Part:
		arr := make([]any, len(x))
		for i, p := range x {
			arr[i] = map[string]any(p)
		}
		return writeCanonical(b, arr)
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return utf16Less(keys[i], keys[j]) })
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			WriteJSString(b, k)
			b.WriteByte(':')
			if err := writeCanonical(b, x[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("jcs: unsupported type %T", v)
	}
	return nil
}

func utf16Less(a, b string) bool {
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

const hexDigits = "0123456789abcdef"

// WriteJSString writes s as ECMAScript JSON.stringify would (no HTML escaping, \u00xx for
// control characters). Invalid UTF-8 bytes become U+FFFD, as a JS string read from them would.
func WriteJSString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20:
			b.WriteString(`\u00`)
			b.WriteByte(hexDigits[r>>4])
			b.WriteByte(hexDigits[r&0xf])
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}

// JSString is WriteJSString into a new string.
func JSString(s string) string {
	var b strings.Builder
	WriteJSString(&b, s)
	return b.String()
}

// ESNumber formats a number like ECMAScript Number.prototype.toString(10).
func ESNumber(v float64) (string, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "", fmt.Errorf("jcs: non-finite number %v", v)
	}
	if v == 0 {
		return "0", nil
	}
	neg := v < 0
	if neg {
		v = -v
	}
	// Shortest round-trip digits and exponent: d.ddde±x.
	e := strconv.FormatFloat(v, 'e', -1, 64)
	mant, exp, _ := strings.Cut(e, "e")
	digits := strings.Replace(mant, ".", "", 1)
	x, _ := strconv.Atoi(exp)
	k := len(digits)
	n := x + 1
	var out string
	switch {
	case k <= n && n <= 21:
		out = digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		out = digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		out = "0." + strings.Repeat("0", -n) + digits
	default:
		sign := "+"
		if n-1 < 0 {
			sign = "-"
		}
		ex := n - 1
		if ex < 0 {
			ex = -ex
		}
		if k == 1 {
			out = digits + "e" + sign + strconv.Itoa(ex)
		} else {
			out = digits[:1] + "." + digits[1:] + "e" + sign + strconv.Itoa(ex)
		}
	}
	if neg {
		out = "-" + out
	}
	return out, nil
}
