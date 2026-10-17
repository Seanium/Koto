package engine

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestPowerShellResumeHelper(t *testing.T) {
	if os.Getenv("KOTO_RESUME_TEST_HELPER") != "1" {
		return
	}
	dir, _ := os.Getwd()
	start := 0
	for i, arg := range os.Args {
		if arg == "--" {
			start = i + 1
			break
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"cwd": dir, "env": os.Getenv("KOTO_RESUME_TEST_ROOT"), "args": os.Args[start:]})
	os.Exit(0)
}

// Run the returned command in the real shell, including apostrophes, Unicode,
// metacharacters and a spaced project path. Windows CI must have PowerShell.
func TestWindowsResumeCommandExecutesLiterally(t *testing.T) {
	shell := os.Getenv("KOTO_TEST_POWERSHELL")
	if shell == "" && runtime.GOOS == "windows" {
		shell = "powershell.exe"
	}
	if shell == "" {
		t.Skip("requires native Windows PowerShell or KOTO_TEST_POWERSHELL")
	}
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "项目 [copy] O'Brien & $env")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	args := []string{"session O'Brien & $HOME; (literal)", `C:\Users\测试\a b.jsonl`}
	argv := append([]string{bin, "-test.run=^TestPowerShellResumeHelper$", "--"}, args...)
	root := filepath.Join(dir, "root %PATH% ' & $")
	line := shellCommand(argv, dir, map[string]string{"KOTO_RESUME_TEST_HELPER": "1", "KOTO_RESUME_TEST_ROOT": root}, "windows")
	encoded := func(s string) string {
		words := utf16.Encode([]rune(s))
		bytes := make([]byte, len(words)*2)
		for i, word := range words {
			binary.LittleEndian.PutUint16(bytes[i*2:], word)
		}
		return base64.StdEncoding.EncodeToString(bytes)
	}
	out, err := exec.Command(shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", encoded(line)).CombinedOutput()
	if err != nil {
		t.Fatalf("PowerShell resume: %v\n%s", err, out)
	}
	var got struct {
		Cwd  string   `json:"cwd"`
		Env  string   `json:"env"`
		Args []string `json:"args"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("readback: %v\n%s", err, out)
	}
	wantDir, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	actualDir, err := os.Stat(got.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(wantDir, actualDir) || got.Env != root || !reflect.DeepEqual(got.Args, args) {
		t.Fatalf("changed command values: %+v", got)
	}
	bad := shellCommand(argv, filepath.Join(dir, "absent"), map[string]string{"KOTO_RESUME_TEST_HELPER": "1"}, "windows")
	out, err = exec.Command(shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", encoded(bad)).CombinedOutput()
	if err == nil || strings.Contains(string(out), `"cwd"`) {
		t.Fatalf("launched target after failed cd: %v\n%s", err, out)
	}
}

func TestWindowsResumeUsesNPMCommandLauncher(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("requires Windows command and PowerShell launchers")
	}
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "Agent tools 用户")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	// npm installs both launchers. A bare PowerShell command prefers .ps1,
	// which commonly fails under the default execution policy.
	if err := os.WriteFile(filepath.Join(dir, "test-agent.ps1"), []byte("throw 'wrong launcher'"), 0600); err != nil {
		t.Fatal(err)
	}
	batch := "@\"" + bin + "\" -test.run=^TestPowerShellResumeHelper$ -- %*\r\n"
	if err := os.WriteFile(filepath.Join(dir, "test-agent.cmd"), []byte(batch), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	args := []string{"--session", filepath.Join(dir, "会话 O'Brien.jsonl")}
	line := ShellCommand(append([]string{"test-agent"}, args...), dir, map[string]string{"KOTO_RESUME_TEST_HELPER": "1"})
	out, err := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", line).CombinedOutput()
	if err != nil {
		t.Fatalf("npm-style resume: %v\n%s", err, out)
	}
	var got struct {
		Args []string `json:"args"`
	}
	if err := json.Unmarshal(out, &got); err != nil || !reflect.DeepEqual(got.Args, args) {
		t.Fatalf("npm launcher readback: %v, %s", err, out)
	}
}
