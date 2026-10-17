package engine

import (
	"os"
	"regexp"
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
