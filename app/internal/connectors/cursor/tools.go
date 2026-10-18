package cursor

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"koto/internal/core"
	"math"
	"regexp"
	"sort"
	"strings"
)

// Imported tool history remains structured. Cursor's MCP-shaped history can
// display arbitrary tool names without claiming that the target can execute them.
type importedTool struct {
	name, id      string
	args          core.JsonObject
	field, bubble int
	ts            int64
	done          bool
}

func valueWire(v any) (wireMessage, error) {
	switch x := v.(type) {
	case nil:
		return wireMessage{wireInt(1, 0)}, nil
	case bool:
		n := int64(0)
		if x {
			n = 1
		}
		return wireMessage{wireInt(4, n)}, nil
	case string:
		return wireMessage{wireText(3, x)}, nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, fmt.Errorf("non-finite Cursor tool argument")
		}
		b := make([]byte, 8)
		binary.LittleEndian.PutUint64(b, math.Float64bits(x))
		return wireMessage{{number: 2, kind: 1, value: b}}, nil
	case []any:
		m := wireMessage{}
		for _, v := range x {
			b, e := valueWire(v)
			if e != nil {
				return nil, e
			}
			m = append(m, wireBytes(1, b.encode()))
		}
		return wireMessage{wireBytes(6, m.encode())}, nil
	case map[string]any:
		m, e := structWire(x)
		if e != nil {
			return nil, e
		}
		return wireMessage{wireBytes(5, m.encode())}, nil
	default:
		b, e := json.Marshal(x)
		if e != nil {
			return nil, e
		}
		var plain any
		if e = json.Unmarshal(b, &plain); e != nil {
			return nil, e
		}
		return valueWire(plain)
	}
}
func structWire(v core.JsonObject) (wireMessage, error) {
	m := wireMessage{}
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b, e := valueWire(v[k])
		if e != nil {
			return nil, e
		}
		m = append(m, wireBytes(1, wireMessage{wireText(1, k), wireBytes(2, b.encode())}.encode()))
	}
	return m, nil
}
func toolWire(t *importedTool, parts []core.Part, outcome string, raw func(core.Part) ([]byte, string, error)) (wireMessage, error) {
	args := wireMessage{wireText(1, t.name), wireText(3, t.id), wireText(4, "koto-import"), wireText(5, t.name), wireText(9, "koto-import")}
	object, e := structWire(t.args)
	if e != nil {
		return nil, e
	}
	for _, f := range object {
		f.number = 2
		args = append(args, f)
	}
	success := wireMessage{}
	for _, p := range parts {
		switch core.S(p["type"]) {
		case "text":
			success = append(success, wireBytes(1, wireMessage{wireBytes(1, wireMessage{wireText(1, core.S(p["text"]))}.encode())}.encode()))
		case "image":
			b, m, e := raw(p)
			if e != nil {
				return nil, e
			}
			success = append(success, wireBytes(1, wireMessage{wireBytes(2, wireMessage{wireBytes(1, b), wireText(2, m)}.encode())}.encode()))
		default:
			return nil, fmt.Errorf("Cursor imported tool result cannot represent %q natively", core.S(p["type"]))
		}
	}
	if outcome == "error" {
		success = append(success, wireInt(2, 1))
	}
	call := wireMessage{wireBytes(15, wireMessage{wireBytes(1, args.encode()), wireBytes(2, wireMessage{wireBytes(1, success.encode())}.encode())}.encode()), wireText(57, t.id), wireInt(59, t.ts), wireInt(60, t.ts)}
	return call, nil
}
func toolResultText(parts []core.Part) string {
	var a []string
	for _, p := range parts {
		if core.S(p["type"]) == "text" {
			a = append(a, core.S(p["text"]))
		}
	}
	return strings.Join(a, "\n")
}

var nativeToolIDPattern = regexp.MustCompile(`^call_[A-Za-z0-9_]+$`)

func nativeToolID(session, id string) string {
	if len(id) <= 64 && nativeToolIDPattern.MatchString(id) {
		return id
	}
	return "call_" + core.SHA256Hex(session + ":" + id)[:32]
}
