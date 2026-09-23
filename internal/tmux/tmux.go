package tmux

import (
	"fmt"
	"os/exec"
	"strings"
)

// RunTmux runs a tmux command and returns stdout. Returns empty string on error.
func RunTmux(args ...string) string {
	cmd := exec.Command("tmux", args...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}

// PaneTarget formats a tmux pane target string (e.g., "session:0.1").
func PaneTarget(sessionName string, windowIndex, paneIndex int) string {
	return fmt.Sprintf("%s:%d.%d", sessionName, windowIndex, paneIndex)
}

// Arg protects a command argument from tmux's own parsing. tmux ends a command
// at any argument that ends in a semicolon — and drops the semicolon — so text
// typed as "git status; " or a key named "M-;" would lose it. A backslash before
// the final semicolon makes tmux keep it literally, and composes correctly with
// text that already ends in "\;".
func Arg(s string) string {
	if strings.HasSuffix(s, ";") {
		return s[:len(s)-1] + `\;`
	}
	return s
}
