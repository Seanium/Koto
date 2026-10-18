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

func TestAttachmentWithURIKeepsBytesAcrossTwoCopies(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "原始报告.txt")
	data := []byte("independent attachment 中文🐙\x00\xff")
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	h := hub.New()
	parts := []core.Part{{"type": "file", "name": "原始报告.txt", "mediaType": "application/octet-stream", "uri": FileURL(source).String(), "blob": h.PutBlob(data)}}
	var link, finalDir string
	no := false
	for _, name := range []string{"first", "second"} {
		dir := filepath.Join(root, name)
		event := &core.Event{ID: name, Kind: "message", Actor: "user", Attrs: core.JsonObject{}, Parts: parts}
		projection := planner.Project([]*core.Event{event}, planner.Options{SinkHarness: "pi", ReadBlob: h.Blob, DeferIncomplete: &no, Capabilities: core.Capabilities{Write: &core.WriteCaps{Images: true, Tools: "native"}}})
		prepared, files := PrepareAttachments(engine.PlanInput{Hub: h, Projection: projection}, dir, func(core.Part, string) bool { return false })
		if len(files) != 1 {
			t.Fatalf("%s copy kept a source link instead of independent bytes: %#v", name, prepared.Projection.Items)
		}
		if err := ApplyAttachments(&engine.DeliveryPlan{Extra: core.JsonObject{"attachments": files}}); err != nil {
			t.Fatal(err)
		}
		link = core.S(prepared.Projection.Items[0].Parts[0]["text"])
		h = hub.New()
		parts = DecodeAttachmentText(link, dir, h.PutBlob)
		if len(parts) != 1 || parts[0]["blob"] == nil {
			t.Fatal("copied attachment did not read back")
		}
		// Native readers may keep both the native path and its resolved bytes.
		parts[0]["uri"] = FileURL(core.S(core.Obj(files[0])["path"])).String()
		finalDir = dir
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "first")); err != nil {
		t.Fatal(err)
	}
	fresh := hub.New()
	decoded := DecodeAttachmentText(link, finalDir, fresh.PutBlob)
	if len(decoded) != 1 {
		t.Fatalf("second copy lost attachment: %#v", decoded)
	}
	got, ok := fresh.Blob(core.S(core.Obj(decoded[0]["blob"])["sha256"]))
	if !ok || !reflect.DeepEqual(got, data) {
		t.Fatal("second copy still depends on a previous conversation's files")
	}
}
