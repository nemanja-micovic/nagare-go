package picker

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/nemke/nagare-go/internal/models"
	"github.com/nemke/nagare-go/internal/theme"
)

// hint is one key and what it does right now.
type hint struct{ key, label string }

// hintsFor returns the keys actually available in the picker's current state.
//
// The bar used to list all twenty bindings unconditionally, which wrapped onto a
// second line and cost a row of the session list. Worse, it advertised keys that
// would do nothing: ^y on an idle session, ^x on a saved one. A footer that
// changes with context is both shorter and truthful, and the full set stays one
// F1 away — the progressive-disclosure convention zellij uses for its modes.
func hintsFor(m Model) []hint {
	if m.mail != nil {
		return mailHints(m.mail)
	}
	// An open overlay or an input mode owns the keyboard, so it owns the footer.
	switch {
	case m.showHelp:
		return []hint{{"F1 / Esc", "Close"}}
	case m.showThemePick:
		return []hint{{"↑/↓", "Preview"}, {"Enter", "Keep"}, {"Esc", "Cancel"}}
	case m.confirmMode:
		return []hint{{"y", "Remove worktree"}, {"n / Esc", "Keep it"}}
	case m.promptMode:
		return []hint{{"Enter", "Send"}, {"Esc", "Cancel"}}
	case m.renameMode:
		return []hint{{"Enter", "Save name"}, {"Esc", "Cancel"}}
	case m.worktreeMode:
		return []hint{{"Enter", "Create worktree"}, {"Esc", "Cancel"}}
	}

	hints := []hint{}
	s, ok := m.selectedSession()
	saved := ok && s.Status == models.StatusSaved

	if ok {
		if saved {
			hints = append(hints, hint{"Enter", "Load"})
		} else {
			hints = append(hints, hint{"Enter", "Jump"})
		}
	}
	hints = append(hints, hint{"↑/↓", "Navigate"})

	// Order below is drop order: helpBar trims from the end to fit one line, so
	// the most useful action for the current selection has to come first.
	//
	// The queue leads. If anything is waiting on the user, getting to it is the
	// most useful thing the picker can offer, and the count is worth showing —
	// "3 waiting" is a different situation from "1 waiting".
	if n := waitingCount(m.filtered); n > 0 {
		label := "Next waiting"
		if n > 1 {
			label = fmt.Sprintf("Next of %d waiting", n)
		}
		hints = append(hints, hint{"F4", label})
	}
	// Then approval, because a waiting agent is blocked until it arrives.
	if ok && approvable(s.Status) {
		hints = append(hints, hint{"^y", "Allow"}, hint{"^a", "Always"})
	}
	if ok && !saved {
		hints = append(hints, hint{"^l", "Prompt"})
		// Ctrl+x offers worktree removal on a worktree pane, and says so rather
		// than leaving the extra prompt as a surprise.
		if s.Details.Worktree != "" {
			hints = append(hints, hint{"^x", "Kill + remove"})
		} else {
			hints = append(hints, hint{"^x", "Kill"})
		}
	}
	hints = append(hints, hint{"Tab", "View"})
	if ok && !saved {
		hints = append(hints, hint{"^w", "Unload"})
	}
	if ok && !saved {
		hints = append(hints, hint{"F2", "Name task"})
	}
	if ok {
		hints = append(hints, hint{"F3", "Worktree"})
	}
	hints = append(hints, hint{"^n", "New"}, hint{"F5", "Mailbox"}, hint{"^f", "Star"}, hint{"^o", "Sort"})

	return hints
}

// helpBar renders the bottom hint bar. It is always exactly one line: hints are
// dropped from the end until they fit, and the trailing "F1 More · Esc Quit" is
// reserved space so there is always a visible way to reach the rest and to get
// out.
func helpBar(m Model, width int) string {
	c := theme.Current().Colors
	keyStyle := lipgloss.NewStyle().Foreground(c.Accent).Bold(true)
	sep := lipgloss.NewStyle().Foreground(c.Muted).Render(" │ ")
	sepWidth := lipgloss.Width(sep)

	render := func(h hint) string {
		return keyStyle.Render(h.key) + " " + mutedStyle().Render(h.label)
	}

	tail := []hint{{"F1", "More"}, {"Esc", "Quit"}}
	if m.showHelp || m.showThemePick || m.confirmMode || m.promptMode ||
		m.renameMode || m.worktreeMode {
		// A mode's own footer already names its exit; "More"/"Quit" would be
		// wrong there, since F1 and Esc mean something else.
		tail = nil
	}
	if m.mail != nil {
		// Esc leaves the mailbox rather than quitting, and the way back is
		// reserved space like any exit. The reader and dialogs name their own.
		tail = nil
		if m.mail.confirm == nil && !m.mail.reader {
			tail = []hint{{"Esc", "Back"}}
		}
	}

	var tailParts []string
	tailWidth := 0
	for _, h := range tail {
		part := render(h)
		tailParts = append(tailParts, part)
		tailWidth += lipgloss.Width(part) + sepWidth
	}

	// Available content width, inside this style's horizontal padding.
	budget := width - 2 - tailWidth

	var parts []string
	used := 0
	for _, h := range hintsFor(m) {
		part := render(h)
		cost := lipgloss.Width(part)
		if len(parts) > 0 {
			cost += sepWidth
		}
		if used+cost > budget {
			break
		}
		parts = append(parts, part)
		used += cost
	}
	parts = append(parts, tailParts...)

	return lipgloss.NewStyle().
		Foreground(c.Muted).
		Background(canvasBg()).
		Width(width).
		MaxHeight(1).
		Padding(0, 1).
		Render(onPlane(strings.Join(parts, sep), canvasBg()))
}

// helpSection is a titled group of key bindings on the F1 screen.
type helpSection struct {
	title string
	lines [][2]string // key, description; an empty key is a continuation note
}

// helpColumns splits the bindings into two groups of roughly equal height, so the
// screen can be laid out side by side. Actions is on its own because it is longer
// than everything else combined.
func helpColumns() ([]helpSection, []helpSection) {
	left := []helpSection{
		{"Navigation", [][2]string{
			{"↑ / ↓", "Move cursor up/down"},
			{"← / →", "Move cursor (grid view)"},
			{"Enter", "Jump to selection"},
			{"F4", "Next session waiting on you"},
			{"Esc", "Quit nagare"},
		}},
		{"Views", [][2]string{
			{"Tab", "Toggle list / grid view"},
			{"Ctrl+t", "Pick a color theme"},
			{"Ctrl+s", "Show saved sessions"},
			{"F1", "Toggle this screen"},
		}},
		{"Search", [][2]string{
			{"Type", "Fuzzy match name or path"},
			{"", "Best match is auto-selected"},
		}},
		{"Mouse", [][2]string{
			{"Click", "Select; click again to jump"},
			{"Wheel", "Move the selection"},
			{"Click away", "Close this screen"},
			{"", "Off: picker.mouse = false"},
		}},
	}
	right := []helpSection{
		{"Agent", [][2]string{
			{"Ctrl+y", "Approve permission"},
			{"Ctrl+a", "Approve always"},
			{"Ctrl+l", "Send inline prompt"},
			{"Ctrl+g", "Send prompt via $EDITOR"},
		}},
		{"Sessions", [][2]string{
			{"Ctrl+n", "Create new session"},
			{"Ctrl+r", "Quick prototype"},
			{"F2", "Name selected task"},
			{"F3", "New git worktree"},
			{"Ctrl+f", "Toggle star"},
			{"Ctrl+o", "Cycle sort mode"},
		}},
		{"Teardown", [][2]string{
			{"Ctrl+w", "Unload agent (kill pane)"},
			{"Ctrl+x", "Kill window"},
			{"", "Offers worktree removal"},
		}},
		{"Mailbox", [][2]string{
			{"F5", "Messages between agents"},
			{"←/→ Tab", "Filter · conversations"},
			{"Ctrl+x", "Delete a message"},
			{"Ctrl+d", "Clean up old messages"},
		}},
		{"Config", [][2]string{
			{"Ctrl+e", "Edit config file"},
		}},
	}
	return left, right
}

// helpOverlay renders the full help screen shown on F1.
//
// Two columns, and sized to its content rather than to a fraction of the
// terminal. A single column ran to some 44 rows, which overflowed the box on any
// terminal shorter than that and got silently clipped — and because an oversized
// dialog's centered position clamps to the top of the frame, it also defeated the
// entry animation.
func helpOverlay(width, height int) string {
	c := theme.Current().Colors

	// Dialog width, then the content width inside border (2) and padding (2*3).
	boxWidth := min(width*3/4, 94)
	innerWidth := boxWidth - 8
	columns := 2
	if innerWidth < 68 {
		columns = 1
	}
	colWidth := innerWidth
	if columns == 2 {
		colWidth = (innerWidth - 2) / 2
	}

	keyStyle := lipgloss.NewStyle().Foreground(c.Accent).Width(11)
	descStyle := lipgloss.NewStyle().Foreground(c.Foreground)

	renderSections := func(sections []helpSection, first bool) string {
		var out []string
		for i, sec := range sections {
			if i > 0 || !first {
				out = append(out, "")
			}
			out = append(out, sectionHeader(sec.title, colWidth))
			for _, l := range sec.lines {
				out = append(out, fmt.Sprintf("%s %s",
					keyStyle.Render(l[0]), descStyle.Render(l[1])))
			}
		}
		return lipgloss.NewStyle().Width(colWidth).Render(strings.Join(out, "\n"))
	}

	left, right := helpColumns()

	var body string
	if columns == 2 {
		gap := lipgloss.NewStyle().Width(2).Render("")
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			renderSections(left, true), gap, renderSections(right, true))
	} else {
		body = renderSections(append(left, right...), true)
	}

	content := sectionHeader("Keyboard Shortcuts", innerWidth) + "\n\n" + body +
		"\n\n" + mutedStyle().Render("Press F1 or Esc to close")

	return dialogStyle().
		Width(boxWidth).
		// No fixed height: the box takes the height its content needs, capped so
		// it can never exceed the frame it will be centered in.
		MaxHeight(height-2).
		Padding(1, 3).
		Render(onPlane(content, c.Overlay))
}
