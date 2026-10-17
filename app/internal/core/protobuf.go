package core

import (
	"unicode"
	"unicode/utf8"
)

// PbField is one field of a protobuf message read without its schema.
type PbField struct {
	Num    int
	Wire   int
	Varint uint64
	HasInt bool
	Bytes  []byte
}

// PbParse reads the wire format; nil when it is not a well-formed message.
func PbParse(buf []byte) []PbField {
	var out []PbField
	p := 0
	varint := func() (uint64, bool) {
		var x uint64
		var s uint
		for {
			if p >= len(buf) {
				return 0, false
			}
			c := buf[p]
			p++
			if s < 64 {
				x |= uint64(c&0x7f) << s
			}
			s += 7
			if c&0x80 == 0 {
				return x, true
			}
		}
	}
	for p < len(buf) {
		key, ok := varint()
		if !ok {
			return nil
		}
		num, wire := int(key>>3), int(key&7)
		if num == 0 {
			return nil
		}
		switch wire {
		case 0:
			v, ok := varint()
			if !ok {
				return nil
			}
			out = append(out, PbField{Num: num, Wire: wire, Varint: v, HasInt: true})
		case 2:
			l, ok := varint()
			if !ok || l > uint64(len(buf)-p) {
				return nil
			}
			out = append(out, PbField{Num: num, Wire: wire, Bytes: buf[p : p+int(l)]})
			p += int(l)
		case 1:
			p += 8
		case 5:
			p += 4
		default:
			return nil
		}
	}
	return out
}

// Pb is a message view with path accessors: Msg(5, 4) = field 5 → its field 4.
type Pb struct{ Fields []PbField }

func NewPb(buf []byte) Pb {
	if buf == nil {
		return Pb{}
	}
	return Pb{Fields: PbParse(buf)}
}

func (m Pb) All(num int) []PbField {
	var out []PbField
	for _, f := range m.Fields {
		if f.Num == num {
			out = append(out, f)
		}
	}
	return out
}

func (m Pb) first(num int) *PbField {
	for i := range m.Fields {
		if m.Fields[i].Num == num {
			return &m.Fields[i]
		}
	}
	return nil
}

func (m Pb) Msg(path ...int) Pb {
	cur := m
	for _, n := range path {
		f := cur.first(n)
		if f == nil {
			cur = Pb{}
		} else {
			cur = NewPb(f.Bytes)
		}
	}
	return cur
}

func (m Pb) Msgs(num int) []Pb {
	var out []Pb
	for _, f := range m.All(num) {
		out = append(out, NewPb(f.Bytes))
	}
	return out
}

func (m Pb) Str(num int) (string, bool) {
	f := m.first(num)
	if f == nil || f.Bytes == nil {
		return "", false
	}
	return string(f.Bytes), true
}

func (m Pb) Int(num int) (int64, bool) {
	f := m.first(num)
	if f == nil || !f.HasInt {
		return 0, false
	}
	return int64(f.Varint), true
}

// LongestString is the longest valid-UTF-8 string with a letter or digit anywhere below (heuristic fallback).
func (m Pb) LongestString(depth int) string {
	best := ""
	for _, f := range m.Fields {
		if f.Bytes == nil {
			continue
		}
		s := string(f.Bytes)
		if utf8.ValidString(s) && hasLetterOrDigit(s) && len([]rune(s)) > len([]rune(best)) {
			best = s
		}
		if depth > 0 {
			if sub := NewPb(f.Bytes).LongestString(depth - 1); len([]rune(sub)) > len([]rune(best)) {
				best = sub
			}
		}
	}
	return best
}

func hasLetterOrDigit(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return true
		}
	}
	return false
}
