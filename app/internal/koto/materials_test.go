package koto

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"koto/internal/core"
	"koto/internal/engine"
)

type material struct {
	name, mime string
	data       []byte
	image      bool
}

func materials(t *testing.T) []material {
	im := image.NewRGBA(image.Rect(0, 0, 32, 24))
	for y := 0; y < 24; y++ {
		for x := 0; x < 32; x++ {
			im.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	out := []material{}
	for _, format := range []string{"png", "jpeg", "gif"} {
		var b bytes.Buffer
		var e error
		switch format {
		case "png":
			e = png.Encode(&b, im)
		case "jpeg":
			e = jpeg.Encode(&b, im, nil)
		case "gif":
			e = gif.Encode(&b, im, nil)
		}
		must(t, e)
		out = append(out, material{"测试 图." + format, "image/" + format, b.Bytes(), true})
	}
	// A complete lossless 1×1 WebP fixture, not a MIME label on arbitrary bytes.
	out = append(out, material{"pixel.webp", "image/webp", core.Base64("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA=="), true})
	out = append(out, material{"报告.pdf", "application/pdf", pdfMaterial(), false}, material{"文本 (α).txt", "text/plain", []byte("雨燕-42\nUnicode: αβγ🙂\n"), false}, material{"data.json", "application/json", []byte("{\"hello\":\"世界\"}"), false}, material{"binary.zip", "application/zip", []byte{'P', 'K', 5, 6, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, false}, material{"audio.wav", "audio/wav", wavMaterial(), false}, material{"empty.txt", "text/plain", []byte{}, false}, material{"../../危险:文件.bin", "application/octet-stream", []byte{0, 255, 13, 1, 2, 3, 4}, false})
	out = append(out, material{"文档.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", docxMaterial(t), false}, material{"large.txt", "text/plain", bytes.Repeat([]byte("完整数据 αβ🙂\n"), 180000), false}, material{"non-utf8.txt", "text/plain", []byte{0xff, 0xfe, 0x41, 0x00}, false})
	return out
}
func materialSource(t *testing.T, s *sandbox, items []material) string {
	parts := []pbf{{2, "附件完整性测试"}}
	for _, m := range items {
		if m.image {
			parts = append(parts, pbf{5, []pbf{{1, base64.StdEncoding.EncodeToString(m.data)}, {2, m.mime}, {3, m.name}}})
		} else {
			parts = append(parts, pbf{9, []pbf{{1, m.mime}, {2, string(m.data)}, {9, m.name}}})
		}
	}
	return writeAgy(t, s.Agy, "materials", []agyStep{{14, 3, []pbf{agyTS(1), {19, parts}}}, modelStep(2, "所有素材均应保留原始字节。")})
}

func assertMaterialBytes(t *testing.T, a *App, harness, session string, items []material) {
	t.Helper()
	report := a.Sessions.Scan(ctx, nil, nil)
	eq(t, len(report.Errors), 0, "material readback discovery: ", report.Errors)
	v, err := a.OpenThread(ctx, harness+":"+session)
	must(t, err)
	b := binding(t, v.Hub, harness, session)
	found := map[string]bool{}
	var walk func([]core.Part)
	walk = func(ps []core.Part) {
		for _, p := range ps {
			if p["type"] == "tool_result" {
				walk(core.Parts(p["parts"]))
			}
			sha := core.S(core.Obj(p["blob"])["sha256"])
			if sha != "" {
				data, yes := v.Hub.Blob(sha)
				ok(t, yes)
				eq(t, core.SHA256Hex(data), sha)
				found[sha] = true
			}
		}
	}
	for _, ev := range v.Hub.Path(b.Leaf) {
		walk(ev.Parts)
	}
	for _, m := range items {
		ok(t, found[core.SHA256Hex(m.data)], "missing bytes for ", m.name)
	}
}

// Every native reader/writer pair must retain the exact attachment bytes, even
// when a target represents a file as a local attachment link rather than inline.
func TestMaterialCopyMatrix(t *testing.T) {
	for _, from := range copyAgents {
		t.Run(from, func(t *testing.T) {
			requireCopyAgent(t, "antigravity", from)
			s := newSandbox(t)
			items := materials(t)
			original := materialSource(t, s, items)
			originalBefore := text(t, original)
			a := s.app(enable(copyAgents...))
			scan(t, a)
			source, session := original, "materials"
			if from != "antigravity" {
				seed := carry(t, a, engine.CarryOptions{Selector: "antigravity:materials", To: from, Cwd: s.Root})
				ok(t, seed.Applied)
				source, session = seed.Plan.Target.Path, seed.Plan.Target.Session
			}
			before := nativeSessionSnapshot(t, source, from, session)
			assertMaterialBytes(t, a, from, session, items)
			for _, to := range copyAgents {
				t.Run(to, func(t *testing.T) {
					requireCopyAgent(t, from, to)
					r := carry(t, a, engine.CarryOptions{Selector: from + ":" + session, To: to, Cwd: s.Root})
					ok(t, r.Applied)
					for _, loss := range r.Plan.Losses {
						ok(t, loss.Kind != "dropped" && loss.Kind != "truncated", "material must not be discarded: ", loss)
					}
					if to == "dsh" {
						dshVerify(t, r.Plan.Target.Path)
					}
					sid := core.S(r.Receipt["session"])
					if sid == "" {
						sid = r.Plan.Target.Session
					}
					assertMaterialBytes(t, a, to, sid, items)
					eq(t, nativeSessionSnapshot(t, source, from, session), before, "source session unchanged")
					_, err := os.Stat(filepath.Join(s.Root, "危险:文件.bin"))
					ok(t, os.IsNotExist(err), "attachment filename must never escape its target directory")
				})
			}
			eq(t, text(t, original), originalBefore, "original fixture remains unchanged")
		})
	}
}

func TestToolMaterialsAndMissingFile(t *testing.T) {
	for _, to := range copyAgents {
		t.Run(to, func(t *testing.T) {
			requireCopyAgent(t, "antigravity", to)
			s := newSandbox(t)
			im := materials(t)[0]
			sid := CCSession
			file := filepath.Join(ccDir(s.Claude), sid+".jsonl")
			writeLines(t, file, []any{
				core.JsonObject{"type": "user", "uuid": "u", "sessionId": sid, "cwd": s.Root, "message": core.JsonObject{"role": "user", "content": "读取图片"}},
				core.JsonObject{"type": "assistant", "uuid": "a", "parentUuid": "u", "sessionId": sid, "message": core.JsonObject{"role": "assistant", "content": []any{core.JsonObject{"type": "tool_use", "id": "call-image", "name": "Read", "input": core.JsonObject{"file_path": "image.png"}}}}},
				core.JsonObject{"type": "user", "uuid": "r", "parentUuid": "a", "sessionId": sid, "message": core.JsonObject{"role": "user", "content": []any{core.JsonObject{"type": "tool_result", "tool_use_id": "call-image", "content": []any{core.JsonObject{"type": "image", "source": core.JsonObject{"type": "base64", "media_type": im.mime, "data": base64.StdEncoding.EncodeToString(im.data)}}}}}}},
				core.JsonObject{"type": "assistant", "uuid": "done", "parentUuid": "r", "sessionId": sid, "message": core.JsonObject{"role": "assistant", "content": []any{core.JsonObject{"type": "text", "text": "收到图片"}}}},
			})
			a := s.app(enable("claude-code", to))
			scan(t, a)
			r := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + sid, To: to, Cwd: s.Root})
			ok(t, r.Applied)
			scan(t, a)
			dst := core.S(r.Receipt["session"])
			if dst == "" {
				dst = r.Plan.Target.Session
			}
			b := binding(t, a.Hub, to, dst)
			found := false
			var walk func([]core.Part)
			walk = func(ps []core.Part) {
				for _, p := range ps {
					if p["type"] == "tool_result" {
						walk(core.Parts(p["parts"]))
					}
					if core.S(core.Obj(p["blob"])["sha256"]) == core.SHA256Hex(im.data) {
						found = true
					}
				}
			}
			for _, ev := range a.Hub.Path(b.Leaf) {
				walk(ev.Parts)
			}
			ok(t, found, "tool-returned image must survive")
		})
	}
	t.Run("missing-side-file", func(t *testing.T) {
		s := newSandbox(t)
		writeCcSession(t, s.Claude, "", true)
		must(t, os.Remove(filepath.Join(ccDir(s.Claude), CCSession, "tool-results", "out1.txt")))
		a := s.app(enable("claude-code", "pi"))
		scan(t, a)
		r := carry(t, a, engine.CarryOptions{Selector: "claude-code:" + CCSession, To: "pi", DryRun: true})
		found := false
		for _, l := range r.Plan.Losses {
			found = found || l.Field == "file" && l.Kind == "dropped"
		}
		ok(t, found, "unavailable complete tool output must be explicitly reported")
	})
}
