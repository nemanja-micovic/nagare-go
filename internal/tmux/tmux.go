package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// SocketEnv names a tmux server socket (tmux -L) for nagare to use instead of
// the default one. The demo runs its simulated agents on a private server this
// way, so they never mix with the user's real sessions.
const SocketEnv = "NAGARE_TMUX_SOCKET"

// Command returns an exec.Cmd running tmux with args, on the socket named by
// $NAGARE_TMUX_SOCKET when it is set. Every tmux invocation goes through here.
func Command(args ...string) *exec.Cmd {
	if sock := os.Getenv(SocketEnv); sock != "" {
		args = append([]string{"-L", sock}, args...)
	}
	return exec.Command("tmux", args...)
}

// RunTmux runs a tmux command and returns stdout. Returns empty string on error.
func RunTmux(args ...string) string {
	cmd := Command(args...)
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
