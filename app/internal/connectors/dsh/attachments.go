package dsh

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"koto/internal/connectors/shared"
	"koto/internal/core"
	"koto/internal/engine"
)

var attachmentID = regexp.MustCompile(`^sha256:([0-9a-f]{64})$`)

func attachmentPart(b map[string]any, ctx *engine.DecodeContext) core.Part {
	a := core.Obj(b["attachment"])
	typ := core.S(b["type"])
	p := core.Part{"type": typ, "name": core.S(a["name"]), "mediaType": core.S(a["mediaType"])}
	m := attachmentID.FindStringSubmatch(core.S(a["attachmentId"]))
	if m == nil {
		return p
	}
	root := filepath.Join(core.S(ctx.Ref.Meta["attachmentHome"]), "attachments", "v1")
	kind := "objects"
	if typ == "file" {
		kind = "file-objects"
	}
	path := filepath.Join(root, kind, m[1][:2], m[1])
	real, e := filepath.EvalSymlinks(path)
	base, re := filepath.EvalSymlinks(root)
	if e != nil || re != nil || !strings.HasPrefix(real, base+string(filepath.Separator)) {
		return p
	}
	st, e := os.Stat(real)
	if e != nil || !st.Mode().IsRegular() || st.Size() > 256*1024*1024 {
		return p
	}
	data, ok := ctx.Side(real)
	if !ok || core.SHA256Hex(data) != m[1] {
		return p
	}
	if n, ok := core.Num(a["bytes"]); ok && n != float64(len(data)) {
		return p
	}
	p["blob"] = ctx.Blob(data)
	if p["mediaType"] == "" {
		p["mediaType"] = http.DetectContentType(data)
	}
	return p
}

// The native image store keys the exact image bytes and records dimensions. WebP's three
// standard frame headers can be read without a new runtime/image conversion dependency.
func imageDimensions(data []byte) (int, int, error) {
	if c, _, e := image.DecodeConfig(bytes.NewReader(data)); e == nil {
		return c.Width, c.Height, nil
	}
	if len(data) >= 30 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		u24 := func(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 }
		switch string(data[12:16]) {
		case "VP8X":
			return 1 + u24(data[24:27]), 1 + u24(data[27:30]), nil
		case "VP8 ":
			if string(data[23:26]) == "\x9d\x01\x2a" {
				return int(binary.LittleEndian.Uint16(data[26:28]) & 0x3fff), int(binary.LittleEndian.Uint16(data[28:30]) & 0x3fff), nil
			}
		case "VP8L":
			if data[20] == 0x2f {
				b := binary.LittleEndian.Uint32(data[21:25])
				return int(b&0x3fff) + 1, int((b>>14)&0x3fff) + 1, nil
			}
		}
	}
	return 0, 0, fmt.Errorf("invalid or unsupported image")
}
func prepareAttachments(in engine.PlanInput, home string) (engine.PlanInput, []any, error) {
	pr := *in.Projection
	pr.Items = append(pr.Items[:0:0], pr.Items...)
	files := []any{}
	seen := map[string]bool{}
	var convert func([]core.Part) ([]core.Part, error)
	convert = func(parts []core.Part) ([]core.Part, error) {
		out := make([]core.Part, 0, len(parts))
		for _, p := range parts {
			typ := core.S(p["type"])
			if typ == "tool_result" {
				ps, e := convert(core.Parts(p["parts"]))
				if e != nil {
					return nil, e
				}
				q := core.Part{}
				for k, v := range p {
					q[k] = v
				}
				q["parts"] = core.PartsJSON(ps)
				p = q
			}
			if typ == "file" || typ == "image" {
				sha := core.S(core.Obj(p["blob"])["sha256"])
				data, ok := in.Hub.Blob(sha)
				if !ok {
					return nil, fmt.Errorf("attachment unavailable")
				}
				name := shared.AttachmentName(core.S(p["name"]))
				a := core.JsonObject{"attachmentId": "sha256:" + sha, "bytes": len(data), "name": name}
				kind := "file-objects"
				if typ == "image" {
					w, h, e := imageDimensions(data)
					if e != nil {
						return nil, e
					}
					kind = "objects"
					a["mediaType"] = p["mediaType"]
					a["width"], a["height"] = w, h
				}
				paths := []string{filepath.Join(home, "attachments", "v1", kind, sha[:2], sha)}
				if typ == "file" {
					paths = append(paths, filepath.Join(home, "attachments", "v1", "files", sha[:2], sha, name))
				}
				for _, path := range paths {
					if !seen[path] {
						seen[path] = true
						files = append(files, core.JsonObject{"path": path, "data": data})
					}
				}
				q := core.Part{}
				for k, v := range p {
					q[k] = v
				}
				q["attachment"] = a
				p = q
			}
			out = append(out, p)
		}
		return out, nil
	}
	for i, it := range pr.Items {
		ps, e := convert(it.Parts)
		if e != nil {
			return in, nil, e
		}
		pr.Items[i].Parts = ps
	}
	in.Projection = &pr
	return in, files, nil
}
