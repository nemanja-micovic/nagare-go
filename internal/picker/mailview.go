package picker

import (
	"fmt"
	"hash/fnv"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/nemke/nagare-go/internal/theme"
)

// Layout constants for the mailbox.
const (
	// mailSplitWidth is the narrowest terminal that shows the list and the
	// message side by side. Below it the list takes the screen, and Enter
	// opens the message full screen instead.
	mailSplitWidth = 100
	// mailListMin is the narrowest the list panel gets in the split layout.
	mailListMin = 44
	// mailStateWidth is the state column ("needs reply" plus a space).
	mailStateWidth = 12
	// mailStateColumnMin is the narrowest list row that still has room for
	// the state column; below it the glyph alone carries the state.
	mailStateColumnMin = 56
)

// viewMailbox draws the mailbox, which replaces the whole picker frame while
// it is open.
func (m Model) viewMailbox() (string, hitTargets) {
	b := m.mail
	hits := hitTargets{}

	header := m.mailHeader()
	footer := helpBar(m, m.width)
	bodyHeight := max(m.height-lipgloss.Height(header)-lipgloss.Height(footer), 3)
	bodyTop := lipgloss.Height(header)

	var body string
	switch {
	case b.reader:
		body = m.mailDetail(m.width, bodyHeight, true)
	case m.width < mailSplitWidth:
		body, hits.sessionAt = m.mailList(m.width, bodyHeight, bodyTop)
		hits.listWidth = m.width
	default:
		listW := max(m.width*2/5, mailListMin)
		var list string
		list, hits.sessionAt = m.mailList(listW, bodyHeight, bodyTop)
		hits.listWidth = listW
		body = lipgloss.JoinHorizontal(lipgloss.Top, list, m.mailDetail(m.width-listW, bodyHeight, false))
	}

	frame := header + "\n" + body + "\n" + footer

	if b.confirm != nil {
		overlay := m.mailConfirmOverlay()
		dy := m.overlayAnim.offset()
		hits = hitTargets{dialog: overlayRect(m.width, m.height, overlay, dy)}
		return placeOverlay(m.width, m.height, overlay, frame, dy), hits
	}
	return frame, hits
}

// canvasRow renders one full-width row on the canvas plane.
func canvasRow(content string, width int) string {
	return lipgloss.NewStyle().
		Background(canvasBg()).
		Width(width).
		MaxWidth(width).
		MaxHeight(1).
		Padding(0, 1).
		Render(onPlane(content, canvasBg()))
}

// mailHeader is the title and totals row, then the filter tabs.
func (m Model) mailHeader() string {
	b := m.mail
	c := theme.Current().Colors
	sep := mutedStyle().Render(" · ")

	title := lipgloss.NewStyle().Foreground(c.Primary).Bold(true).Render("✉ Mailbox")
	var stats []string
	pairs := map[[2]string]bool{}
	var bytes int64
	var oldest time.Duration
	for _, e := range b.all {
		a, z := pairKey(e.env.FromSession, e.env.ToSession)
		pairs[[2]string{a, z}] = true
		bytes += e.env.Size
		oldest = max(oldest, e.age)
	}
	counts := b.counts()
	stats = append(stats, subtleStyle().Render(fmt.Sprintf("%d message%s", len(b.all), plural(len(b.all)))))
	if len(b.all) > 0 {
		stats = append(stats,
			mutedStyle().Render(fmt.Sprintf("%d conversation%s", len(pairs), plural(len(pairs)))),
			mutedStyle().Render(humanBytes(bytes)),
			mutedStyle().Render("oldest "+shortAge(oldest)))
	}
	top := title + "   " + strings.Join(stats, sep)
	switch {
	case b.err != "":
		top += "   " + lipgloss.NewStyle().Foreground(c.Error).Render(b.err)
	case b.note != "":
		top += "   " + lipgloss.NewStyle().Foreground(c.Success).Render(b.note)
	}

	// Tabs. The selected one is filled; a non-empty problem count is red even
	// when unselected, since that is the tab worth noticing.
	var tabs []string
	for f := mailFilter(0); f < filterCount; f++ {
		label := fmt.Sprintf(" %s %d ", f.label(), counts[f])
		st := lipgloss.NewStyle().Foreground(c.Muted)
		if f == filterProblems && counts[f] > 0 {
			st = st.Foreground(c.Error)
		}
		if f == b.filter {
			st = lipgloss.NewStyle().Foreground(c.Background).Background(c.Primary).Bold(true)
		}
		tabs = append(tabs, st.Render(label))
	}
	view := "timeline"
	if b.threads {
		view = "conversations"
	}
	right := mutedStyle().Render("sort ") + subtleStyle().Render(b.sort.label()) +
		sep + mutedStyle().Render("view ") + subtleStyle().Render(view)
	left := strings.Join(tabs, " ")
	gap := m.width - 2 - lipgloss.Width(left) - lipgloss.Width(right)
	tabRow := left
	if gap >= 2 {
		tabRow = left + strings.Repeat(" ", gap) + right
	}

	return canvasRow(top, m.width) + "\n" + canvasRow(tabRow, m.width)
}

// mailList draws the list panel. It returns, for mouse hit-testing, the frame
// row each message was drawn on; top is the frame row the panel starts at.
func (m Model) mailList(width, height, top int) (string, map[int]int) {
	b := m.mail
	inner := width - 4 // border and padding, one cell each side
	innerH := height - 4
	rowAt := map[int]int{}

	// The input only knows how wide it is once told; unset, it shows the
	// first letter of its placeholder and nothing more.
	b.search.SetWidth(max(inner-4, 1))
	lines := []string{b.search.View(), ""}
	listH := max(innerH-len(lines), 1)

	switch {
	case len(b.all) == 0:
		lines = append(lines, mutedStyle().Width(inner).Render(
			"No messages yet. When agents message each other, every message and its reply show up here."))
	case len(b.rows) == 0:
		lines = append(lines, mutedStyle().Width(inner).Render(
			fmt.Sprintf("Nothing in %s matches. ←/→ changes the filter; Esc clears the search.", b.filter.label())))
	default:
		cursorRow := 0
		for i, r := range b.rows {
			if r.group == nil && !r.blank && r.entry == b.cursor {
				cursorRow = i
			}
		}
		// Keep the cursor visible, and its conversation header with it when
		// there is room, so a message is never shown without its context.
		start := 0
		if cursorRow >= listH {
			start = cursorRow - listH + 1
		}
		end := min(start+listH, len(b.rows))
		firstRow := top + 1 + 1 + len(lines) // border, padding, search and gap
		for i := start; i < end; i++ {
			r := b.rows[i]
			switch {
			case r.blank:
				lines = append(lines, "")
			case r.group != nil:
				lines = append(lines, m.mailGroupRow(r.group, inner))
			default:
				rowAt[firstRow+i-start] = r.entry
				lines = append(lines, m.mailRow(b.shown[r.entry], inner, r.entry == b.cursor))
			}
		}
	}

	content := onPlane(strings.Join(lines, "\n"), surfaceBg())
	return fitBox(primaryPanelStyle(), width, height).Render(content), rowAt
}

// nameColor gives each agent a color of its own, so a sender can be followed
// down the list by color before its name is read. Colors go by position in
// the sorted list of agents rather than by hash, which guarantees distinct
// colors for as many agents as the palette has — a hash put "api" and "web" on
// the same blue. Status colors are left out: they already mean something.
func (b *mailbox) nameColor(name string) color.Color {
	c := theme.Current().Colors
	palette := []color.Color{
		c.Primary, c.Secondary, c.Accent,
		theme.Mix(c.Secondary, c.Error, 0.5), // rose
		theme.Mix(c.Accent, c.Success, 0.5),  // teal
	}
	i, ok := b.agentIndex[name]
	if !ok {
		h := fnv.New32a()
		h.Write([]byte(name))
		i = int(h.Sum32() % uint32(len(palette)))
	}
	return palette[i%len(palette)]
}

// mailGroupRow is a conversation header: the two agents, how many messages,
// what is still open or wrong, and when it last moved.
func (m Model) mailGroupRow(g *mailGroup, width int) string {
	c := theme.Current().Colors
	bg := surfaceBg()
	seg := func(s string, fg color.Color, bold bool) string {
		return lipgloss.NewStyle().Foreground(fg).Background(bg).Bold(bold).Render(s)
	}

	left := seg(g.a, m.mail.nameColor(g.a), true) + seg(" ⇄ ", c.Muted, false) + seg(g.b, m.mail.nameColor(g.b), true)
	info := seg(fmt.Sprintf(" · %d message%s", len(g.entries), plural(len(g.entries))), c.Muted, false)
	if g.open > 0 {
		info += seg(fmt.Sprintf(" · %d open", g.open), c.Warning, false)
	}
	if g.problem > 0 {
		info += seg(fmt.Sprintf(" · %d problem%s", g.problem, plural(g.problem)), c.Error, false)
	}
	age := seg(shortAge(m.mail.now().Sub(g.latest)), c.Muted, false)

	row := left + info
	room := width - lipgloss.Width(age) - 1
	if lipgloss.Width(row) > room {
		row = truncate(row, room)
	}
	pad := width - lipgloss.Width(row) - lipgloss.Width(age)
	return row + seg(strings.Repeat(" ", max(pad, 1)), c.Muted, false) + age
}

// mailRow is one message in the list. Every segment carries the row's own
// background: a segment rendered without one ends in a reset that punches a
// hole in the selection tint.
func (m Model) mailRow(e mailEntry, width int, selected bool) string {
	b := m.mail
	c := theme.Current().Colors
	bg := surfaceBg()
	if selected {
		bg = c.SelBg
	}
	seg := func(s string, fg color.Color, bold bool) string {
		return lipgloss.NewStyle().Foreground(fg).Background(bg).Bold(bold).Render(s)
	}

	var left string
	if b.threads {
		left = seg("  ", c.Muted, false)
	}
	left += seg(e.state.glyph()+" ", e.state.color(c), true)
	from, to := e.env.FromSession, e.env.ToSession
	if b.threads {
		// The header already names both sides; the sender is what changes.
		left += seg(truncate(from, max(width/3, 8)), m.mail.nameColor(from), selected) + seg(" →", c.Muted, false)
	} else {
		who := max(width/3, 12)
		left += seg(truncate(from, who/2), m.mail.nameColor(from), selected) +
			seg(" → ", c.Muted, false) +
			seg(truncate(to, who/2), m.mail.nameColor(to), selected)
	}
	left += seg("  ", c.Muted, false)
	if width >= mailStateColumnMin {
		left += seg(fmt.Sprintf("%-*s", mailStateWidth, e.state.label()), e.state.color(c), false)
	}

	age := seg(" "+shortAge(e.age), c.Muted, false)
	room := width - lipgloss.Width(left) - lipgloss.Width(age)
	previewColor := c.Foreground
	if !selected {
		previewColor = c.Subtle
	}
	preview := ""
	if room > 0 {
		text := previewLine(e.env.Content)
		if e.env.InReplyTo != "" {
			text = "↩ " + text
		}
		preview = seg(truncate(text, room), previewColor, false)
	}
	pad := max(width-lipgloss.Width(left)-lipgloss.Width(preview)-lipgloss.Width(age), 0)
	return left + preview + seg(strings.Repeat(" ", pad), c.Muted, false) + age
}

// previewLine is a message's first line of prose, with markdown punctuation
// stripped so the list shows words rather than "## " and "**".
func previewLine(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "#>-*` ")
		line = strings.NewReplacer("**", "", "__", "", "`", "").Replace(line)
		if line != "" {
			return line
		}
	}
	return "(empty)"
}

// mailDetail draws the selected message: who, when, where it got to, and what
// was said, with the reply beneath it. focus marks the full-screen reader.
func (m Model) mailDetail(width, height int, focus bool) string {
	b := m.mail
	style := panelStyle()
	if focus {
		style = primaryPanelStyle()
	}
	inner := width - 4
	innerH := height - 4

	e, ok := b.selected()
	if !ok {
		msg := mutedStyle().Width(inner).Render("Select a message to read it.")
		return fitBox(style, width, height).Render(onPlane(msg, surfaceBg()))
	}

	rows := m.mailDetailRows(e, inner)

	// Clamp here, where the content's height is known, so scrolling past the
	// end stops rather than accumulating presses that must be undone.
	maxScroll := max(len(rows)-innerH, 0)
	b.scroll = min(max(b.scroll, 0), maxScroll)
	view := rows[b.scroll:min(b.scroll+innerH, len(rows))]
	if maxScroll > 0 {
		pct := b.scroll * 100 / maxScroll
		marker := mutedStyle().Render(fmt.Sprintf("── %d%% · PgUp/PgDn to scroll ──", pct))
		if len(view) == innerH {
			view[len(view)-1] = marker
		}
	}
	return fitBox(style, width, height).Render(onPlane(strings.Join(view, "\n"), surfaceBg()))
}

// mailDetailRows lays the message out as rows no wider than width.
func (m Model) mailDetailRows(e mailEntry, width int) []string {
	b := m.mail
	c := theme.Current().Colors
	env := e.env
	bold := func(s string, fg color.Color) string {
		return lipgloss.NewStyle().Foreground(fg).Bold(true).Render(s)
	}
	wrap := lipgloss.NewStyle().Width(width)

	var out []string
	add := func(s string) {
		out = append(out, strings.Split(wrap.Render(s), "\n")...)
	}
	field := func(label, value string) {
		add(mutedStyle().Render(fmt.Sprintf("%-9s", label)) + " " + value)
	}

	add(bold(env.FromSession, m.mail.nameColor(env.FromSession)) + mutedStyle().Render("  →  ") +
		bold(env.ToSession, m.mail.nameColor(env.ToSession)))
	add("")

	status := bold(e.state.glyph()+" "+strings.ToUpper(e.state.label()[:1])+e.state.label()[1:], e.state.color(c))
	if env.Waiting {
		status += lipgloss.NewStyle().Foreground(c.Warning).Render("  · sender is waiting")
	}
	field("Status", status)
	field("Sent", subtleStyle().Render(stamp(env.Sent())+" · "+agoText(e.age)))
	if t := env.Answered(); !t.IsZero() {
		how := ""
		if env.AutoReply {
			how = " · automatic"
		}
		field("Replied", subtleStyle().Render(stamp(t)+" · "+shortAge(t.Sub(env.Sent()))+" later"+how))
	}
	switch {
	case env.InReplyTo != "":
		kind := "Reply to " + env.InReplyTo
		if orig, ok := b.find(env.InReplyTo); ok {
			kind = fmt.Sprintf("Reply to “%s”", truncate(previewLine(orig.env.Content), max(width-22, 10)))
		}
		field("Kind", subtleStyle().Render(kind))
	case env.ExpectsReply:
		field("Kind", subtleStyle().Render("Question — the sender asked for a reply"))
	default:
		field("Kind", subtleStyle().Render("Note — no reply asked for"))
	}
	field("ID", mutedStyle().Render(env.ID))
	field("Filed", mutedStyle().Render("messages/"+env.Inbox+" · "+humanBytes(env.Size)))
	add("")

	if why := explain(e); why != "" {
		fg := c.Subtle
		if e.problem() {
			fg = c.Error
		} else if e.open() {
			fg = c.Warning
		}
		add(lipgloss.NewStyle().Foreground(fg).Render(why))
		add("")
	}

	out = append(out, sectionHeader("Message", width))
	out = append(out, strings.Split(b.md.render(env.Content, width), "\n")...)

	if env.Response != nil {
		out = append(out, "")
		title := "Reply from " + env.ToSession
		if t := env.Answered(); !t.IsZero() {
			title += " · " + stamp(t)
		}
		out = append(out, sectionHeader(title, width))
		out = append(out, strings.Split(b.md.render(*env.Response, width), "\n")...)
	}

	// Replies that came back as messages of their own, so the thread can be
	// followed from either end.
	var followups []mailEntry
	for _, other := range b.all {
		if other.env.InReplyTo == env.ID {
			followups = append(followups, other)
		}
	}
	if len(followups) > 0 {
		out = append(out, "", sectionHeader("Replies in this thread", width))
		for _, f := range followups {
			add(bold(f.state.glyph(), f.state.color(c)) + " " +
				lipgloss.NewStyle().Foreground(m.mail.nameColor(f.env.FromSession)).Render(f.env.FromSession) +
				mutedStyle().Render(" · "+agoText(f.age)+" · ") +
				truncate(previewLine(f.env.Content), max(width-30, 10)))
		}
	}
	return out
}

// find looks a message up by id among everything loaded.
func (b *mailbox) find(id string) (mailEntry, bool) {
	for _, e := range b.all {
		if e.env.ID == id {
			return e, true
		}
	}
	return mailEntry{}, false
}

// explain says in plain words where a message got to — the question the
// mailbox exists to answer when something seems to have gone missing.
func explain(e mailEntry) string {
	env := e.env
	to, from := env.ToSession, env.FromSession
	var why string
	switch e.state {
	case mailQueued:
		why = fmt.Sprintf("Waiting for %s, who was busy when it was sent. It is delivered at their next tool call, or as soon as they finish their turn.", to)
		if e.age > staleQueue {
			why += fmt.Sprintf(" It has waited %s — %s may be stuck on a prompt.", shortAge(e.age), to)
		}
	case mailLost:
		why = fmt.Sprintf("Never delivered: no queued copy is left and %s never read it. %s was most likely restarted or renamed before it arrived. Resend it, or delete it with Ctrl+x.", to, to)
	case mailNeedsReply:
		why = fmt.Sprintf("%s has it, and %s asked for an answer that has not come yet.", to, from)
		if env.Waiting {
			why += fmt.Sprintf(" %s is blocked waiting for it right now.", from)
		}
	case mailDelivered:
		why = fmt.Sprintf("Delivered to %s. No reply was asked for.", to)
	case mailAnswered:
		why = fmt.Sprintf("%s answered %s after it was sent.", to, shortAge(env.Answered().Sub(env.Sent())))
		if env.AutoReply {
			why = fmt.Sprintf("%s answered %s after it was sent — its final message, sent back automatically.", to, shortAge(env.Answered().Sub(env.Sent())))
		}
	}
	if e.gone && e.open() {
		why += fmt.Sprintf(" No running agent is called %s any more, so it may never arrive — the agent exited or was renamed.", to)
	}
	return why
}

// mailConfirmOverlay asks before deleting anything: deletion cannot be undone,
// and a queued message deleted is a message that will never arrive.
func (m Model) mailConfirmOverlay() string {
	b := m.mail
	c := theme.Current().Colors
	conf := b.confirm
	title := lipgloss.NewStyle().Foreground(c.Warning).Bold(true)
	width := min(64, m.width-6)
	wrap := lipgloss.NewStyle().Width(width)

	var parts []string
	if conf.cleanup {
		age := cleanupAges[conf.ageIdx]
		victims := b.cleanupCandidates(age.age)
		var bytes int64
		for _, e := range victims {
			bytes += e.env.Size
		}
		parts = append(parts, title.Render("Clean up old messages"), "")
		parts = append(parts, "Older than  "+
			lipgloss.NewStyle().Foreground(c.Accent).Bold(true).Render("‹ "+age.label+" ›"))
		parts = append(parts, "")
		if len(victims) == 0 {
			parts = append(parts, mutedStyle().Render("Nothing that old to delete."))
		} else {
			parts = append(parts, lipgloss.NewStyle().Foreground(c.Foreground).Bold(true).Render(
				fmt.Sprintf("%d message%s · %s", len(victims), plural(len(victims)), humanBytes(bytes))))
			parts = append(parts, wrap.Foreground(c.Muted).Render(
				"Delivered, answered and undelivered messages go. Queued and unanswered ones are kept, so no conversation still in progress is cut."))
		}
		parts = append(parts, "", mutedStyle().Render("←/→ age    y delete    n / esc cancel"))
	} else {
		e := conf.target
		parts = append(parts, title.Render("Delete message"), "")
		parts = append(parts,
			lipgloss.NewStyle().Foreground(m.mail.nameColor(e.env.FromSession)).Bold(true).Render(e.env.FromSession)+
				mutedStyle().Render(" → ")+
				lipgloss.NewStyle().Foreground(m.mail.nameColor(e.env.ToSession)).Bold(true).Render(e.env.ToSession)+
				mutedStyle().Render(" · "+agoText(e.age)))
		parts = append(parts, wrap.Foreground(c.Subtle).Render(truncate(previewLine(e.env.Content), width)))
		if e.open() {
			parts = append(parts, "", wrap.Foreground(c.Warning).Render(
				fmt.Sprintf("It is still %s — deleting it means it will never be delivered or answered.", e.state.label())))
		}
		parts = append(parts, "", mutedStyle().Render("y delete    n / esc keep"))
	}
	return dialogStyle().Padding(1, 2).Render(onPlane(strings.Join(parts, "\n"), c.Overlay))
}

// mailHints are the footer hints while the mailbox is open, in drop order.
func mailHints(b *mailbox) []hint {
	switch {
	case b.confirm != nil && b.confirm.cleanup:
		return []hint{{"y", "Delete"}, {"n / Esc", "Cancel"}, {"←/→", "Age"}}
	case b.confirm != nil:
		return []hint{{"y", "Delete"}, {"n / Esc", "Keep"}}
	case b.reader:
		return []hint{{"Esc / Enter", "Back"}, {"↑/↓", "Scroll"}, {"PgUp/PgDn", "Page"}, {"^x", "Delete"}}
	}
	return []hint{
		{"Enter", "Read"}, {"↑/↓", "Select"}, {"←/→", "Filter"}, {"Type", "Search"},
		{"Tab", "Conversations/Timeline"}, {"^o", "Sort"}, {"PgUp/PgDn", "Scroll"},
		{"^x", "Delete"}, {"^d", "Clean up"},
	}
}

// handleMailMouse routes mouse intent to the mailbox: a click selects a
// message, a second click opens it, and the wheel moves the selection — or
// scrolls, in the reader.
func (m Model) handleMailMouse(msg tea.Msg) Model {
	b := m.mail
	if b.confirm != nil {
		return m
	}
	switch msg := msg.(type) {
	case mouseSelectMsg:
		if msg.index >= 0 && msg.index < len(b.shown) {
			b.cursor, b.scroll = msg.index, 0
		}
	case mouseActivateMsg:
		if msg.index >= 0 && msg.index < len(b.shown) {
			b.cursor, b.scroll, b.reader = msg.index, 0, true
		}
	case mouseScrollMsg:
		if b.reader {
			b.scroll = max(b.scroll+msg.delta*3, 0)
		} else if len(b.shown) > 0 {
			b.cursor = min(max(b.cursor+msg.delta, 0), len(b.shown)-1)
			b.scroll = 0
		}
	}
	return m
}

// shortAge is a compact duration: "now", "5m", "3h", "2d", "6w".
func shortAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dw", int(d.Hours()/24/7))
	}
}

// agoText is shortAge as a phrase: "just now", "5m ago".
func agoText(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	return shortAge(d) + " ago"
}

// stamp is a local timestamp, with the year only when it is not this one.
func stamp(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	t = t.Local()
	if t.Year() != time.Now().Year() {
		return t.Format("Jan 2 2006 15:04")
	}
	return t.Format("Jan 2 15:04")
}

func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/1024/1024)
	}
}
