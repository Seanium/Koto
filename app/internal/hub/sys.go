package hub

import (
	"encoding/json"
	"io"
)

func newDecoder(r io.Reader) *json.Decoder { return json.NewDecoder(r) }
