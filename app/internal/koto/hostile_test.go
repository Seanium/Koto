package koto

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"koto/internal/connectors/claudecode"
	"koto/internal/connectors/codex"
	"koto/internal/connectors/dsh"
	"koto/internal/connectors/pi"
	"koto/internal/core"
	"koto/internal/engine"
)

func TestOwnSideFile(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	dir := filepath.Join(root, "proj", "sess", "tool-results")
	must(t, os.MkdirAll(dir, 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "out.txt"), []byte("ok"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "secret.txt"), []byte("nope"), 0o644))
	must(t, os.Symlink(filepath.Join(root, "secret.txt"), filepath.Join(dir, "link.txt")))
	must(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))
	ok(t, claudecode.OwnSideFile(dir, filepath.Join(dir, "out.txt")))
	ok(t, !claudecode.OwnSideFile(dir, filepath.Join(dir, "..", "..", "..", "secret.txt")))
	ok(t, !claudecode.OwnSideFile(dir, dir+"/../../../secret.txt"))
	ok(t, !claudecode.OwnSideFile(dir, filepath.Join(dir, "link.txt")), "symlink out of the folder")
	ok(t, !claudecode.OwnSideFile(dir, filepath.Join(dir, "sub")), "not a file")
	ok(t, !claudecode.OwnSideFile(dir, "tool-results/out.txt"), "relative")
	ok(t, !claudecode.OwnSideFile(dir, filepath.Join(dir, "missing.txt")))
	ok(t, !claudecode.OwnSideFile(dir+"-x", filepath.Join(dir, "out.txt")), "prefix of another folder")
}

func TestHostileCwdStaysOneFolderUnder(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	cwds := []string{"../../pwned", "/tmp/x/../..", `a:b\c`, "..", ".", "", "/", "/../../etc", `C:\..\..`, "~/x", `..\..\up`, strings.Repeat("/a", 300)}
	for _, cwd := range cwds {
		for name, enc := range map[string]string{"claude-code": claudecode.ProjectDir(cwd), "pi": pi.CwdDir(cwd), "dsh": dsh.ProjectKey(cwd)} {
			full := filepath.Join(root, enc, "x.jsonl")
			eq(t, filepath.Dir(filepath.Dir(full)), root, name+" "+cwd+" → "+enc)
			ok(t, enc != "." && enc != ".." && !strings.ContainsAny(enc, `/\`), name, " ", cwd, " → ", enc)
		}
	}
	for _, seg := range []string{".", "..", "../x", "a/b", `a\b`} {
		e := dsh.EncodeSegment(seg)
		ok(t, !strings.ContainsAny(e, `/\`) && e != "." && e != "..", seg)
	}
}

func TestProtobufHostile(t *testing.T) {
	ok(t, core.PbParse([]byte{0x0a, 0xff, 0xff, 0xff, 0xff, 0x0f, 1, 2}) == nil, "len 2^32-1")
	ok(t, core.PbParse([]byte{0x0a, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}) == nil, "len ~2^63")
	ok(t, core.PbParse([]byte{0x08, 0x80, 0x80}) == nil, "truncated varint")
	ok(t, core.PbParse([]byte{0x00, 0x01}) == nil, "field 0")
	ok(t, core.PbParse(bytes.Repeat([]byte{0x80}, 64)) == nil)
	f := core.PbParse([]byte{0x08, 0x2a})
	ok(t, len(f) == 1 && f[0].Varint == 42)
}

func TestZstdHostile(t *testing.T) {
	data := []byte(strings.Repeat("hello ", 100))
	frame := core.ZstdCompress(data)
	skip := func(size uint32, payload []byte) []byte {
		h := make([]byte, 8)
		binary.LittleEndian.PutUint32(h, 0x184d2a50)
		binary.LittleEndian.PutUint32(h[4:], size)
		return append(h, payload...)
	}
	// a skippable frame claiming 4 GiB just ends the container
	out, err := core.ZstdDecompressAll(append(append([]byte{}, frame...), skip(0xffffffff, nil)...))
	must(t, err)
	ok(t, bytes.Equal(out, data))
	two := append(append(skip(3, []byte("abc")), frame...), frame...)
	out, err = core.ZstdDecompressAll(two)
	must(t, err)
	ok(t, bytes.Equal(out, append(append([]byte{}, data...), data...)))
	_, err = core.ZstdDecompressAll([]byte("not zstd at all"))
	ok(t, err != nil, "garbage")
	_, err = core.ZstdDecompressAll(frame[:len(frame)-2])
	ok(t, err != nil, "truncated")
}

func TestResumeCommand(t *testing.T) {
	cc := claudecode.New(claudecode.Options{Root: t.TempDir()})
	eq(t, engine.ResumeEnvVar("claude-code"), "KOTO_CLAUDE_CODE_RESUME_CMD")
	jsonEq(t, engine.ResumeArgv(cc, "claude-code", "abc"), []string{"claude", "--resume", "abc"})
	t.Setenv("KOTO_CLAUDE_CODE_RESUME_CMD", "  /opt/my claude/ccw   --resume={id} -v ")
	jsonEq(t, engine.ResumeArgv(cc, "claude-code", "a b; rm -rf ~"), []string{"/opt/my", "claude/ccw", "--resume=a b; rm -rf ~", "-v"})
	eq(t, engine.ShellLine([]string{"ccw", "--resume", "x'; rm -rf ~ #"}), `ccw --resume 'x'\''; rm -rf ~ #'`)
	t.Setenv("KOTO_CLAUDE_CODE_RESUME_CMD", "   ")
	jsonEq(t, engine.ResumeArgv(cc, "claude-code", "abc"), []string{"claude", "--resume", "abc"})
	ok(t, engine.ResumeArgv(nil, "antigravity", "abc") == nil)
	t.Setenv("KOTO_ANTIGRAVITY_RESUME_CMD", "agy resume {id}")
	jsonEq(t, engine.ResumeArgv(nil, "antigravity", "abc"), []string{"agy", "resume", "abc"})
}

func TestCodexBinaryMissing(t *testing.T) {
	s := &codex.AppServer{Bin: filepath.Join(os.TempDir(), "koto-no-such-codex"), Env: map[string]string{}}
	_, err := s.Call("thread/list", map[string]any{})
	ok(t, err != nil && strings.Contains(err.Error(), "cannot start"), err)
	_, err = s.Call("thread/list", map[string]any{})
	ok(t, err != nil && strings.Contains(err.Error(), "cannot start"), "and again: no stale state")
	s.Stop()
}
