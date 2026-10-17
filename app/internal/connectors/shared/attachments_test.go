package shared

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/hub"
	"koto/internal/planner"
)

func TestAttachmentReadBoundaries(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "assets")
	data := []byte("private fixture")
	sha := core.SHA256Hex(data)
	file := filepath.Join(dir, sha, "name (1).txt")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	link := func(p string) string {
		return "[附件：name](" + strings.NewReplacer("(", "%28", ")", "%29").Replace(FileURL(p).String()) + ")"
	}
	reads := 0
	put := func(b []byte) core.JsonObject { reads++; return core.JsonObject{"sha256": core.SHA256Hex(b)} }
	p := DecodeAttachmentText(link(file), dir, put)
	if reads != 1 || len(p) != 1 || p[0]["blob"] == nil {
		t.Fatal("valid file was not decoded")
	}
	if err := os.WriteFile(file, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	p = DecodeAttachmentText(link(file), dir, put)
	if reads != 1 || p[0]["blob"] != nil {
		t.Fatal("changed bytes were accepted")
	}
	outside := filepath.Join(root, "private.txt")
	if err := os.WriteFile(outside, data, 0600); err != nil {
		t.Fatal(err)
	}
	p = DecodeAttachmentText(link(outside), dir, put)
	if reads != 1 || p[0]["type"] != "text" {
		t.Fatal("arbitrary file link was followed")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, file); err != nil {
		t.Skip(err)
	}
	p = DecodeAttachmentText(link(file), dir, put)
	if reads != 1 || p[0]["blob"] != nil {
		t.Fatal("symlink escaped session assets")
	}
}

func TestAttachmentCopyRoundTripWithNativePaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "附件 [a] O'Brien & 空格")
	h := hub.New()
	data := []byte("attachment content: 中文\n")
	part := core.Part{"type": "file", "name": "CON.txt", "blob": h.PutBlob(data)}
	input := engine.PlanInput{Hub: h, Projection: &planner.Projection{Items: []planner.Item{{Event: &core.Event{ID: "event"}, Role: "user", Parts: []core.Part{part}}}}}
	prepared, files := PrepareAttachments(input, dir, func(core.Part, string) bool { return false })
	if len(files) != 1 {
		t.Fatalf("missing attachment: %v", files)
	}
	plan := &engine.DeliveryPlan{Extra: core.JsonObject{"attachments": files}}
	if err := ApplyAttachments(plan); err != nil {
		t.Fatal(err)
	}
	link := core.S(prepared.Projection.Items[0].Parts[0]["text"])
	if runtime.GOOS == "windows" && !strings.Contains(link, "file:///") {
		t.Fatalf("invalid drive URI: %s", link)
	}
	decoded := DecodeAttachmentText(link, dir, h.PutBlob)
	if len(decoded) != 1 || decoded[0]["type"] != "file" || decoded[0]["name"] != "_CON.txt" {
		t.Fatalf("cannot restore file from link: %+v", decoded)
	}
	got, ok := h.Blob(core.S(core.Obj(decoded[0]["blob"])["sha256"]))
	if !ok || !reflect.DeepEqual(got, data) {
		t.Fatal("attachment bytes changed")
	}
}
