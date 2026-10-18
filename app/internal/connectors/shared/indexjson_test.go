package shared

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestIndexJSONMatchesStandardValues(t *testing.T) {
	for _, input := range []string{
		`{"a":"中文🐙","b":{"nested":[true,null,"\\\"}"]},"a":"last","empty":""}`,
		`{"\u0061":null,"text":"\ud800","n":1.25}`,
		"{\"invalid\":\"\xff\"}",
		`{}`, `null`, `[]`, `"scalar"`,
	} {
		raw := []byte(input)
		before := bytes.Clone(raw)
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		want, _ := value.(map[string]any)
		fields, err := IndexJSONFields(raw)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for name := range fields {
			names = append(names, name)
		}
		got, err := IndexJSONValues(fields, names...)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) {
			t.Fatalf("keys differ: %#v %#v", got, want)
		}
		for key, value := range want {
			if !reflect.DeepEqual(got[key], value) {
				t.Fatalf("value %s differs: %#v %#v", key, got[key], value)
			}
		}
		if !bytes.Equal(raw, before) {
			t.Fatal("input bytes changed")
		}
	}
}

func TestIndexJSONRejectsMalformedAndExtraValues(t *testing.T) {
	for _, raw := range []string{``, `{`, `{"a":}`, `{"a":[1,]}`, `{"a":"\x"}`, `{"a":true} false`, `[] {}`, `{"unused":{"bad":}}`} {
		if _, err := IndexJSONFields([]byte(raw)); err == nil {
			t.Fatalf("accepted %q", raw)
		}
		if _, err := IndexJSONElements([]byte(raw)); err == nil {
			t.Fatalf("accepted array %q", raw)
		}
	}
	fields, err := IndexJSONFields([]byte(`{"n":1e1000}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := IndexJSONValues(fields, "n"); err == nil {
		t.Fatal("accepted unrepresentable selected number")
	}
}

func TestIndexJSONArrayKeepsExactViews(t *testing.T) {
	raw := []byte(` [ {"escaped":"\\[\""} , null, ["中文",1] ] `)
	before := bytes.Clone(raw)
	elements, err := IndexJSONElements(raw)
	if err != nil {
		t.Fatal(err)
	}
	var want []any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if len(elements) != len(want) {
		t.Fatal("array count changed")
	}
	for i, element := range elements {
		var got any
		if err := json.Unmarshal(element, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want[i]) {
			t.Fatal("array value changed")
		}
	}
	if !bytes.Equal(raw, before) {
		t.Fatal("input bytes changed")
	}
}
