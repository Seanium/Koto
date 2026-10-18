package engine

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
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
	input := ""
	if os.Getenv("KOTO_RESUME_TEST_STDIN") == "1" {
		b, _ := io.ReadAll(os.Stdin)
		input = string(b)
	}
	if out := os.Getenv("KOTO_RESUME_TEST_FILE"); out != "" {
		time.Sleep(150 * time.Millisecond)
		b, _ := json.Marshal(map[string]any{"cwd": dir, "args": os.Args[start:], "input": input})
		if err := os.WriteFile(out, b, 0600); err != nil {
			os.Exit(24)
		}
		os.Exit(23)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"cwd": dir, "env": os.Getenv("KOTO_RESUME_TEST_ROOT"), "args": os.Args[start:], "input": input})
	os.Exit(0)
}

func TestWindowsGUIResumeWaitsAndKeepsArguments(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("requires a native Windows GUI-subsystem executable")
	}
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	// A private copy of this test helper models Electron's GUI subsystem. No
	// signed installed application or original test executable is modified.
	pe := int(binary.LittleEndian.Uint32(image[0x3c:]))
	if string(image[pe:pe+4]) != "PE\x00\x00" {
		t.Fatal("test executable has no PE header")
	}
	binary.LittleEndian.PutUint16(image[pe+24+68:], 2)
	dir := filepath.Join(t.TempDir(), "项目 O'Brien [copy]")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	gui := filepath.Join(dir, "GUI CLI.exe")
	if err := os.WriteFile(gui, image, 0700); err != nil {
		t.Fatal(err)
	}
	args := []string{"", "中文🐙 O'Brien & $HOME; [literal]", `quote"inside`, `C:\space path\`, `two\\"quote`, "line\nbreak"}
	appended := `appended "literal" \\`
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		t.Run(shell, func(t *testing.T) {
			if _, err := exec.LookPath(shell); err != nil {
				t.Skip("shell not installed")
			}
			out := filepath.Join(dir, shell+"-result.json")
			argv := append([]string{gui, "-test.run=^TestPowerShellResumeHelper$", "--"}, args...)
			line := shellCommand(argv, dir, map[string]string{"ELECTRON_RUN_AS_NODE": "1", "KOTO_RESUME_TEST_HELPER": "1", "KOTO_RESUME_TEST_FILE": out}, "windows")
			script := line + " " + PowerShellQ(appended) + "; if ($LASTEXITCODE -ne 23 -or -not (Test-Path -LiteralPath " + PowerShellQ(out) + ")) { throw 'GUI command returned before child completion' }"
			words := utf16.Encode([]rune(script))
			encoded := make([]byte, len(words)*2)
			for i, word := range words {
				binary.LittleEndian.PutUint16(encoded[i*2:], word)
			}
			if output, err := exec.Command(shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(encoded)).CombinedOutput(); err != nil {
				t.Fatalf("GUI resume: %v\n%s", err, output)
			}
			b, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Args []string `json:"args"`
				Cwd  string   `json:"cwd"`
			}
			decodeErr := json.Unmarshal(b, &got)
			wantArgs := append(args, appended)
			argsEqual := reflect.DeepEqual(got.Args, wantArgs)
			cwdEqual := sameResumeDirectory(got.Cwd, dir)
			if diagnosticDir := os.Getenv("KOTO_RESUME_TEST_DIAGNOSTICS"); diagnosticDir != "" {
				diagnostic, _ := json.MarshalIndent(map[string]any{"architecture": runtime.GOARCH, "shell": shell, "argsEqual": argsEqual, "cwdEqual": cwdEqual, "wantedArgs": wantArgs, "gotArgs": got.Args, "wantedCwd": dir, "gotCwd": got.Cwd}, "", "  ")
				if err := os.WriteFile(filepath.Join(diagnosticDir, "gui-resume-"+runtime.GOARCH+"-"+shell+".json"), diagnostic, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if decodeErr != nil || !argsEqual || !cwdEqual {
				t.Fatalf("GUI command changed arguments/directory: decode=%v argsEqual=%v cwdEqual=%v wantedArgs=%s gotArgs=%s wantedCwd=%s gotCwd=%s", decodeErr, argsEqual, cwdEqual, strconv.QuoteToASCII(resumeArgsJSON(wantArgs)), strconv.QuoteToASCII(resumeArgsJSON(got.Args)), strconv.QuoteToASCII(dir), strconv.QuoteToASCII(got.Cwd))
			}
		})
	}
}

func sameResumeDirectory(a, b string) bool {
	left, leftErr := os.Stat(a)
	right, rightErr := os.Stat(b)
	return leftErr == nil && rightErr == nil && os.SameFile(left, right)
}

func resumeArgsJSON(args []string) string {
	b, _ := json.Marshal(args)
	return string(b)
}

func TestWindowsResumeInputPromptPreservesUTF8(t *testing.T) {
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
	dir := filepath.Join(t.TempDir(), "项目 O'Brien [copy]")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	prompt, input := "Message O'Brien $HOME; [literal]", "请回复 中文🐙 O'Brien & $HOME; 不要改写"
	argv := []string{bin, "-test.run=^TestPowerShellResumeHelper$", "--", "session O'Brien"}
	line := shellCommandWithInputPrompt(argv, dir, map[string]string{"KOTO_RESUME_TEST_HELPER": "1", "KOTO_RESUME_TEST_STDIN": "1"}, "windows", prompt)
	// Start with the BOM-bearing console encoding used by UTF-8 Windows CI
	// consoles. .NET Framework can emit its preamble before OutputEncoding is
	// applied. Replace only keyboard input; assert the native bytes are exact
	// and both caller encoding settings survive the wrapper.
	script := "$ErrorActionPreference='Stop'; [Console]::InputEncoding=[System.Text.Encoding]::UTF8; $before=$OutputEncoding; $inputBefore=[Console]::InputEncoding; function Read-Host { param($Prompt); if ($Prompt -cne " + PowerShellQ(prompt) + ") { throw 'changed prompt' }; " + PowerShellQ(input) + " }; " + line + " 'appended literal'; if (-not [Object]::ReferenceEquals($before,$OutputEncoding)) { throw 'output encoding leaked' }; if ([Console]::InputEncoding.CodePage -ne $inputBefore.CodePage -or [Convert]::ToBase64String([Console]::InputEncoding.GetPreamble()) -cne [Convert]::ToBase64String($inputBefore.GetPreamble())) { throw 'console input encoding leaked' }"
	words := utf16.Encode([]rune(script))
	bytes := make([]byte, len(words)*2)
	for i, word := range words {
		binary.LittleEndian.PutUint16(bytes[i*2:], word)
	}
	out, err := exec.Command(shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(bytes)).Output()
	if err != nil {
		t.Fatalf("PowerShell stdin resume: %v\n%s", err, out)
	}
	var got struct {
		Input string   `json:"input"`
		Args  []string `json:"args"`
		Cwd   string   `json:"cwd"`
	}
	if err := json.Unmarshal(out, &got); err != nil || strings.TrimSuffix(strings.TrimSuffix(got.Input, "\n"), "\r") != input || !reflect.DeepEqual(got.Args, []string{"session O'Brien", "appended literal"}) || !sameResumeDirectory(got.Cwd, dir) {
		t.Fatalf("stdin or command changed: %v, %s", err, out)
	}
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
	// Windows PowerShell can emit first-run module progress as CLIXML on stderr.
	// The helper's JSON contract is stdout; progress must not corrupt its readback.
	out, err := exec.Command(shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", encoded(line)).Output()
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
	out, err := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", line).Output()
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
