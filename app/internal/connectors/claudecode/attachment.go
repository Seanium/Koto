package claudecode

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"koto/internal/core"
	"koto/internal/engine"
)

// attachedFile reads the snapshot Claude actually delivered to the parent model.
// The source path is descriptive: its current disk contents may have changed and
// must never replace the recorded bytes. Other attachment types remain context.
func attachedFile(row core.JsonObject, ctx *engine.DecodeContext) ([]core.Part, core.JsonObject, bool) {
	a := core.Obj(row["attachment"])
	content := core.Obj(a["content"])
	file := core.Obj(content["file"])
	text, present := file["content"].(string)
	_, contentRecorded := file["content"]
	if a["type"] != "file" || content["type"] != "text" || (!contentRecorded && core.S(file["filePath"]) == "") || core.S(row["parentUuid"]) == "" {
		return nil, nil, false
	}
	name := core.S(a["filename"])
	if name == "" {
		name = core.S(file["filePath"])
	}
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "" || name == "." || name == "/" {
		return nil, nil, false
	}
	attrs := core.JsonObject{"attachment": "file"}
	part := core.Part{"type": "file", "name": name, "mediaType": "text/plain"}
	if ctx.IndexOnly {
		return []core.Part{part}, attrs, true
	}
	if !present {
		note := fmt.Sprintf("[File snapshot unavailable in the source session: %s.]", name)
		attrs["attachmentPartial"] = note
		return []core.Part{{"type": "text", "text": note}, part}, attrs, true
	}
	part["blob"] = ctx.Blob([]byte(text))
	start, startOK := core.Num(file["startLine"])
	count, countOK := core.Num(file["numLines"])
	total, totalOK := core.Num(file["totalLines"])
	validRange := startOK && countOK && totalOK && start >= 1 && count >= 0 && total >= 0 && math.Trunc(start) == start && math.Trunc(count) == count && math.Trunc(total) == total
	lines := strings.Count(text, "\n") + 1
	complete := validRange && start == 1 && count == total && (count == float64(lines) || text == "" && count == 0)
	parts := []core.Part{}
	if !complete {
		note := fmt.Sprintf("[File snapshot: %s. Only the bytes recorded in the source session are available; the original file may contain more content.]", name)
		if validRange && count > 0 {
			note = fmt.Sprintf("[File snapshot: %s, lines %g–%g of %g. Only this recorded range is included.]", name, start, start+count-1, total)
		}
		parts = append(parts, core.Part{"type": "text", "text": note})
		attrs["attachmentPartial"] = note
	}
	return append(parts, part), attrs, true
}
