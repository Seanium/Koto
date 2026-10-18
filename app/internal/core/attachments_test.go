package core

import "testing"

func TestAttachmentQuestionsUseOnlyNativeParent(t *testing.T) {
	path := []*Event{
		{ID: "q1", Kind: "message", Actor: "user"},
		{ID: "f1", Kind: "attachment", Actor: "user", Parents: []string{"q1"}},
		{ID: "f2", Kind: "attachment", Actor: "user", Parents: []string{"f1"}},
		{ID: "a1", Kind: "message", Actor: "assistant", Parents: []string{"f2"}},
		{ID: "wrong", Kind: "attachment", Actor: "user", Parents: []string{"a1"}},
		{ID: "orphan", Kind: "attachment", Actor: "user", Parents: []string{"unloaded"}},
		{ID: "side", Kind: "attachment", Actor: "user", Parents: []string{"q1"}, Attrs: JsonObject{"sidechain": true}},
		{ID: "q2", Kind: "message", Actor: "user", Parents: []string{"a1"}},
		{ID: "f3", Kind: "attachment", Actor: "user", Parents: []string{"q2"}},
	}
	got := AttachmentQuestions(path)
	if len(got) != 3 || got["f1"] != "q1" || got["f2"] != "q1" || got["f3"] != "q2" {
		t.Fatal(got)
	}
}
