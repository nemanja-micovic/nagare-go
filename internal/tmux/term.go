package tmux

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Screen is one snapshot of a pane: what it shows and where its cursor is.
//
// It is what lets nagare host an agent's terminal inside its own UI rather than
// handing the user over to tmux. tmux already keeps the full terminal state for
// every pane — nagare only has to ask for it and draw it.
type Screen struct {
	Lines         []string // rendered rows, carrying the pane's own SGR styling
	CursorX       int
	CursorY       int
	CursorVisible bool
	Width         int
	Height        int
	History       int  // rows of scrollback above the visible screen
	Zoomed        bool // the pane's window is zoomed onto it
	Panes         int  // panes in the pane's window
}

// ErrPaneGone is returned when the pane no longer exists.
var ErrPaneGone = errors.New("pane is gone")

// screenFormat is appended after the capture so a single tmux invocation
// returns the content and the geometry it was captured at. Two invocations would
// be a fork more per frame and could straddle a resize.
const screenFormat = "#{cursor_x} #{cursor_y} #{cursor_flag} #{pane_width} #{pane_height} #{history_size} #{window_zoomed_flag} #{window_panes}"

// Snapshot captures a pane. scroll is how many rows above the live screen the
// view starts; 0 is the live screen. height is the pane height the caller last
// saw, needed to address a scrolled window before this capture reports it.
func Snapshot(target string, scroll, height int) (Screen, error) {
	return snapshot(ExecRunner, target, scroll, height)
}

func snapshot(run Runner, target string, scroll, height int) (Screen, error) {
	args := []string{"capture-pane", "-e", "-p", "-t", target}
	if scroll > 0 && height > 0 {
		args = append(args, "-S", strconv.Itoa(-scroll), "-E", strconv.Itoa(height-1-scroll))
	}
	args = append(args, ";", "display-message", "-p", "-t", target, screenFormat)

	out, err := run(args...)
	if err != nil {
		return Screen{}, ErrPaneGone
	}
	return parseScreen(out)
}

// parseScreen splits capture output from the geometry line appended to it.
func parseScreen(out string) (Screen, error) {
	out = strings.TrimSuffix(out, "\n")
	cut := strings.LastIndexByte(out, '\n')
	meta := out[cut+1:]
	body := ""
	if cut >= 0 {
		body = out[:cut]
	}

	f := strings.Fields(meta)
	if len(f) < 8 {
		return Screen{}, fmt.Errorf("unexpected pane geometry %q", meta)
	}
	n := make([]int, len(f))
	for i, s := range f {
		v, err := strconv.Atoi(s)
		if err != nil {
			return Screen{}, fmt.Errorf("unexpected pane geometry %q", meta)
		}
		n[i] = v
	}

	var lines []string
	if cut >= 0 {
		lines = strings.Split(body, "\n")
	}
	return Screen{
		Lines:         lines,
		CursorX:       n[0],
		CursorY:       n[1],
		CursorVisible: n[2] == 1,
		Width:         n[3],
		Height:        n[4],
		History:       n[5],
		Zoomed:        n[6] == 1,
		Panes:         n[7],
	}, nil
}

// focusMark is a window option nagare sets on any window it has resized. A
// crash cannot restore the size it changed, so the mark is what lets the next
// run find those windows and hand them back.
const focusMark = "@nagare_focus"

// FitWindow sizes a pane's window so the pane is exactly w×h, which is what
// makes an agent draw for the space nagare will show it in rather than for a
// terminal it is not attached to. A pane sharing its window is zoomed first, so
// it has the whole window to itself; zoomed reports whether this call did it.
func FitWindow(target string, w, h int, zoom bool) {
	fitWindow(ExecRunner, target, w, h, zoom)
}

func fitWindow(run Runner, target string, w, h int, zoom bool) {
	if zoom {
		run("resize-pane", "-Z", "-t", target)
	}
	run("resize-window", "-t", target, "-x", strconv.Itoa(w), "-y", strconv.Itoa(h),
		";", "set-option", "-w", "-t", target, focusMark, "1")
}

// ReleaseWindow undoes FitWindow: the window goes back to following the clients
// attached to it, and a zoom FitWindow applied is taken off again.
//
// The order matters. resize-window -A itself sets window-size to manual, so the
// option can only be unset after it; the other way round leaves the window
// pinned at whatever size it had.
func ReleaseWindow(target string, unzoom bool) {
	releaseWindow(ExecRunner, target, unzoom)
}

func releaseWindow(run Runner, target string, unzoom bool) {
	if unzoom {
		run("resize-pane", "-Z", "-t", target)
	}
	run("resize-window", "-A", "-t", target,
		";", "set-option", "-wu", "-t", target, "window-size",
		";", "set-option", "-wu", "-t", target, focusMark)
}

// ReleaseStaleWindows hands back every window a previous run resized and never
// released — after a crash, or a kill -9. Without it such a window would stay
// pinned at nagare's size the next time the user attached to it.
func ReleaseStaleWindows() {
	out := RunTmux("list-windows", "-a", "-F", "#{window_id} #{"+focusMark+"}")
	for _, line := range strings.Split(out, "\n") {
		id, mark, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok && mark == "1" {
			ReleaseWindow(id, false)
		}
	}
}

// shellMark is the window option tying a companion shell to its agent's pane.
const shellMark = "@nagare_shell"

// CompanionShell returns the pane of the shell kept beside an agent pane,
// creating it — a new window in the agent's session, in the agent's directory —
// if there is none yet. Reusing it means history and running jobs survive
// switching away and back.
func CompanionShell(run Runner, agentPane, sessionName, dir string) (string, error) {
	out, _ := run("list-panes", "-a", "-F", "#{pane_id} #{"+shellMark+"}")
	for _, line := range strings.Split(out, "\n") {
		id, owner, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok && owner == agentPane {
			return id, nil
		}
	}
	args := []string{"new-window", "-d", "-P", "-F", "#{pane_id}", "-n", "shell", "-t", sessionName + ":"}
	if dir != "" {
		args = append(args, "-c", dir)
	}
	b, err := run(args...)
	id := strings.TrimSpace(b)
	if err != nil || id == "" {
		return "", fmt.Errorf("could not open a shell in %s", sessionName)
	}
	run("set-option", "-w", "-t", id, shellMark, agentPane)
	return id, nil
}
