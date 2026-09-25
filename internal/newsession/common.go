package newsession

import (
	"errors"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/nemanja-micovic/nagare-go/internal/theme"
)

var errEmptyName = errors.New("name is required")

// nagareFormTheme implements huh.Theme by building a *huh.Styles on demand
// from the active nagare theme. Honours the terminal's dark/light preference
// via the isDark flag that huh passes in at render time.
type nagareFormTheme struct{}

func (nagareFormTheme) Theme(isDark bool) *huh.Styles {
	t := huh.ThemeBase(isDark)
	c := theme.Current().Colors

	t.Focused.Base = t.Focused.Base.BorderForeground(c.Primary)
	t.Focused.Title = lipgloss.NewStyle().Foreground(c.Primary).Bold(true)
	t.Focused.Description = lipgloss.NewStyle().Foreground(c.Muted)
	t.Focused.SelectSelector = lipgloss.NewStyle().Foreground(c.Accent).Bold(true)
	t.Focused.SelectedOption = lipgloss.NewStyle().Foreground(c.Accent).Bold(true)
	t.Focused.UnselectedOption = lipgloss.NewStyle().Foreground(c.Foreground)
	t.Focused.FocusedButton = lipgloss.NewStyle().
		Background(c.Primary).
		Foreground(c.Background).
		Padding(0, 2)
	t.Focused.BlurredButton = lipgloss.NewStyle().
		Foreground(c.Foreground).
		Padding(0, 2)
	t.Focused.TextInput.Prompt = lipgloss.NewStyle().Foreground(c.Accent)
	t.Focused.TextInput.Placeholder = lipgloss.NewStyle().Foreground(c.Muted)
	t.Focused.ErrorIndicator = lipgloss.NewStyle().Foreground(c.Error)
	t.Focused.ErrorMessage = lipgloss.NewStyle().Foreground(c.Error)

	t.Blurred.Title = lipgloss.NewStyle().Foreground(c.Muted)
	t.Blurred.Description = lipgloss.NewStyle().Foreground(c.Muted)

	t.Help.FullKey = lipgloss.NewStyle().Foreground(c.Muted)
	t.Help.FullDesc = lipgloss.NewStyle().Foreground(c.Muted)
	t.Help.ShortKey = lipgloss.NewStyle().Foreground(c.Muted)
	t.Help.ShortDesc = lipgloss.NewStyle().Foreground(c.Muted)

	return t
}

func formTheme() huh.Theme { return nagareFormTheme{} }

// Frame sizing shared by the two forms.
//
// Both render a huh form inside a bordered box and place it centred. Two things
// have to be told about the terminal for that to fit: huh, which otherwise lays
// out at its own default width of about 98 cells, and the assembled frame, since
// lipgloss.Place pads up to the given size but never truncates — so an oversized
// box silently scrolled the alt screen. The other three TUIs in nagare already
// clamp their frames; these two did not.

// boxChromeWidth is the border (2) and horizontal padding (2*2) around the form.
const boxChromeWidth = 6

// boxChromeHeight is the border (2), vertical padding (2), the title and the
// blank line under it.
const boxChromeHeight = 6

func formWidth(termWidth int) int {
	return max(termWidth-boxChromeWidth, 20)
}

func formHeight(termHeight int) int {
	return max(termHeight-boxChromeHeight, 6)
}

// clampFrame pins an assembled frame to the terminal, the same safety net the
// picker, notification centre and popup all use.
func clampFrame(content string, width, height int) string {
	if width <= 0 || height <= 0 {
		return content
	}
	return lipgloss.NewStyle().MaxWidth(width).MaxHeight(height).Render(content)
}
