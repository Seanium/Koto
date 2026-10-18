package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"koto/internal/core"
	"koto/internal/engine"
)

// Shape observed in Claude Desktop 2.42304 / Code 2.1.299. The rendered
// system-reminder is presentation; content.file is the recorded file snapshot.
func attachmentRecord() core.JsonObject {
	return core.JsonObject{"type": "attachment", "uuid": "file", "parentUuid": "question", "renderedRole": "system", "attachment": core.JsonObject{
		"type": "file", "filename": `C:\native\说明 中文🐙.txt`, "content": core.JsonObject{"type": "text", "file": core.JsonObject{
			"filePath": `C:\native\说明 中文🐙.txt`, "content": "附件口令：海风-7286\n计数：37\n仅供本次 koto 原生复制验收。\n", "startLine": 1, "numLines": 4, "totalLines": 4,
		}},
	}, "rendered": []any{core.JsonObject{"content": "<system-reminder>Called Read and rendered its result.</system-reminder>"}}}
}

func TestNativeFileAttachmentSnapshot(t *testing.T) {
	for _, mode := range []string{"full", "empty", "partial", "unknown-range", "missing", "other", "no-parent"} {
		t.Run(mode, func(t *testing.T) {
			row := attachmentRecord()
			a := core.Obj(row["attachment"])
			f := core.Obj(core.Obj(a["content"])["file"])
			switch mode {
			case "empty":
				f["content"], f["numLines"], f["totalLines"] = "", 0, 0
			case "partial":
				f["content"], f["startLine"], f["numLines"], f["totalLines"] = "仅已有快照🐙\n", 5, 2, 20
			case "unknown-range":
				delete(f, "totalLines")
			case "missing":
				delete(f, "content")
			case "other":
				a["type"] = "directory"
			case "no-parent":
				delete(row, "parentUuid")
			}
			file := filepath.Join(t.TempDir(), "source.jsonl")
			raw, _ := json.Marshal(row)
			if err := os.WriteFile(file, append(raw, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
			read := func(index bool) core.SourceBatch {
				ctx := context.Background()
				if index {
					ctx = engine.WithIndexOnly(ctx)
				}
				var batch core.SourceBatch
				if err := engine.TailJSONL(ctx, core.NativeRef{Path: file, Session: "source"}, nil, Decoder{}, func(b core.SourceBatch) error { batch = b; return nil }, 2000, false); err != nil {
					t.Fatal(err)
				}
				return batch
			}
			full, index := read(false), read(true)
			if len(index.Blobs) != 0 || !reflect.DeepEqual(full.Offset, index.Offset) {
				t.Fatal("index allocated material or changed checkpoint")
			}
			d, light := full.Drafts[0], index.Drafts[0]
			if d.Kind != light.Kind || d.Actor != light.Actor || !reflect.DeepEqual(d.Parent, light.Parent) {
				t.Fatal("light reader changed attachment ownership")
			}
			if mode == "other" || mode == "no-parent" {
				if d.Kind != "context" {
					t.Fatal("unproven context became material")
				}
				return
			}
			if d.Kind != "attachment" || d.Actor != "user" || d.Parent.Native != "question" {
				t.Fatalf("bad event: %+v", d)
			}
			p := d.Parts[len(d.Parts)-1]
			if p["name"] != "说明 中文🐙.txt" {
				t.Fatal("native basename changed", p)
			}
			if mode == "missing" {
				if p["blob"] != nil || len(full.Blobs) != 0 {
					t.Fatal("missing data manufactured as empty")
				}
			} else {
				want := core.S(f["content"])
				if len(full.Blobs) != 1 || string(full.Blobs[0]) != want || core.S(core.Obj(p["blob"])["sha256"]) != core.SHA256Hex([]byte(want)) {
					t.Fatal("snapshot bytes changed")
				}
			}
			partial := mode != "full" && mode != "empty"
			if (core.S(d.Attrs["attachmentPartial"]) != "") != partial {
				t.Fatal("snapshot completeness unreported")
			}
			if mode == "partial" && !strings.Contains(core.S(d.Parts[0]["text"]), "lines 5–6 of 20") {
				t.Fatal("missing original range")
			}
		})
	}
}
