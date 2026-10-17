// Package legal carries complete license notices in both standalone executables.
package legal

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"io"
)

// Refresh with node app/scripts/prepare-notices.mjs after changing license sources.
// The generated resource is committed so ordinary go builds need no extra tooling.
//
//go:embed notices.txt.gz
var compressed []byte

// Notices expands on demand. No decompressed copy is retained by the package.
func Notices() (string, error) {
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return "", fmt.Errorf("read embedded notices: %w", err)
	}
	defer reader.Close()
	text, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("expand embedded notices: %w", err)
	}
	return string(text), nil
}
