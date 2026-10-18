package cursor

import (
	"encoding/binary"
	"fmt"
)

type wireField struct {
	number  int
	value   []byte
	integer uint64
	kind    int
}
type wireMessage []wireField

// Parse strictly: truncated/overflowing fields must never become empty content.
func parseWire(b []byte) (wireMessage, error) {
	out := wireMessage{}
	for len(b) > 0 {
		key, n := binary.Uvarint(b)
		if n <= 0 || key>>3 == 0 || key>>3 > 1<<29-1 {
			return nil, fmt.Errorf("invalid Cursor protobuf field")
		}
		b = b[n:]
		f := wireField{number: int(key >> 3), kind: int(key & 7)}
		switch f.kind {
		case 0:
			x, n := binary.Uvarint(b)
			if n <= 0 {
				return nil, fmt.Errorf("invalid Cursor protobuf integer")
			}
			f.integer = x
			b = b[n:]
		case 1:
			if len(b) < 8 {
				return nil, fmt.Errorf("truncated Cursor protobuf fixed64")
			}
			f.value = b[:8]
			b = b[8:]
		case 2:
			size, n := binary.Uvarint(b)
			if n <= 0 || size > uint64(len(b)-max(n, 0)) {
				return nil, fmt.Errorf("truncated Cursor protobuf bytes")
			}
			b = b[n:]
			f.value = b[:int(size)]
			b = b[int(size):]
		case 5:
			if len(b) < 4 {
				return nil, fmt.Errorf("truncated Cursor protobuf fixed32")
			}
			f.value = b[:4]
			b = b[4:]
		default:
			return nil, fmt.Errorf("unsupported Cursor protobuf wire kind %d", f.kind)
		}
		out = append(out, f)
	}
	return out, nil
}
func (m wireMessage) all(n int) [][]byte {
	var out [][]byte
	for _, f := range m {
		if f.number == n && f.kind == 2 {
			out = append(out, f.value)
		}
	}
	return out
}
func (m wireMessage) bytes(n int) []byte {
	for _, f := range m {
		if f.number == n && f.kind == 2 {
			return f.value
		}
	}
	return nil
}
func (m wireMessage) text(n int) string { return string(m.bytes(n)) }
func (m wireMessage) integer(n int) uint64 {
	for _, f := range m {
		if f.number == n && f.kind == 0 {
			return f.integer
		}
	}
	return 0
}
func (m wireMessage) has(n int) bool {
	for _, f := range m {
		if f.number == n {
			return true
		}
	}
	return false
}
func (m wireMessage) message(n int) (wireMessage, error) { return parseWire(m.bytes(n)) }

func wireBytes(n int, b []byte) wireField { return wireField{number: n, kind: 2, value: b} }
func (m wireMessage) encode() []byte {
	var b []byte
	for _, f := range m {
		b = binary.AppendUvarint(b, uint64(f.number<<3|f.kind))
		switch f.kind {
		case 0:
			b = binary.AppendUvarint(b, f.integer)
		case 2:
			b = binary.AppendUvarint(b, uint64(len(f.value)))
			b = append(b, f.value...)
		default:
			b = append(b, f.value...)
		}
	}
	return b
}
func (m wireMessage) replace(n int, values ...[]byte) wireMessage {
	out := wireMessage{}
	for _, f := range m {
		if f.number != n {
			out = append(out, f)
		}
	}
	for _, value := range values {
		out = append(out, wireBytes(n, value))
	}
	return out
}
