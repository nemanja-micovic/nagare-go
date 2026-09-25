package picker

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/nemanja-micovic/nagare-go/internal/log"
	"github.com/nemanja-micovic/nagare-go/internal/theme"
)

// handleThemePickKey handles keys when the theme picker overlay is open.
func (m Model) handleThemePickKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case keyEscape:
		theme.Set(m.themeOriginal)
		m.showThemePick = false
	case keyEnter:
		log.Info("theme confirmed: %s", m.themeNames[m.themeCursor])
		m.showThemePick = false
	case keyUp:
		if m.themeCursor > 0 {
			m.themeCursor--
			theme.Set(m.themeNames[m.themeCursor])
		}
	case keyDown:
		if m.themeCursor < len(m.themeNames)-1 {
			m.themeCursor++
			theme.Set(m.themeNames[m.themeCursor])
		}
	}
	return m, nil
}

// themePickOverlay renders the theme selection dialog.
func themePickOverlay(names []string, cursor int, width, height int) string {
	c := theme.Current().Colors

	itemWidth := 24
	title := sectionHeader("Select Theme", itemWidth)
	hint := mutedStyle().Render("↑/↓ preview · Enter confirm · Esc cancel")

	lines := []string{title, ""}

	for i, name := range names {
		// Color swatch from the theme's palette
		swatch := ""
		if t := theme.Get(name); t != nil {
			tc := t.Colors
			swatch = lipgloss.NewStyle().Foreground(tc.Primary).Render("●") +
				lipgloss.NewStyle().Foreground(tc.Secondary).Render("●") +
				lipgloss.NewStyle().Foreground(tc.Accent).Render("●") +
				lipgloss.NewStyle().Foreground(tc.Success).Render("●") +
				lipgloss.NewStyle().Foreground(tc.Warning).Render("●") +
				lipgloss.NewStyle().Foreground(tc.Error).Render("●")
		}

		label := fmt.Sprintf("  %s  %s", name, swatch)
		style := lipgloss.NewStyle().Foreground(c.Foreground).Width(itemWidth)
		if i == cursor {
			// Match the list/grid convention: background tint only, no caret.
			style = style.Background(c.SelBg).Bold(true)
		}
		lines = append(lines, style.Render(label))
	}

	lines = append(lines, "", hint)

	return dialogStyle().
		Width(itemWidth+8).
		Height(len(names)+8).
		Padding(1, 2).
		Render(onPlane(strings.Join(lines, "\n"), c.Overlay))
}
