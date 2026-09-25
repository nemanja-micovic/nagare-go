package picker

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sahilm/fuzzy"

	"github.com/nemke/nagare-go/internal/models"
	"github.com/nemke/nagare-go/internal/theme"
)

// The command palette is one fuzzy box over everything nagare can do and every
// agent it can open. It is how a new user finds features without reading the
// help screen, and how a practised one reaches the rare ones without learning
// their keys.
//
// An action is its key binding: running it replays that keypress through the
// ordinary key handling, so the palette can never do something the key does
// not, or drift from it when a binding changes.

const (
	keyPalette      = "ctrl+k" // in the list
	keyFocusPalette = "alt+k"  // in focus mode
	paletteRows     = 12
)

type paletteState struct {
	open  bool
	input textinput.Model
	sel   int
}

// paletteItem is one entry: an agent to open, or an action to run.
type paletteItem struct {
	label   string
	keys    string          // shortcut shown beside the entry
	press   tea.KeyPressMsg // what running it replays
	session *models.Session // set for an agent entry
}

func keyPress(code rune, mod tea.KeyMod) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Mod: mod}
}

// paletteActions are the actions valid right now. Focus mode and the list have
// different keys, and some actions only exist in one of them.
func (m Model) paletteActions() []paletteItem {
	if m.focus.on {
		items := []paletteItem{
			{label: "Back to the session list", keys: keyLabel(m.leaveKeyOrDefault(), false), press: leavePress(m.leaveKeyOrDefault())},
			{label: "Next agent waiting on you", keys: "F4", press: keyPress(tea.KeyF4, 0)},
			{label: "Split: add the next agent beside this one", keys: "Alt+v", press: keyPress('v', tea.ModAlt)},
			{label: "Review this agent's changes", keys: "Alt+d", press: keyPress('d', tea.ModAlt)},
			{label: "Shell in this agent's directory", keys: "Alt+s", press: keyPress('s', tea.ModAlt)},
			{label: "Zoom: hide the sidebar", keys: "Alt+z", press: keyPress('z', tea.ModAlt)},
			{label: "Open this agent in tmux", keys: "F5", press: keyPress(tea.KeyF5, 0)},
			{label: "Help: every key", keys: "F1", press: keyPress(tea.KeyF1, 0)},
		}
		if m.focus.n > 1 {
			items = append(items[:3], append([]paletteItem{
				{label: "Close this tile", keys: "Alt+x", press: keyPress('x', tea.ModAlt)},
				{label: "Next tile", keys: "Alt+→", press: keyPress(tea.KeyRight, tea.ModAlt)},
			}, items[3:]...)...)
		}
		return items
	}
	return []paletteItem{
		{label: "Next agent waiting on you", keys: "F4", press: keyPress(tea.KeyF4, 0)},
		{label: "New session", keys: "Ctrl+n", press: keyPress('n', tea.ModCtrl)},
		{label: "New git worktree for this repo", keys: "F3", press: keyPress(tea.KeyF3, 0)},
		{label: "Review the selected agent's changes", keys: "Ctrl+d", press: keyPress('d', tea.ModCtrl)},
		{label: "Quick prototype", keys: "Ctrl+r", press: keyPress('r', tea.ModCtrl)},
		{label: "Send a prompt to the selected agent", keys: "Ctrl+l", press: keyPress('l', tea.ModCtrl)},
		{label: "Compose a prompt in $EDITOR", keys: "Ctrl+g", press: keyPress('g', tea.ModCtrl)},
		{label: "Approve the pending permission", keys: "Ctrl+y", press: keyPress('y', tea.ModCtrl)},
		{label: "Name the selected task", keys: "F2", press: keyPress(tea.KeyF2, 0)},
		{label: "Open the selected agent in tmux", keys: "F5", press: keyPress(tea.KeyF5, 0)},
		{label: "Toggle list / grid view", keys: "Tab", press: keyPress(tea.KeyTab, 0)},
		{label: "Show saved sessions", keys: "Ctrl+s", press: keyPress('s', tea.ModCtrl)},
		{label: "Cycle sort order", keys: "Ctrl+o", press: keyPress('o', tea.ModCtrl)},
		{label: "Star the selected session", keys: "Ctrl+f", press: keyPress('f', tea.ModCtrl)},
		{label: "Pick a colour theme", keys: "Ctrl+t", press: keyPress('t', tea.ModCtrl)},
		{label: "Unload the selected agent", keys: "Ctrl+w", press: keyPress('w', tea.ModCtrl)},
		{label: "Kill the selected session", keys: "Ctrl+x", press: keyPress('x', tea.ModCtrl)},
		{label: "Edit the config file", keys: "Ctrl+e", press: keyPress('e', tea.ModCtrl)},
		{label: "Help: every key", keys: "F1", press: keyPress(tea.KeyF1, 0)},
		{label: "Quit nagare", keys: "Esc", press: keyPress(tea.KeyEscape, 0)},
	}
}

// leavePress is the keypress for the configured leave key. Only the forms a
// config can name — ctrl/alt plus one character, or a function key — are
// supported; anything else falls back to the default.
func leavePress(k string) tea.KeyPressMsg {
	mods, base := tea.KeyMod(0), k
	for {
		switch {
		case strings.HasPrefix(base, "ctrl+"):
			mods |= tea.ModCtrl
			base = base[5:]
			continue
		case strings.HasPrefix(base, "alt+"):
			mods |= tea.ModAlt
			base = base[4:]
			continue
		}
		break
	}
	if len(base) >= 2 && base[0] == 'f' {
		n := 0
		for _, r := range base[1:] {
			n = n*10 + int(r-'0')
		}
		if n >= 1 && n <= 12 {
			return keyPress(tea.KeyF1+rune(n-1), mods)
		}
	}
	if r := []rune(base); len(r) == 1 {
		return keyPress(r[0], mods)
	}
	return keyPress(']', tea.ModCtrl)
}

// paletteItems are the entries matching the palette's query: agents first when
// the query names one, then actions.
func (m Model) paletteItems() []paletteItem {
	var all []paletteItem
	for i := range m.sessions {
		s := m.sessions[i]
		if s.Status == models.StatusSaved {
			continue
		}
		label := childLabel(s)
		if repo := groupKeyOf(s); repo != "" && repo != label {
			label = repo + " / " + label
		}
		all = append(all, paletteItem{label: "Open " + label, keys: models.AgentLabel(s.AgentType), session: &s})
	}
	all = append(all, m.paletteActions()...)

	q := strings.TrimSpace(m.palette.input.Value())
	if q == "" {
		// With nothing typed, lead with what matters now: actions, then agents
		// waiting on the user.
		var waiting, rest []paletteItem
		for _, it := range all {
			switch {
			case it.session == nil:
				rest = append(rest, it)
			case it.session.Status == models.StatusWaitingInput:
				waiting = append(waiting, it)
			}
		}
		return append(waiting, rest...)
	}
	labels := make([]string, len(all))
	for i, it := range all {
		labels[i] = it.label
	}
	matches := fuzzy.Find(q, labels)
	out := make([]paletteItem, len(matches))
	for i, mt := range matches {
		out[i] = all[mt.Index]
	}
	return out
}

// openPalette opens the command palette.
func (m Model) openPalette() (Model, tea.Cmd) {
	ti := textinput.New()
	ti.Prompt = "› "
	ti.Placeholder = "type an action or an agent…"
	ti.CharLimit = 80
	ti.Focus()
	m.palette = paletteState{open: true, input: ti}
	return m, textinput.Blink
}

// handlePaletteKey drives the open palette.
func (m Model) handlePaletteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := m.paletteItems()
	switch msg.String() {
	case keyEscape, keyPalette, keyFocusPalette:
		m.palette.open = false
		return m, nil
	case keyUp, "ctrl+p":
		if m.palette.sel > 0 {
			m.palette.sel--
		}
		return m, nil
	case keyDown, "ctrl+n":
		if m.palette.sel < len(items)-1 {
			m.palette.sel++
		}
		return m, nil
	case keyEnter:
		m.palette.open = false
		if m.palette.sel >= len(items) {
			return m, nil
		}
		it := items[m.palette.sel]
		if it.session != nil {
			return m.enterFocus(*it.session)
		}
		return m.Update(it.press)
	}
	var cmd tea.Cmd
	m.palette.input, cmd = m.palette.input.Update(msg)
	m.palette.sel = 0
	return m, cmd
}

// renderPaletteOverlay draws the palette: the query, then the matches.
func (m Model) renderPaletteOverlay() string {
	c := theme.Current().Colors
	w := min(max(m.width*2/3, 40), 90)
	iw := w - 2 - 4
	bg := lipgloss.NewStyle().Background(c.Overlay)

	m.palette.input.SetWidth(iw - 3)
	lines := []string{m.palette.input.View(), fadingRule(iw, c.GradientFrom, c.Overlay)}

	items := m.paletteItems()
	rows := min(paletteRows, max(m.height-10, 3))
	start := 0
	if m.palette.sel >= rows {
		start = m.palette.sel - rows + 1
	}
	q := strings.TrimSpace(m.palette.input.Value())
	for i := start; i < len(items) && i < start+rows; i++ {
		it := items[i]
		rowBg := c.Overlay
		if i == m.palette.sel {
			rowBg = c.SelBg
		}
		rs := lipgloss.NewStyle().Background(rowBg)
		glyph := rs.Foreground(c.Accent).Render("›")
		if it.session != nil {
			glyph = statusDotOn(it.session.Status, m.breath, rowBg)
		}
		keys := rs.Foreground(c.Muted).Render(it.keys)
		labelW := max(iw-2-lipgloss.Width(keys)-2, 8)
		label := truncate(it.label, labelW)
		nameStyle := rs.Foreground(c.Foreground)
		if i == m.palette.sel {
			nameStyle = nameStyle.Bold(true)
		}
		name := renderNameWithMatches(label, it.label, q, nameStyle, c.Accent)
		gap := max(iw-2-lipgloss.Width(name)-lipgloss.Width(keys), 1)
		lines = append(lines, rs.Width(iw).Render(onPlane(glyph+rs.Render(" ")+name+rs.Render(strings.Repeat(" ", gap))+keys, rowBg)))
	}
	if len(items) == 0 {
		lines = append(lines, mutedStyle().Render("no matches"))
	}
	lines = append(lines, "", bg.Foreground(c.Muted).Render("↑/↓ choose · Enter run · Esc close"))
	return dialogStyle().Width(w).Padding(1, 2).Render(onPlane(strings.Join(lines, "\n"), c.Overlay))
}
