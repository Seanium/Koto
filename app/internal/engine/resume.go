package engine

import (
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// ResumeEnvVar: KOTO_<HARNESS>_RESUME_CMD replaces the connector's command (a binary installed
// under another name). The template is split on whitespace first and {id} substituted per word, so
// a session id is always exactly one argument.
func ResumeEnvVar(harness string) string {
	return "KOTO_" + regexp.MustCompile(`[^A-Z0-9]`).ReplaceAllString(strings.ToUpper(harness), "_") + "_RESUME_CMD"
}

// ResumeArgv is how to continue a native session in its harness, or nil.
func ResumeArgv(c *Connector, harness, session string) []string {
	tpl := strings.TrimSpace(os.Getenv(ResumeEnvVar(harness)))
	if tpl == "" && c != nil && c.Sink != nil {
		tpl = c.Sink.ResumeCommand()
	}
	if tpl == "" {
		return nil
	}
	id := session
	if c != nil && c.Sink != nil {
		if r, ok := c.Sink.(ResumeIDer); ok {
			id = r.ResumeID(session)
		}
	}
	var out []string
	for _, w := range strings.Fields(tpl) {
		out = append(out, strings.ReplaceAll(w, "{id}", id))
	}
	return out
}

var plainArg = regexp.MustCompile(`^[\w@%+=:,./-]+$`)

// ShQ quotes for a POSIX shell.
func ShQ(s string) string {
	if plainArg.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ShellLine is argv as one shell-quoted command line.
func ShellLine(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = ShQ(a)
	}
	return strings.Join(q, " ")
}

// PowerShellQ quotes a literal without allowing interpolation or expressions.
func PowerShellQ(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// NativeShellLine matches the shell opened by the desktop on this platform.
func NativeShellLine(argv []string) string {
	return shellCommand(argv, "", nil, runtime.GOOS)
}

// ShellCommand includes a session's working directory and storage overrides.
// Windows uses PowerShell 5.1 syntax, which is also accepted by PowerShell 7.
func ShellCommand(argv []string, cwd string, envs map[string]string) string {
	return shellCommand(argv, cwd, envs, runtime.GOOS)
}

// ShellCommandWithInputPrompt supplies a line of interactive input to a native
// stdin command on Windows. The encoding change is scoped to the script block;
// PowerShell 5.1 otherwise replaces non-ASCII input with question marks.
func ShellCommandWithInputPrompt(argv []string, cwd string, envs map[string]string, prompt string) string {
	return shellCommandWithInputPrompt(argv, cwd, envs, runtime.GOOS, prompt)
}

func shellCommand(argv []string, cwd string, envs map[string]string, goos string) string {
	return shellCommandWithInputPrompt(argv, cwd, envs, goos, "")
}

func shellCommandWithInputPrompt(argv []string, cwd string, envs map[string]string, goos, prompt string) string {
	keys := make([]string, 0, len(envs))
	for key := range envs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var parts []string
	if goos == "windows" {
		for _, key := range keys {
			// Names are connector-owned constants; quoting values keeps paths literal.
			parts = append(parts, "$env:"+key+"="+PowerShellQ(envs[key])+";")
		}
		if len(argv) > 0 {
			if prompt != "" {
				parts = append(parts, "& { $OutputEncoding = [System.Text.UTF8Encoding]::new($false); Read-Host", PowerShellQ(prompt), "|")
			}
			parts = append(parts, "&")
			for i, arg := range argv {
				// npm installs both .ps1 and .cmd launchers. Resolve a native
				// executable/command file so a default PowerShell execution
				// policy cannot silently choose the blocked .ps1 launcher.
				if i == 0 && runtime.GOOS == "windows" {
					if resolved, err := exec.LookPath(arg); err == nil {
						arg = resolved
					}
				}
				parts = append(parts, PowerShellQ(arg))
			}
			if prompt != "" {
				// Preserve ordinary appended options for the native command.
				parts = append(parts, "@args }")
			}
		}
	} else {
		for _, key := range keys {
			parts = append(parts, key+"="+ShQ(envs[key]))
		}
		parts = append(parts, ShellLine(argv))
	}
	return commandInDirectory(strings.Join(parts, " "), cwd, goos)
}

func CommandInDirectory(command, cwd string) string {
	return commandInDirectory(command, cwd, runtime.GOOS)
}

func commandInDirectory(command, cwd, goos string) string {
	if cwd == "" {
		return command
	}
	if goos == "windows" {
		line := "Set-Location -LiteralPath " + PowerShellQ(cwd) + " -ErrorAction Stop"
		if command != "" {
			line += "; " + command
		}
		return line
	}
	line := "cd " + ShQ(cwd)
	if command != "" {
		line += " && " + command
	}
	return line
}
