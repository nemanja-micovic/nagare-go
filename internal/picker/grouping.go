package picker

import (
	"fmt"
	"strings"

	"github.com/nemke/nagare-go/internal/models"
)

// Tree glyphs prefixing a grouped session's label.
const (
	glyphMid = "├"
	glyphEnd = "└"
	// spacerRow separates adjacent project blocks without being selectable.
	spacerRow = -2
)

// listRow is one rendered line in the list view: either a group header or a
// session sitting under one. Only session rows are selectable, so headers carry
// SessionIdx -1.
//
// Rows are derived per frame rather than stored, which keeps them from drifting
// out of sync with the session slice they describe.
type listRow struct {
	SessionIdx int    // index into the session slice; -1 for a header
	Label      string // worktree name for a child, full name otherwise
	Group      string // header text (headers only)
	Count      int    // members in the group (headers only)
	Glyph      string // glyphMid / glyphEnd for children, empty otherwise
}

// groupKeyOf returns the key sessions are grouped by: the tmux session name,
// which every pane of one repo shares.
func groupKeyOf(s models.Session) string {
	if s.SessionName != "" {
		return s.SessionName
	}
	return s.Name
}

// childLabel returns the text a grouped session shows. The repo name lives on
// the header, so a child only needs to say which worktree or pane it is.
func childLabel(s models.Session) string {
	if s.Details.Worktree != "" {
		return s.Details.Worktree
	}
	if s.SessionName != "" {
		if rest, ok := strings.CutPrefix(s.Name, s.SessionName+"/"); ok && rest != "" {
			return rest
		}
	}
	return s.Name
}

// buildRows turns a session list into display rows, inserting a project header
// above every tmux session. Even a lone agent is a child: a consistent tree
// keeps agent sigils and names aligned across the whole list.
//
// Sessions must already be in visual order, with group members contiguous —
// sortFiltered guarantees this. Non-contiguous members would produce a header
// per run rather than corrupt the mapping.
func buildRows(sessions []models.Session) []listRow {
	counts := make(map[string]int, len(sessions))
	for _, s := range sessions {
		counts[groupKeyOf(s)]++
	}

	rows := make([]listRow, 0, len(sessions)*3)
	for i, s := range sessions {
		key := groupKeyOf(s)
		if i == 0 || groupKeyOf(sessions[i-1]) != key {
			if len(rows) > 0 {
				rows = append(rows, listRow{SessionIdx: spacerRow})
			}
			rows = append(rows, listRow{SessionIdx: -1, Group: key, Count: counts[key]})
		}

		glyph := glyphMid
		if i == len(sessions)-1 || groupKeyOf(sessions[i+1]) != key {
			glyph = glyphEnd
		}
		label := childLabel(s)
		// A lone pane normally has the bare tmux session name, leaving nothing
		// useful below a same-named header. Mirror the multi-pane fallback name
		// locally without changing the session's real MCP/tmux identity.
		if counts[key] == 1 && label == key {
			agent := string(s.AgentType)
			if agent == "" || s.AgentType == models.AgentUnknown {
				agent = "session"
			}
			label = agent + "_01"
		}
		rows = append(rows, listRow{SessionIdx: i, Label: label, Glyph: glyph})
	}
	return rows
}

// sharedPaths returns the working directories held by more than one agent, with
// their counts. Two agents in one directory will overwrite each other's edits,
// which is worth surfacing. Sessions without a path (saved ones) are ignored.
func sharedPaths(sessions []models.Session) map[string]int {
	counts := make(map[string]int, len(sessions))
	for _, s := range sessions {
		if s.Path != "" {
			counts[s.Path]++
		}
	}
	for path, n := range counts {
		if n < 2 {
			delete(counts, path)
		}
	}
	return counts
}

// killTarget returns the tmux target to kill for s and whether it addresses a
// window rather than a whole session.
//
// Worktree panes live as windows inside one session, so killing the session
// would take a repo's other worktrees down too. A session holding more than one
// agent pane is therefore killed a window at a time; a lone session is killed
// whole, as it always was.
func killTarget(s models.Session, sessions []models.Session) (string, bool) {
	agents := 0
	for _, other := range sessions {
		if other.SessionName == s.SessionName {
			agents++
		}
	}
	if agents > 1 {
		return fmt.Sprintf("%s:%d", s.SessionName, s.WindowIndex), true
	}
	return s.SessionName, false
}

// branchFor returns the branch text a list row should show, or "" when it would
// only repeat what the row already says.
//
// Claude names a worktree's branch "worktree-<name>", so printing it adds
// nothing while costing the columns a real branch needs. The comparison is
// against the worktree name as well as the label, because a lone pane in a
// worktree is labelled with its full "{session}/{worktree}" name rather than the
// bare worktree — matching only the label would let that case slip through.
//
// Plain sessions are the opposite case: their branch (feat/foo, fix/bar) cannot
// be guessed from the name, and showing it is the entire point — unless it is
// the default branch, which is what a row without one is taken to be on.
func branchFor(label, worktree, branch string) string {
	if branch == "" {
		return ""
	}
	// The default branch says nothing: on a row that is working on main, the
	// absence of a branch is the information. Showing it cost every such row
	// the columns its own name needed.
	for _, redundant := range []string{label, worktree, "worktree-" + label, "worktree-" + worktree, "main", "master"} {
		if branch == redundant {
			return ""
		}
	}
	return branch
}

// splitRowWidth divides the columns available to a row's text between its label
// and its branch. The label takes what it needs, but never so much that the
// branch drops below minBranchWidth; the branch then takes the remainder.
func splitRowWidth(avail, labelLen, branchLen int) (labelW, branchW int) {
	if branchLen == 0 {
		return avail, 0
	}
	labelW = labelLen
	if max := avail - minBranchWidth - 1; labelW > max {
		labelW = max
	}
	if labelW < 1 {
		labelW = 1
	}
	branchW = avail - labelW - 1
	if branchW < 0 {
		branchW = 0
	}
	return labelW, branchW
}
