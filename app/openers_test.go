package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestTerminalArgvKeepsWordsWhole(t *testing.T) {
	list := []Opener{
		{ID: "as", argv: []string{"osascript", "-e", `do script "{cmd_as}"`}},
		{ID: "url", argv: []string{"open", "x://new?path={cwd_url}"}, clip: true},
		{ID: customOpener},
	}
	cmd := `codex resume 'a"b' $(rm -rf ~)`
	argv, _ := terminalArgv(list, "as", "", cmd, "/tmp/my proj")
	want := `do script "cd '/tmp/my proj' && codex resume 'a\"b' $(rm -rf ~)"`
	if len(argv) != 3 || argv[2] != want {
		t.Fatalf("applescript word: %q", argv)
	}
	argv, clip := terminalArgv(list, "url", "", "x", "/tmp/a b&c")
	if !clip || argv[1] != "x://new?path=%2Ftmp%2Fa+b%26c" {
		t.Fatalf("url word: %q %v", argv, clip)
	}
	// A custom template is split on whitespace first, so the substituted line stays one argument.
	argv, _ = terminalArgv(list, customOpener, "kitty --directory {cwd} sh -c {cmd}", "echo hi", "/x y")
	if !reflect.DeepEqual(argv, []string{"kitty", "--directory", "/x y", "sh", "-c", "cd '/x y' && echo hi"}) {
		t.Fatalf("custom: %q", argv)
	}
	if argv, _ := terminalArgv(list, "nope", "", "x", ""); argv != nil {
		t.Fatal("unknown id must not run anything")
	}
	// Without a cwd the line is the command alone and {cwd} is home.
	argv, _ = terminalArgv([]Opener{{ID: "t", argv: []string{"{cwd}", "{cmd}"}}}, "t", "", "ls", "")
	if argv[1] != "ls" || argv[0] == "" {
		t.Fatalf("no cwd: %q", argv)
	}
}

func TestPickFallsBackToInstalled(t *testing.T) {
	list := []Opener{{ID: "a", Installed: false}, {ID: "b", Installed: true}, {ID: customOpener, Installed: true}}
	if pick(list, "a", "") != "b" || pick(list, "", "") != "b" || pick(list, "b", "") != "b" {
		t.Fatal("fallback to first installed")
	}
	if pick(list, customOpener, "  ") != "b" || pick(list, customOpener, "x {path}") != customOpener {
		t.Fatal("custom needs a template")
	}
	if pick([]Opener{{ID: customOpener, Installed: true}}, "", "") != "" {
		t.Fatal("nothing installed")
	}
}

func TestEditorArgv(t *testing.T) {
	list := withDetection([]Opener{{ID: "e", argv: []string{"open", "-b", "x", "{path}"}}})
	if got := editorArgv(list, "e", "", "/a b"); !reflect.DeepEqual(got, []string{"open", "-b", "x", "/a b"}) {
		t.Fatalf("%q", got)
	}
	if got := editorArgv(list, customOpener, "subl -n {path}", "/a b"); strings.Join(got, "|") != "subl|-n|/a b" {
		t.Fatalf("%q", got)
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{"/a/b": "/a/b", "a b": "'a b'", "it's": `'it'\''s'`, "": "''"} {
		if got := shellQuote(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestMergePath(t *testing.T) {
	if got := mergePath("/usr/bin:/bin", "/opt/homebrew/bin:/usr/bin::/x"); got != "/usr/bin:/bin:/opt/homebrew/bin:/x" {
		t.Fatal(got)
	}
	if between("motd\n__M__/a:/b__M__trailer", "__M__") != "/a:/b" || between("no marks", "__M__") != "" {
		t.Fatal("between")
	}
}
