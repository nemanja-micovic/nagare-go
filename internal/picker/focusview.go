package picker

import (
	"fmt"
	"image"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nemke/nagare-go/internal/models"
	"github.com/nemke/nagare-go/internal/theme"
)

// viewFocus lays out focus mode: the sidebar, then the agent's terminal.
func (m Model) viewFocus() (string, hitTargets) {
	g := m.focusGeometry()
	hits := hitTargets{focus: true}
	hits.term = image.Rect(g.panelX, 0, g.panelX+g.panelW, g.height)

	panel := strings.Split(m.renderTermPanel(g), "\n")
	if g.sidebarW == 0 {
		return strings.Join(panel, "\n"), hits
	}

	sidebar, sessionAt := m.cachedSidebar(g)
	hits.sessionAt = sessionAt
	hits.listWidth = g.sidebarW

	// Both halves are exactly g.height rows by construction, so they are joined
	// line by line rather than through JoinHorizontal, which would measure every
	// cell of a panel that repaints at up to 30fps.
	left := strings.Split(sidebar, "\n")
	var b strings.Builder
	for i := range g.height {
		if i > 0 {
			b.WriteByte('\n')
		}
		if i < len(left) {
			b.WriteString(left[i])
		}
		if i < len(panel) {
			b.WriteString(panel[i])
		}
	}
	return b.String(), hits
}

// sidebarCache holds focus mode's last sidebar render. It is a pointer so that
// every copy of the Model shares it — View runs on a value receiver.
type sidebarCache struct {
	key       string
	out       string
	sessionAt map[int]int
}

// cachedSidebar renders the sidebar, or reuses the last render when nothing it
// draws from has changed.
//
// This is what keeps focus mode affordable. The terminal beside the sidebar
// repaints at up to 30fps while an agent streams output, but the sidebar only
// changes on a scan, a keypress, or a tick of the status-dot breath — and
// rendering it is most of a frame, because fitBox measures every cell of the
// panel to pin its size.
func (m Model) cachedSidebar(g focusGeometry) (string, map[int]int) {
	c := m.sidebarCache
	if c == nil {
		return m.viewList(g.sidebarW, g.height, true)
	}
	// Everything viewList reads, in one string. A missed input shows up as a
	// sidebar that lags reality until the next scan, so err on including more.
	var flashes []string
	for k, f := range m.flashes {
		flashes = append(flashes, fmt.Sprintf("%s=%d:%.3f", k, f.kind, f.level))
	}
	slices.Sort(flashes)
	key := fmt.Sprintf("%d|%d|%s|%d|%d|%.4f|%v|%v|%d|%v|%p|%s|%s",
		g.sidebarW, g.height, theme.Current().Name, m.filterGen, m.cursor, m.breath,
		m.slide, m.showSaved, m.sortMode, m.pending != nil, m.registry,
		m.statusMessage(), strings.Join(flashes, ","))
	if key != c.key {
		c.out, c.sessionAt = m.viewList(g.sidebarW, g.height, true)
		c.key = key
	}
	return c.out, c.sessionAt
}

// focusedSession returns the live session behind the focused pane, if a scan
// has listed it.
func (m Model) focusedSession() (models.Session, bool) {
	for _, s := range m.sessions {
		if sessionKey(s) == m.focus.key {
			return s, true
		}
	}
	return models.Session{}, false
}

// visibleRows returns the rows of the capture that fit the panel, and the index
// of the first one. A capture taller than the panel — taken before a resize
// landed — keeps its bottom, which is where the prompt and cursor are.
func visibleRows(lines []string, height int) ([]string, int) {
	if len(lines) > height {
		off := len(lines) - height
		return lines[off:], off
	}
	return lines, 0
}

// renderTermPanel draws the focused agent's screen inside a gradient frame
// whose top edge carries the title.
//
// It is assembled by hand, not through a lipgloss border. This panel repaints
// every time the agent's screen changes, and fitBox's Render measures every
// cell of a panel just to pin its size — the single largest cost in a frame.
// Here the size is known exactly, so nothing needs measuring except each
// captured line, once.
func (m Model) renderTermPanel(g focusGeometry) string {
	c := theme.Current().Colors
	w, h := g.panelW, g.height
	if w < 4 || h < 3 {
		return ""
	}

	surface := bgSeq(c.Surface)
	const reset = "\x1b[0m"
	edge := func(x int) string {
		t := 0.0
		if w > 1 {
			t = float64(x) / float64(w-1)
		}
		return fgSeq(theme.Mix(c.GradientFrom, c.GradientTo, t))
	}
	leftEdge := surface + edge(0) + "│"
	rightEdge := surface + edge(w-1) + "│" + reset

	var b strings.Builder
	b.Grow((w + 16) * h * 2)

	// Top edge: ╭─ title ───── status ─╮
	title := m.focusTitle()
	status := m.focusStatus()
	room := w - 4 // corners, and one dash beside each
	if lipgloss.Width(title)+2 > room {
		title = ansi.Truncate(title, max(room-2, 0), ellipsis)
	}
	used := lipgloss.Width(title) + 2
	if status != "" && used+lipgloss.Width(status)+3 > room {
		status = ""
	}
	statusW := 0
	if status != "" {
		statusW = lipgloss.Width(status) + 2
	}
	fill := room - used - statusW

	b.WriteString(surface + edge(0) + "╭" + edge(1) + "─ ")
	b.WriteString(title)
	b.WriteString(surface + " ")
	x := 2 + used
	for range fill {
		b.WriteString(edge(x) + "─")
		x++
	}
	if status != "" {
		b.WriteString(surface + " " + status + surface + " ")
		x += statusW
	}
	b.WriteString(edge(w-2) + "─" + edge(w-1) + "╮" + reset)

	// Body: the agent's screen, one row of the capture per row of the panel.
	f := m.focus
	rows, _ := visibleRows(f.screen.Lines, g.termH)
	for i := range g.termH {
		b.WriteByte('\n')
		b.WriteString(leftEdge + surface + " ")
		line := ""
		switch {
		case i < len(rows):
			line = rows[i]
		case !f.have && i == g.termH/2:
			line = mutedStyle().Render(centered("connecting to "+f.name+"…", g.termW))
		}
		if ansi.StringWidth(line) > g.termW {
			line = ansi.Truncate(line, g.termW, "")
		}
		// The capture's own resets would otherwise drop cells onto the
		// terminal's background; its trailing attributes would otherwise leak
		// into the padding.
		b.WriteString(reassertBg(line, surface))
		b.WriteString(reset + surface)
		b.WriteString(strings.Repeat(" ", g.termW-ansi.StringWidth(line)+1))
		b.WriteString(rightEdge)
	}

	// Bottom edge.
	b.WriteByte('\n')
	b.WriteString(surface + edge(0) + "╰")
	for x := 1; x < w-1; x++ {
		b.WriteString(edge(x) + "─")
	}
	b.WriteString(edge(w-1) + "╯" + reset)
	return b.String()
}

// focusTitle is the panel's name tag: status, name, agent and branch — the
// detail pane's essentials, folded into the frame so the agent keeps every row.
func (m Model) focusTitle() string {
	c := theme.Current().Colors
	base := lipgloss.NewStyle().Background(c.Surface)
	s, live := m.focusedSession()

	status := models.StatusIdle
	if live {
		status = s.Status
	}
	dot := statusDotOn(status, m.breath, c.Surface)
	// Named the way the sidebar names it — repo, then the row's own label — so
	// the frame and the highlighted row visibly refer to the same thing.
	name := base.Foreground(c.Foreground).Bold(true).Render(m.focus.name)
	if live {
		repo, child := groupKeyOf(s), childLabel(s)
		name = base.Foreground(c.Foreground).Bold(true).Render(child)
		if repo != "" && repo != child {
			name = base.Foreground(c.Secondary).Bold(true).Render(repo) +
				base.Foreground(c.Muted).Render(" / ") + name
		}
	}
	agent := base.Foreground(lipgloss.Color(models.AgentColor(m.focus.agent))).
		Render(models.AgentLabel(m.focus.agent))
	sep := base.Foreground(c.Border).Render(" · ")

	if m.focus.shell {
		// The dot still reports the agent: its state is what the user is
		// keeping an eye on while they work in the shell.
		agent = base.Foreground(c.Accent).Bold(true).Render("shell")
	}
	parts := dot + base.Render(" ") + name + sep + agent
	if live {
		if br := branchFor(childLabel(s), s.Details.Worktree, s.Details.GitBranch); br != "" {
			parts += sep + base.Foreground(c.Muted).Render(br)
		}
		if status == models.StatusWaitingInput {
			parts += sep + base.Foreground(c.Warning).Bold(true).Render("needs you")
		}
	}
	return parts
}

// focusStatus is the right-hand tag: where the view is, when that is anywhere
// but the live screen. Empty otherwise, so the frame stays quiet.
func (m Model) focusStatus() string {
	c := theme.Current().Colors
	base := lipgloss.NewStyle().Background(c.Surface)
	if m.focus.scroll > 0 {
		return base.Foreground(c.Warning).Render(fmt.Sprintf("↑ %d lines back", m.focus.scroll)) +
			base.Foreground(c.Muted).Render(" · ⇧PgDn to return")
	}
	if m.focus.zoom {
		return base.Foreground(c.Muted).Render("zoomed · Alt+z")
	}
	return ""
}

// centered pads s to sit in the middle of width cells.
func centered(s string, width int) string {
	pad := (width - lipgloss.Width(s)) / 2
	if pad <= 0 {
		return s
	}
	return strings.Repeat(" ", pad) + s
}

// focusCursor places the real terminal cursor where the agent's cursor is, so
// typing into an embedded agent feels like typing into it directly. Nil when the
// agent hides its cursor (Claude Code draws its own), when the view is scrolled
// away from the live screen, or when a dialog has the screen.
func (m Model) focusCursor() *tea.Cursor {
	f := m.focus
	if !f.on || !f.have || !f.screen.CursorVisible || f.scroll > 0 || m.overlayOpen() {
		return nil
	}
	g := m.focusGeometry()
	_, off := visibleRows(f.screen.Lines, g.termH)
	x, y := f.screen.CursorX, f.screen.CursorY-off
	if x < 0 || x >= g.termW || y < 0 || y >= g.termH {
		return nil
	}
	return tea.NewCursor(g.panelX+2+x, 1+y)
}
