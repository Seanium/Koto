package claudecode

import (
	"mime"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/planner"
)

// A plain text file attached to a human prompt is a native attachment snapshot,
// not an Anthropic API document. Keep both its recorded bytes and a new owned
// file, so opening or reading the attachment never depends on the source path.
func prepareTextSnapshots(in engine.PlanInput, dir string) (engine.PlanInput, []any) {
	projection := *in.Projection
	projection.Items = append([]planner.Item(nil), projection.Items...)
	files := []any{}
	seen := map[string]bool{}
	for i, item := range projection.Items {
		if item.Role != "user" || item.Event.Kind != "message" || item.Event.Actor != "user" {
			continue
		}
		parts := append([]core.Part(nil), item.Parts...)
		for j, part := range parts {
			mediaType, _, _ := mime.ParseMediaType(core.S(part["mediaType"]))
			if part["type"] != "file" || !strings.HasPrefix(mediaType, "text/") || part["document"] == true || core.S(part["uri"]) != "" {
				continue
			}
			sha := core.S(core.Obj(part["blob"])["sha256"])
			data, exists := in.Hub.Blob(sha)
			if !exists || !utf8.Valid(data) {
				continue
			}
			path := filepath.Join(dir, sha, shared.AttachmentName(core.S(part["name"])))
			copy := core.Part{}
			for key, value := range part {
				copy[key] = value
			}
			copy["ccSnapshotPath"] = path // plan-local; never written into the native file
			parts[j] = copy
			projection.Items[i].Verbatim = false
			if !seen[path] {
				seen[path] = true
				files = append(files, core.JsonObject{"path": path, "data": data})
			}
		}
		projection.Items[i].Parts = parts
	}
	in.Projection = &projection
	return in, files
}

func textSnapshotRecords(in engine.PlanInput, parts []core.Part) ([]core.Part, []core.O) {
	kept := make([]core.Part, 0, len(parts))
	records := []core.O{}
	for i, part := range parts {
		path := core.S(part["ccSnapshotPath"])
		if path == "" {
			// Only remove a notice we generated for this exact adjacent snapshot.
			// Ordinary user text, including an identical-looking sentence, stays.
			if part["attachmentSnapshotNotice"] == true && i+1 < len(parts) && core.S(parts[i+1]["ccSnapshotPath"]) != "" &&
				core.S(core.Obj(parts[i+1]["snapshot"])["notice"]) == core.S(part["text"]) {
				continue
			}
			kept = append(kept, part)
			continue
		}
		data, exists := in.Hub.Blob(core.S(core.Obj(part["blob"])["sha256"]))
		if !exists { // preparation already checked this; retain the normal fallback
			kept = append(kept, part)
			continue
		}
		text := string(data)
		file := core.JsonObject{"filePath": path, "content": text}
		snapshot, recorded := part["snapshot"].(map[string]any)
		if recorded {
			for _, key := range []string{"startLine", "numLines", "totalLines"} {
				if value, ok := snapshot[key]; ok {
					file[key] = value
				}
			}
		} else {
			lines := strings.Count(text, "\n") + 1
			if text == "" {
				lines = 0
			}
			file["startLine"], file["numLines"], file["totalLines"] = 1, lines, lines
		}
		start, valid := core.Num(file["startLine"])
		if !valid || start < 1 {
			start = 1
		}
		var rendered strings.Builder
		if text != "" {
			for line, content := range strings.Split(text, "\n") {
				rendered.WriteString(strconv.FormatFloat(start+float64(line), 'f', -1, 64))
				rendered.WriteByte('\t')
				rendered.WriteString(content)
				rendered.WriteByte('\n')
			}
		}
		records = append(records, core.O{
			{K: "type", V: "attachment"},
			{K: "attachment", V: core.JsonObject{"type": "file", "filename": path, "displayPath": core.S(part["name"]), "content": core.JsonObject{"type": "text", "file": file}}},
			{K: "renderedRole", V: "system"},
			{K: "rendered", V: []any{
				core.JsonObject{"content": "<system-reminder>\nCalled the Read tool with the following input: " + core.Stringify(core.JsonObject{"file_path": path}) + "\n</system-reminder>"},
				core.JsonObject{"content": "<system-reminder>\nResult of calling the Read tool:\n" + rendered.String() + "</system-reminder>"},
			}},
		})
	}
	return kept, records
}
