package picker

import (
	"time"

	"charm.land/lipgloss/v2"

	"github.com/nemke/nagare-go/internal/theme"
)

// Toasts tell you about the agents you are not looking at.
//
// In focus mode the user is heads-down in one agent. The sidebar still flashes
// when another one starts waiting or finishes, but a sidebar is peripheral by
// design — so the same two transitions also raise a toast in the corner of the
// tiles, where the eye already is. A toast never takes the keyboard; F4 is the
// way to act on one.

const (
	toastFor  = 5 * time.Second
	maxToasts = 3
)

type toast struct {
	key   string // session the toast is about
	text  string
	kind  flashKind
	until time.Time
}

// pushToasts raises a toast for each transition worth announcing, except for
// the agent that already has the keyboard: the user is looking right at it.
func (m *Model) pushToasts(flashes map[string]flashState, now time.Time) {
	if !m.focus.on || len(flashes) == 0 {
		return
	}
	active := m.focus.cur().key
	// A new slice rather than an append: Model copies must not share one.
	next := make([]toast, 0, len(m.toasts)+len(flashes))
	for _, t := range m.toasts {
		if _, replaced := flashes[t.key]; !replaced {
			next = append(next, t)
		}
	}
	for key, f := range flashes {
		if key == active {
			continue
		}
		s, ok := m.sessionFor(key)
		if !ok {
			continue
		}
		label := childLabel(s)
		if repo := groupKeyOf(s); repo != "" && repo != label {
			label = repo + " / " + label
		}
		text := label + " finished"
		if f.kind == flashAttention {
			text = label + " needs you · F4"
		}
		next = append(next, toast{key: key, text: text, kind: f.kind, until: now.Add(toastFor)})
	}
	if len(next) > maxToasts {
		next = next[len(next)-maxToasts:]
	}
	m.toasts = next
}

// pruneToasts drops expired toasts, returning a new slice when anything goes.
func pruneToasts(ts []toast, now time.Time) []toast {
	keep := 0
	for _, t := range ts {
		if now.Before(t.until) {
			keep++
		}
	}
	if keep == len(ts) {
		return ts
	}
	out := make([]toast, 0, keep)
	for _, t := range ts {
		if now.Before(t.until) {
			out = append(out, t)
		}
	}
	return out
}

// drawToasts stacks the live toasts in the top-right corner of the frame.
func (m Model) drawToasts(frame string, width, height int) string {
	if len(m.toasts) == 0 || width < 30 {
		return frame
	}
	c := theme.Current().Colors
	layers := []*lipgloss.Layer{lipgloss.NewLayer(frame).X(0).Y(0).Z(0)}
	y := 1
	for i, t := range m.toasts {
		accent := c.Success
		glyph := "✓"
		if t.kind == flashAttention {
			accent, glyph = c.Warning, "●"
		}
		bg := lipgloss.NewStyle().Background(c.Overlay)
		pill := bg.Foreground(accent).Render("▌") +
			bg.Foreground(accent).Bold(true).Render(" "+glyph+" ") +
			bg.Foreground(c.Foreground).Render(truncate(t.text, width/2)+" ")
		x := max(width-lipgloss.Width(pill)-2, 0)
		layers = append(layers, lipgloss.NewLayer(pill).X(x).Y(y).Z(1+i))
		y += 2
		if y >= height-1 {
			break
		}
	}
	return lipgloss.NewStyle().MaxWidth(width).MaxHeight(height).
		Render(lipgloss.NewCompositor(layers...).Render())
}
