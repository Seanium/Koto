package core

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// SHA256Hex of a string or bytes.
func SHA256Hex[T string | []byte](data T) string {
	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:])
}

// ZeroPrefix starts a chain of record hashes.
var ZeroPrefix = "0000000000000000000000000000000000000000000000000000000000000000"

// ChainPrefix: prefix_n = H(prefix_{n-1} ‖ H(record_n)), 32 bytes of state kept with the offset.
func ChainPrefix(prev, recordSHA256Hex string) string {
	a, _ := hex.DecodeString(prev)
	b, _ := hex.DecodeString(recordSHA256Hex)
	h := sha256.New()
	h.Write(a)
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

// CheckpointEvery records, the prefix hash is kept (for locating where a file diverged).
const CheckpointEvery = 256

// IDNamespace is the namespace of every uuid5 this project mints. Frozen.
const IDNamespace = "6f1c2a4e-8b0d-5e3a-9c47-2d5b8e1f0a63"

// UUID5 is RFC 4122 v5 (SHA-1, name-based): deterministic native ids for new sessions and records.
func UUID5(name string, namespace ...string) string {
	ns := IDNamespace
	if len(namespace) > 0 {
		ns = namespace[0]
	}
	nb, _ := hex.DecodeString(stripDashes(ns))
	h := sha1.New()
	h.Write(nb)
	h.Write([]byte(name))
	s := h.Sum(nil)
	s[6] = (s[6] & 0x0f) | 0x50
	s[8] = (s[8] & 0x3f) | 0x80
	x := hex.EncodeToString(s[:16])
	return fmt.Sprintf("%s-%s-%s-%s-%s", x[0:8], x[8:12], x[12:16], x[16:20], x[20:32])
}

func stripDashes(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '-' {
			out = append(out, s[i])
		}
	}
	return string(out)
}

// ShortID is a short deterministic hex id (pi's 8-character entry ids).
func ShortID(name string, length, salt int) string {
	if salt != 0 {
		name = fmt.Sprintf("%s\x00%d", name, salt)
	}
	return SHA256Hex(name)[:length]
}
