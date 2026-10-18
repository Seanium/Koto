package koto

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func TestBriefUTF16SlicingKeepsExistingSemantics(t *testing.T) {
	for _, s := range []string{"", "hello", "中文🐙abc", "a\xffz"} {
		u := utf16.Encode([]rune(s))
		if got := jsLen(s); got != len(u) {
			t.Fatalf("length %q: %d", s, got)
		}
		for n := 0; n <= len(u)+1; n++ {
			want := s
			if n < len(u) {
				want = string(utf16.Decode(u[:n]))
			}
			if got := jsSlice(s, n); got != want {
				t.Fatalf("slice %q %d: %q != %q", s, n, got, want)
			}
		}
	}
	large := strings.Repeat("big tool output ", 100000)
	if n := testing.AllocsPerRun(10, func() { _ = jsLen(large) }); n != 0 {
		t.Fatalf("length allocated %v times", n)
	}
}
