package picker

import (
	"fmt"
	"image"
	"image/color"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nemke/nagare-go/internal/models"
	"github.com/nemke/nagare-go/internal/theme"
)

// viewFocus lays out focus mode: the sidebar, then the tiles.
func (m Model) viewFocus() (string, hitTargets) {
	g := m.focusGeometry()
	hits := hitTargets{focus: true}
	hits.term = image.Rect(g.panelX, 0, g.panelX+g.panelW, g.height)
	for _, r := range g.tiles {
		hits.tiles = append(hits.tiles, image.Rect(r.x, r.y, r.x+r.w, r.y+r.h))
	}

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
// This is what keeps focus mode affordable. The terminals beside the sidebar
// repaint at up to 30fps while agents stream output, but the sidebar only
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
	var shown []string
	for i := range m.focus.n {
		shown = append(shown, m.focus.tiles[i].key)
	}
	key := fmt.Sprintf("%d|%d|%s|%d|%d|%.4f|%v|%v|%d|%v|%p|%s|%s|%s",
		g.sidebarW, g.height, theme.Current().Name, m.filterGen, m.cursor, m.breath,
		m.slide, m.showSaved, m.sortMode, m.pending != nil, m.registry,
		m.statusMessage(), strings.Join(flashes, ","), strings.Join(shown, ","))
	if key != c.key {
		c.out, c.sessionAt = m.viewList(g.sidebarW, g.height, true)
		c.key = key
	}
	return c.out, c.sessionAt
}

// focusedSession returns the live session behind the active tile, if a scan has
// listed it.
func (m Model) focusedSession() (models.Session, bool) {
	return m.sessionFor(m.focus.tiles[m.focus.active].key)
}

// sessionFor returns the live session with the given key.
func (m Model) sessionFor(key string) (models.Session, bool) {
	for _, s := range m.sessions {
		if sessionKey(s) == key {
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

// renderTermPanel draws every tile into the area g gives them. The layouts are
// all columns of stacked tiles, so the frame is assembled row by row: each row
// of the area is the matching row of every tile that crosses it, left to right.
func (m Model) renderTermPanel(g focusGeometry) string {
	if g.panelW < 4 || g.height < 3 {
		return ""
	}
	n := max(m.focus.n, 1)
	panels := make([][]string, n)
	for i := range n {
		panels[i] = strings.Split(m.renderTile(i, g.tiles[i]), "\n")
	}
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int { return g.tiles[a].x - g.tiles[b].x })

	var b strings.Builder
	for y := range g.height {
		if y > 0 {
			b.WriteByte('\n')
		}
		for _, i := range order {
			r := g.tiles[i]
			if y >= r.y && y < r.y+r.h && y-r.y < len(panels[i]) {
				b.WriteString(panels[i][y-r.y])
			}
		}
	}
	return b.String()
}

// renderTile draws tile i's screen inside a frame whose top edge carries the
// title. The active tile's frame is the theme gradient — where the keyboard is,
// the same cue the list uses — and the others take the quiet border colour.
//
// It is assembled by hand, not through a lipgloss border. A tile repaints every
// time its agent's screen changes, and fitBox's Render measures every cell of a
// panel just to pin its size — the single largest cost in a frame. Here the size
// is known exactly, so nothing needs measuring except each captured line, once.
func (m Model) renderTile(i int, r tileRect) string {
	c := theme.Current().Colors
	w, h := r.w, r.h
	if w < 4 || h < 3 {
		return strings.Repeat(strings.Repeat(" ", max(w, 0))+"\n", max(h-1, 0)) + strings.Repeat(" ", max(w, 0))
	}
	t := m.focus.tiles[i]
	active := i == m.focus.active

	surface := bgSeq(c.Surface)
	const reset = "\x1b[0m"
	quiet := fgSeq(c.Border)
	edge := func(x int) string {
		if !active {
			return quiet
		}
		f := 0.0
		if w > 1 {
			f = float64(x) / float64(w-1)
		}
		return fgSeq(theme.Mix(c.GradientFrom, c.GradientTo, f))
	}
	leftEdge := surface + edge(0) + "│"
	rightEdge := surface + edge(w-1) + "│" + reset

	var b strings.Builder
	b.Grow((w + 16) * h * 2)

	// Top edge: ╭─ title ───── status ─╮
	title := m.tileTitle(t, active)
	status := m.tileStatus(t)
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
	}
	b.WriteString(edge(w-2) + "─" + edge(w-1) + "╮" + reset)

	// Body: the agent's screen, one row of the capture per row of the tile.
	termW, termH := r.termW(), r.termH()
	rows, _ := visibleRows(t.screen.Lines, termH)
	for y := range termH {
		b.WriteByte('\n')
		b.WriteString(leftEdge + surface + " ")
		line := ""
		switch {
		case y < len(rows):
			line = rows[y]
		case !t.have && y == termH/2:
			line = mutedStyle().Render(centered("connecting to "+t.name+"…", termW))
		}
		if ansi.StringWidth(line) > termW {
			line = ansi.Truncate(line, termW, "")
		}
		// The capture's own resets would otherwise drop cells onto the
		// terminal's background; its trailing attributes would otherwise leak
		// into the padding.
		b.WriteString(reassertBg(line, surface))
		b.WriteString(reset + surface)
		b.WriteString(strings.Repeat(" ", termW-ansi.StringWidth(line)+1))
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

// tileTitle is a tile's name tag: status, name, agent and branch — the detail
// pane's essentials, folded into the frame so the agent keeps every row. An
// inactive tile's title is muted, so the active one reads first.
func (m Model) tileTitle(t paneView, active bool) string {
	c := theme.Current().Colors
	base := lipgloss.NewStyle().Background(c.Surface)
	s, live := m.sessionFor(t.key)

	status := models.StatusIdle
	if live {
		status = s.Status
	}
	strong, soft := c.Foreground, c.Secondary
	if !active {
		strong, soft = c.Subtle, c.Muted
	}
	dot := statusDotOn(status, m.breath, c.Surface)
	// Named the way the sidebar names it — repo, then the row's own label — so
	// the frame and the highlighted row visibly refer to the same thing.
	name := base.Foreground(strong).Bold(active).Render(t.name)
	if live {
		repo, child := groupKeyOf(s), childLabel(s)
		name = base.Foreground(strong).Bold(active).Render(child)
		if repo != "" && repo != child {
			name = base.Foreground(soft).Bold(active).Render(repo) +
				base.Foreground(c.Muted).Render(" / ") + name
		}
	}
	var agentColor color.Color = lipgloss.Color(models.AgentColor(t.agent))
	if !active {
		agentColor = c.Muted
	}
	agent := base.Foreground(agentColor).Render(models.AgentLabel(t.agent))
	sep := base.Foreground(c.Border).Render(" · ")

	if t.shell {
		// The dot still reports the agent: its state is what the user is
		// keeping an eye on while they work in the shell.
		agent = base.Foreground(c.Accent).Bold(active).Render("shell")
	}
	parts := dot + base.Render(" ") + name + sep + agent
	if live {
		if br := branchFor(childLabel(s), s.Details.Worktree, s.Details.GitBranch); br != "" && active {
			parts += sep + base.Foreground(c.Muted).Render(br)
		}
		// Loud in every tile, active or not: this is the one thing a background
		// tile exists to tell you.
		if status == models.StatusWaitingInput {
			parts += sep + base.Foreground(c.Warning).Bold(true).Render("needs you")
		}
	}
	return parts
}

// tileStatus is the right-hand tag: where the view is, when that is anywhere
// but the live screen. Empty otherwise, so the frame stays quiet.
func (m Model) tileStatus(t paneView) string {
	c := theme.Current().Colors
	base := lipgloss.NewStyle().Background(c.Surface)
	if t.scroll > 0 {
		return base.Foreground(c.Warning).Render(fmt.Sprintf("↑ %d lines back", t.scroll)) +
			base.Foreground(c.Muted).Render(" · ⇧PgDn to return")
	}
	if m.focus.zoom && m.focus.n == 1 {
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

// focusCursor places the real terminal cursor where the active agent's cursor
// is, so typing into an embedded agent feels like typing into it directly. Nil
// when the agent hides its cursor (Claude Code draws its own), when the view is
// scrolled away from the live screen, or when a dialog has the screen.
func (m Model) focusCursor() *tea.Cursor {
	f := m.focus
	if !f.on || f.n == 0 || m.overlayOpen() {
		return nil
	}
	t := f.tiles[f.active]
	if !t.have || !t.screen.CursorVisible || t.scroll > 0 {
		return nil
	}
	g := m.focusGeometry()
	r := g.tiles[f.active]
	_, off := visibleRows(t.screen.Lines, r.termH())
	x, y := t.screen.CursorX, t.screen.CursorY-off
	if x < 0 || x >= r.termW() || y < 0 || y >= r.termH() {
		return nil
	}
	return tea.NewCursor(r.x+2+x, r.y+1+y)
}
