package core

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestJCS(t *testing.T) {
	got := MustCanonicalize(map[string]any{"numbers": []any{333333333.33333329, 1e30, 4.50, 2e-3, 0.000000000000000000000000001}})
	if got != `{"numbers":[333333333.3333333,1e+30,4.5,0.002,1e-27]}` {
		t.Fatal(got)
	}
	got = MustCanonicalize(map[string]any{"€": "Euro Sign", "\r": "Carriage Return", "1": "One", "\u0080": "Control", "😀": "Smiley", "ö": "Latin Small Letter O With Diaeresis"})
	want := "{\"\\r\":\"Carriage Return\",\"1\":\"One\",\"\u0080\":\"Control\",\"ö\":\"Latin Small Letter O With Diaeresis\",\"€\":\"Euro Sign\",\"😀\":\"Smiley\"}"
	if got != want {
		t.Fatal(got)
	}
	if MustCanonicalize(map[string]any{"a": []any{1, nil, true}}) != `{"a":[1,null,true]}` {
		t.Fatal("array")
	}
	for in, want := range map[float64]string{1e21: "1e+21", 123e-20: "1.23e-18", -0.5: "-0.5", 1e-7: "1e-7", 0.000001: "0.000001", 100: "100", 9007199254740993: "9007199254740992"} {
		if s, _ := ESNumber(in); s != want {
			t.Errorf("%v: %s want %s", in, s, want)
		}
	}
}

func TestEv1Vectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/ev1.json")
	if err != nil {
		t.Fatal(err)
	}
	var vs []struct {
		Name  string `json:"name"`
		Input struct {
			Thread     string    `json:"thread"`
			Parents    []string  `json:"parents"`
			Kind       string    `json:"kind"`
			Actor      string    `json:"actor"`
			Parts      []Part    `json:"parts"`
			Replaces   *Replaces `json:"replaces"`
			Supersedes string    `json:"supersedes"`
		} `json:"input"`
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &vs); err != nil {
		t.Fatal(err)
	}
	if len(vs) < 6 {
		t.Fatal("too few vectors")
	}
	for _, v := range vs {
		in := v.Input
		got := IdentifyEv1(IdentityInput{Thread: in.Thread, Parents: in.Parents, Kind: in.Kind, Actor: in.Actor, Parts: in.Parts, Replaces: in.Replaces, Supersedes: in.Supersedes})
		if got.ID != v.ID {
			t.Errorf("%s: %s want %s", v.Name, got.ID, v.ID)
		}
	}
}

func TestIdentityNormalizes(t *testing.T) {
	base := func(thread string, parents []string, text string) string {
		return IdentifyEv1(IdentityInput{Thread: thread, Parents: parents, Kind: "message", Actor: "user", Parts: []Part{{"type": "text", "text": text}}}).ID
	}
	a := base("th:x", nil, "Cafe\u0301\r\nline")
	if a != base("th:x", nil, "Café\nline") {
		t.Fatal("NFC + CRLF")
	}
	if a == base("th:y", nil, "Café\nline") || a == base("th:x", []string{"ev1:sha256:p"}, "Café\nline") {
		t.Fatal("thread and parents participate")
	}
	long := strings.Repeat("x", 20000)
	r := IdentifyEv1(IdentityInput{Thread: "th:x", Kind: "tool_result", Actor: "tool", Parts: []Part{{"type": "tool_result", "callId": "c", "outcome": "success", "parts": []any{map[string]any{"type": "text", "text": long}}}}})
	if len(r.Blobs) != 1 || r.Blobs[SHA256Hex(long)] != long {
		t.Fatal("blob")
	}
	back := Rehydrate(r.Stored, func(s string) string { return r.Blobs[s] })
	if MustCanonicalize(back) != MustCanonicalize([]any{map[string]any{"type": "tool_result", "callId": "c", "outcome": "success", "parts": []any{map[string]any{"type": "text", "text": long}}}}) {
		t.Fatal("rehydrate")
	}
}

func TestHashes(t *testing.T) {
	if UUID5("python.org", "6ba7b810-9dad-11d1-80b4-00c04fd430c8") != "886313e1-3b8a-5372-9b90-0c9aee199e5d" {
		t.Fatal("uuid5")
	}
	p1 := ChainPrefix(ZeroPrefix, SHA256Hex("a"))
	if ChainPrefix(p1, SHA256Hex("b")) == ChainPrefix(ChainPrefix(ZeroPrefix, SHA256Hex("b")), SHA256Hex("a")) {
		t.Fatal("order")
	}
	if !strings.HasPrefix(ThreadIDFor("pi", "s"), "th:") || len(ThreadIDFor("pi", "s")) != 35 {
		t.Fatal("thread id")
	}
}
