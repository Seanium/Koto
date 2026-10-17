package shared

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"koto/internal/platform"
)

// PidAlive: signal 0 reaches the process (EPERM: alive under another user).
func PidAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	return platform.ProcessAlive(pid)
}

func run(cmd string, args ...string) (string, bool) {
	c := exec.Command(cmd, args...)
	c.Stdin = nil
	done := make(chan struct{})
	var out []byte
	var err error
	go func() { out, err = c.Output(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = c.Process.Kill()
		<-done
		return "", false
	}
	if err != nil {
		// lsof exits 1 when nothing matches; its stdout is still the (empty) answer.
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return string(out), true
		}
		return "", false
	}
	return string(out), true
}

// ours: this process or one it started (our own codex app-server) is never "someone else".
func ours(pid int) bool {
	p := pid
	for i := 0; p > 1 && i < 16; i++ {
		if p == os.Getpid() {
			return true
		}
		out, ok := run("ps", "-o", "ppid=", "-p", strconv.Itoa(p))
		if !ok {
			return false
		}
		pp, _ := strconv.Atoi(strings.TrimSpace(out))
		if pp == 0 || pp == p {
			return false
		}
		p = pp
	}
	return false
}

// FileHolders are the pids (not ours) holding file open; ok=false when it cannot be told (no lsof).
func FileHolders(file string) ([]int, bool) {
	if runtime.GOOS == "windows" {
		return nil, false
	}
	out, ok := run("lsof", "-t", "--", file)
	if !ok {
		return nil, false
	}
	var pids []int
	for _, l := range strings.Split(out, "\n") {
		if p, _ := strconv.Atoi(strings.TrimSpace(l)); p > 0 && !ours(p) {
			pids = append(pids, p)
		}
	}
	return pids, true
}

var interp = regexp.MustCompile(`(^|/)(node|nodejs|bun|deno|tsx)$`)

// ProgramOf is the program a command line runs (for an interpreter, the script it was given).
func ProgramOf(command string) string {
	words := strings.Fields(command)
	if len(words) == 0 {
		return ""
	}
	if !interp.MatchString(words[0]) {
		return words[0]
	}
	for _, w := range words[1:] {
		if !strings.HasPrefix(w, "-") && w != "run" {
			return w
		}
	}
	return words[0]
}

// Proc is a running process.
type Proc struct {
	Pid     int
	Command string
	Cwd     string
}

var psLine = regexp.MustCompile(`^\s*(\d+)\s+(.*)$`)

// ProcessesWithCwd: running processes whose program matches, with their working directories (best effort).
func ProcessesWithCwd(match *regexp.Regexp) ([]Proc, bool) {
	if runtime.GOOS == "windows" {
		return nil, false
	}
	out, ok := run("ps", "-axo", "pid=,command=")
	if !ok {
		return nil, false
	}
	var procs []Proc
	for _, l := range strings.Split(out, "\n") {
		m := psLine.FindStringSubmatch(l)
		if m == nil || !match.MatchString(ProgramOf(m[2])) {
			continue
		}
		pid, _ := strconv.Atoi(m[1])
		if ours(pid) {
			continue
		}
		procs = append(procs, Proc{Pid: pid, Command: m[2]})
	}
	if len(procs) == 0 {
		return procs, true
	}
	if runtime.GOOS == "linux" {
		for i := range procs {
			if c, err := os.Readlink("/proc/" + strconv.Itoa(procs[i].Pid) + "/cwd"); err == nil {
				procs[i].Cwd = c
			}
		}
		return procs, true
	}
	ids := make([]string, len(procs))
	for i, p := range procs {
		ids[i] = strconv.Itoa(p.Pid)
	}
	lo, _ := run("lsof", "-a", "-d", "cwd", "-Fpn", "-p", strings.Join(ids, ","))
	pid := 0
	for _, l := range strings.Split(lo, "\n") {
		if strings.HasPrefix(l, "p") {
			pid, _ = strconv.Atoi(l[1:])
		} else if strings.HasPrefix(l, "n") {
			for i := range procs {
				if procs[i].Pid == pid {
					procs[i].Cwd = l[1:]
				}
			}
		}
	}
	return procs, true
}

func real(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	a, _ := filepath.Abs(p)
	return a
}

// SamePath compares two paths after resolving symlinks.
func SamePath(a, b string) bool { return real(a) == real(b) }
