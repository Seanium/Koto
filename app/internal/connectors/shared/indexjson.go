package shared

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/go-json-experiment/json/jsontext"
)

// IndexJSONFields validates an object while retaining views into the caller's
// raw bytes. Large unused JSON strings are never decoded or copied. Duplicate
// keys and invalid UTF-8 follow encoding/json, used by the full decoder.
func IndexJSONFields(raw []byte) (map[string][]byte, error) {
	d := indexJSONDecoder(raw)
	if d.PeekKind() != '{' {
		if _, err := d.ReadValue(); err != nil {
			return nil, err
		}
		return nil, indexJSONEnd(d)
	}
	if _, err := d.ReadToken(); err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for d.PeekKind() != '}' {
		key, err := d.ReadToken()
		if err != nil {
			return nil, err
		}
		name := key.String()
		value, err := d.ReadValue()
		if err != nil {
			return nil, err
		}
		// The decoder's returned value expires on the next read. Keep an
		// independent view of the original input using its documented offset.
		end := int(d.InputOffset())
		out[name] = raw[end-len(value) : end]
	}
	if _, err := d.ReadToken(); err != nil {
		return nil, err
	}
	return out, indexJSONEnd(d)
}

func IndexJSONElements(raw []byte) ([][]byte, error) {
	d := indexJSONDecoder(raw)
	if d.PeekKind() != '[' {
		if _, err := d.ReadValue(); err != nil {
			return nil, err
		}
		return nil, indexJSONEnd(d)
	}
	if _, err := d.ReadToken(); err != nil {
		return nil, err
	}
	var out [][]byte
	for d.PeekKind() != ']' {
		value, err := d.ReadValue()
		if err != nil {
			return nil, err
		}
		end := int(d.InputOffset())
		out = append(out, raw[end-len(value):end])
	}
	if _, err := d.ReadToken(); err != nil {
		return nil, err
	}
	return out, indexJSONEnd(d)
}

// IndexJSONValues decodes selected fields only. The fields must come from a
// successful IndexJSONFields call, so individual values are already validated.
func IndexJSONValues(fields map[string][]byte, names ...string) (map[string]any, error) {
	out := make(map[string]any, min(len(fields), len(names)))
	for _, name := range names {
		if raw, ok := fields[name]; ok {
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
			out[name] = value
		}
	}
	return out, nil
}

func indexJSONDecoder(raw []byte) *jsontext.Decoder {
	return jsontext.NewDecoder(bytes.NewBuffer(raw), jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))
}

func indexJSONEnd(d *jsontext.Decoder) error {
	_, err := d.ReadValue()
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("multiple JSON values in one record")
}
