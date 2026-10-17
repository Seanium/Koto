package shared

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"koto/internal/core"
	"koto/internal/engine"
	"koto/internal/planner"
)

// AttachmentName is a portable basename; a transcript can never choose a destination directory.
func AttachmentName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.Trim(name, " .")
	if name == "" {
		name = "attachment"
	}
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
		name = "_" + name
	}
	for len(name) > 180 {
		name = string([]rune(name)[:len([]rune(name))-1])
	}
	return name
}

// PrepareAttachments preserves files that have no embedded representation in the target as
// ordinary files beside the NEW transcript. There is no copy ledger or persistent staging area.
func PrepareAttachments(in engine.PlanInput, dir string, native func(core.Part, string) bool) (engine.PlanInput, []any) {
	pr := *in.Projection
	pr.Items = append([]planner.Item{}, pr.Items...)
	pr.Losses = append([]core.Loss{}, pr.Losses...)
	files := []any{}
	seen := map[string]bool{}
	var convert func([]core.Part, string, string) ([]core.Part, bool)
	convert = func(parts []core.Part, role, event string) ([]core.Part, bool) {
		out := make([]core.Part, 0, len(parts))
		changed := false
		for _, p := range parts {
			typ := core.S(p["type"])
			if typ == "tool_result" {
				children, ch := convert(core.Parts(p["parts"]), "tool", event)
				if ch {
					p = clonePart(p)
					p["parts"] = core.PartsJSON(children)
					changed = true
				}
			}
			if typ == "file" && !native(p, role) {
				sha := core.S(core.Obj(p["blob"])["sha256"])
				data, ok := in.Hub.Blob(sha)
				if !ok {
					out = append(out, core.Part{"type": "text", "text": "[附件内容不可用]"})
					pr.Losses = append(pr.Losses, core.Loss{Event: event, Field: "file", Kind: "dropped", Note: "附件内容不可用"})
					changed = true
					continue
				}
				name := AttachmentName(core.S(p["name"]))
				path := filepath.Join(dir, sha, name)
				if !seen[path] {
					seen[path] = true
					files = append(files, core.JsonObject{"path": path, "data": data})
				}
				u := strings.NewReplacer("(", "%28", ")", "%29").Replace(FileURL(path).String())
				label := strings.NewReplacer("[", "（", "]", "）").Replace(name)
				p = core.Part{"type": "text", "text": "[附件：" + label + "](" + u + ")"}
				changed = true
				pr.Losses = append(pr.Losses, core.Loss{Event: event, Field: "file", Kind: "referenced", Note: "目标没有此附件的内嵌格式；已保留完整原文件，使用附件路径读取"})
			}
			out = append(out, p)
		}
		return out, changed
	}
	for i, it := range pr.Items {
		ps, ch := convert(it.Parts, it.Role, it.Event.ID)
		pr.Items[i].Parts = ps
		if ch {
			pr.Items[i].Verbatim = false
		}
	}
	in.Projection = &pr
	return in, files
}
func clonePart(p core.Part) core.Part {
	q := core.Part{}
	for k, v := range p {
		q[k] = v
	}
	return q
}

func ApplyAttachments(plan *engine.DeliveryPlan) error {
	for _, v := range core.Arr(plan.Extra["attachments"]) {
		a := core.Obj(v)
		var b []byte
		switch d := a["data"].(type) {
		case []byte:
			b = d
		case string:
			b = core.Base64(d)
		}
		if err := createExclusive(core.S(a["path"]), b); err != nil {
			return err
		}
	}
	return nil
}

var attachmentLink = regexp.MustCompile(`\[附件：([^\]\n]*)\]\((file://[^)\n]+)\)`)
var assetHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

// DecodeAttachmentText only follows our hash-addressed files inside this session's asset
// directory, with realpath containment and checksum verification. Ordinary message paths/URLs
// are never read. Missing assets remain visible instead of turning into empty text.
func DecodeAttachmentText(text string, dir string, put func([]byte) core.JsonObject) []core.Part {
	out := []core.Part{}
	at := 0
	for _, m := range attachmentLink.FindAllStringSubmatchIndex(text, -1) {
		u, e := url.Parse(text[m[4]:m[5]])
		if e != nil {
			continue
		}
		path, valid := FileURLPath(u)
		if !valid {
			continue
		}
		sha := filepath.Base(filepath.Dir(path))
		rel, e := filepath.Rel(dir, path)
		if e != nil || strings.HasPrefix(rel, "..") || !assetHash.MatchString(sha) || filepath.Dir(filepath.Dir(path)) != filepath.Clean(dir) {
			continue
		}
		if m[0] > at {
			out = append(out, core.Part{"type": "text", "text": text[at:m[0]]})
		}
		p := core.Part{"type": "file", "name": text[m[2]:m[3]], "mediaType": "application/octet-stream"}
		real, e := filepath.EvalSymlinks(path)
		root, re := filepath.EvalSymlinks(dir)
		if e == nil && re == nil && strings.HasPrefix(real, root+string(filepath.Separator)) {
			if st, e := os.Stat(real); e == nil && st.Mode().IsRegular() && st.Size() <= 256*1024*1024 {
				if b, e := os.ReadFile(real); e == nil && core.SHA256Hex(b) == sha {
					p["blob"] = put(b)
					p["mediaType"] = http.DetectContentType(b)
				}
			}
		}
		out = append(out, p)
		at = m[1]
	}
	if at < len(text) {
		out = append(out, core.Part{"type": "text", "text": text[at:]})
	}
	return out
}
