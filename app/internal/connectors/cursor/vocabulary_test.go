package cursor

import (
	"koto/internal/core"
	"testing"
)

func TestVerifiedVocabularyPreservesSourceArguments(t *testing.T) {
	v := Vocabulary{}
	for _, tc := range []struct {
		name     string
		args     core.JsonObject
		lossless bool
	}{
		{"Read", core.JsonObject{"path": "/项目/材料.txt"}, true},
		{"Glob", core.JsonObject{"glob_pattern": "**/*.txt", "target_directory": "/项目"}, true},
		{"Glob", core.JsonObject{"glob_pattern": "*.md", "target_directory": nil}, true},
		{"Read", core.JsonObject{"path": "/a", "offset": float64(4)}, false},
		{"Read", core.JsonObject{"path": "/a", "limit": float64(2)}, false},
		{"Glob", core.JsonObject{"glob_pattern": "*", "exclude": "private"}, false},
		// Do not claim an unverified alias or unknown schema is reversible.
		{"ReadFile", core.JsonObject{"path": "/a"}, false},
		{"Read", core.JsonObject{"path": float64(1)}, false},
		{"Glob", core.JsonObject{"glob_pattern": ""}, false},
		{"Unknown", core.JsonObject{"path": "/a"}, false},
	} {
		p := core.Part{"rawName": tc.name, "args": tc.args, "tool": v.ToCanonical(tc.name, tc.args)}
		if got := core.CanonicalIsLossless(p, v); got != tc.lossless {
			t.Errorf("%s %#v: lossless=%v, want %v", tc.name, tc.args, got, tc.lossless)
		}
	}
}

func TestVocabularyDoesNotClaimUnsupportedReadRange(t *testing.T) {
	if _, ok := (Vocabulary{}).FromCanonical(core.JsonObject{"op": "read", "path": "/a", "range": []any{float64(2), float64(4)}}); ok {
		t.Fatal("unsupported partial read claimed as a full-file read")
	}
}
