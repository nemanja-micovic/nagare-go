// Package verify runs a project's own checks when an agent says it is done.
//
// A project opts in with a `.nagare/verify` file at its root: a shell
// command (or script body) such as `go test ./...` or `make check`. When an
// agent stops, the hook runs it in the agent's directory; if it fails, the
// agent is told to keep going with the failure output, instead of the human
// discovering the breakage during review. Nothing runs for a project without
// the file.
package verify

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// FileName is where a project keeps its verify command, relative to its root.
const FileName = ".nagare/verify"

// MaxAttempts caps how many times one stop is sent back. An agent that
// cannot make the checks pass after this many tries needs a human, not a
// fourth lap.
const MaxAttempts = 3

// Timeout bounds one run. The Stop hook is installed with a longer timeout
// than this, so a slow suite reports failure rather than being killed.
const Timeout = 8 * time.Minute

// FindFile returns the path of a .nagare file (e.g. FileName) for the
// repository containing dir: the worktree's copy first, then the main
// checkout's. "" if neither exists.
func FindFile(dir, name string) string {
	for _, root := range roots(dir) {
		path := filepath.Join(root, name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// Find returns the verify command for the repository containing dir and the
// file it came from, or "" if there is none.
func Find(dir string) (cmd, file string) {
	file = FindFile(dir, FileName)
	if file == "" {
		return "", ""
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", ""
	}
	return command(string(data)), file
}

// command drops comment lines ("# ...") and blank lines; what remains is run
// as one script, so a file can hold several commands.
func command(text string) string {
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		t := strings.TrimSpace(l)
		if t != "" && !strings.HasPrefix(t, "#") {
			lines = append(lines, t)
		}
	}
	return strings.Join(lines, "\n")
}

// roots lists where to look: the worktree's toplevel, then the main checkout.
func roots(dir string) []string {
	var out []string
	if top := gitOut(dir, "rev-parse", "--show-toplevel"); top != "" {
		out = append(out, top)
	}
	if common := gitOut(dir, "rev-parse", "--path-format=absolute", "--git-common-dir"); common != "" {
		main := filepath.Dir(common)
		if len(out) == 0 || out[0] != main {
			out = append(out, main)
		}
	}
	if len(out) == 0 {
		out = append(out, dir)
	}
	return out
}

func gitOut(dir string, args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Result is one verify run.
type Result struct {
	OK     bool
	Output string // combined output, tail only
}

// maxOutput keeps the part of the output an agent needs — the end, where
// test failures and compiler errors are reported.
const maxOutput = 4000

// Run executes cmd with sh in dir.
func Run(dir, cmd string, timeout time.Duration) Result {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	c.Dir = dir
	// Kill the whole group on timeout: killing only sh would leave its
	// children (the test runner) holding the output pipe open.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
	c.WaitDelay = 2 * time.Second
	var buf bytes.Buffer
	c.Stdout, c.Stderr = &buf, &buf
	err := c.Run()
	out := buf.String()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		out += "\n(verify timed out after " + timeout.String() + ")"
	}
	if len(out) > maxOutput {
		out = "…" + out[len(out)-maxOutput:]
	}
	return Result{OK: err == nil, Output: strings.TrimSpace(out)}
}

// Reason is what the agent is told when its stop is blocked.
func Reason(cmd string, r Result, attempt int) string {
	return "The project's checks (`" + cmd + "`) fail, so the task is not done yet " +
		"(nagare verify, attempt " + strconv.Itoa(attempt) + "/" + strconv.Itoa(MaxAttempts) + "). " +
		"Fix the failures below, then finish again.\n\n" + r.Output
}
