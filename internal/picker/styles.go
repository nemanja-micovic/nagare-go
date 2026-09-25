package picker

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"

	"github.com/nemanja-micovic/nagare-go/internal/models"
	"github.com/nemanja-micovic/nagare-go/internal/theme"
)

const (
	// rowGutter is the blank column kept at the right edge of a list row or
	// grid cell, so nothing renders flush against the panel border.
	rowGutter = 1
	// minBranchWidth is the floor for the branch column in a list row. Below
	// this a branch is too clipped to identify, so the label yields instead.
	minBranchWidth = 8
	// minNameWidth is the floor for a name column. Below this a name is all
	// ellipsis and conveys nothing, so narrow panels overflow instead.
	minNameWidth = 6
	// minCellHeight is the shortest a grid card can be and still be a card:
	// border (2), padding (2), separator (1), one row of header and one of
	// preview. Anything less and its content cannot fit inside its own box.
	minCellHeight = 7
	// ellipsis marks a shortened name. One cell, unlike "..." — on a 30-column
	// panel those two extra cells are two more characters of the actual name.
	ellipsis = "…"
)

// fitBox pins a panel to exactly w×h cells, border and padding included.
//
// Both halves are needed. Width/Height only pad *up* to the target, so a
// wrapped path or an over-long preview line still pushes a panel past its
// budget; MaxWidth/MaxHeight clip it back. Getting this wrong is not cosmetic
// — a frame one row too tall scrolls the alt screen and smears the whole UI.
//
// Note these are lipgloss v2 semantics, where Width/Height are the TOTAL
// rendered size. In v1 they excluded the border, so panels were sized as
// Width(outer-2); carrying that over unchanged left every panel two cells
// short in each dimension.
func fitBox(s lipgloss.Style, w, h int) lipgloss.Style {
	return s.Width(w).Height(h).MaxWidth(w).MaxHeight(h)
}

// truncate shortens a display name to at most maxWidth terminal cells,
// appending an ellipsis when it had to cut. Width is measured in cells, not
// runes: CJK and emoji session names occupy two columns each, and counting
// runes would let them overflow the panel.
func truncate(name string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if lipgloss.Width(name) <= maxWidth {
		return name
	}
	return ansi.Truncate(name, maxWidth, ellipsis)
}

// statusDot renders the colored ● marker for a session's status. Running and
// waiting sessions breathe; idle, dead and saved ones hold still, because motion
// is the signal that something is happening.
func statusDot(status models.SessionStatus, phase float64) string {
	return statusDotOn(status, phase, nil)
}

// statusDotOn draws the dot over a known background, so a row's selection tint
// runs unbroken behind it and the breath fades toward the right colour. Passing
// nil leaves the background alone and fades toward the panel surface.
//
// The breath was a Faint toggle flipping once a second, which read as a blink
// rather than a pulse — two states, and the transition between them instant. It is
// now a colour walked toward the background and back on an eased curve. That is
// what a fade is in a terminal: there is no opacity to animate, so the only thing
// left to move is the colour itself.
func statusDotOn(status models.SessionStatus, phase float64, bg color.Color) string {
	fg := color.Color(lipgloss.Color(models.StatusColor(status)))

	if breathes(status) {
		toward := bg
		if toward == nil {
			toward = theme.Current().Colors.Surface
		}
		fg = theme.Mix(fg, toward, breathDepth*breathFactor(phase))
	}

	base := lipgloss.NewStyle().Foreground(fg)
	if bg != nil {
		base = base.Background(bg)
	}
	return base.Render("●")
}

// renderNameWithMatches renders a (possibly truncated) display string for a
// session, highlighting runes that match the current fuzzy query with the
// accent color only (no bold, no background — per television / atuin
// convention). The match runs against the *original* full name so
// highlights stay aligned even when the display text was shortened with
// an ellipsis.
//
// base is the style applied to unmatched runes. When query is empty the
// base style is applied unchanged — zero allocations in the common case.
func renderNameWithMatches(display, full, query string, base lipgloss.Style, accent color.Color) string {
	if query == "" {
		return base.Render(display)
	}
	matches := fuzzy.Find(query, []string{full})
	if len(matches) == 0 || len(matches[0].MatchedIndexes) == 0 {
		return base.Render(display)
	}

	displayRunes := []rune(display)
	// Display is a prefix of full (plus an ellipsis when truncated), so match
	// indexes line up rune-for-rune over the prefix. The ellipsis itself
	// stands in for the cut-off tail and must never be highlighted.
	limit := len(displayRunes)
	if strings.HasSuffix(display, ellipsis) {
		limit--
	}
	hit := make(map[int]struct{}, len(matches[0].MatchedIndexes))
	for _, idx := range matches[0].MatchedIndexes {
		if idx >= 0 && idx < limit {
			hit[idx] = struct{}{}
		}
	}
	if len(hit) == 0 {
		return base.Render(display)
	}

	// Render in runs rather than per rune. A fuzzy match is a handful of runs, not
	// a rune-length list of them, and Style.Render re-measures its input with full
	// grapheme segmentation every call — so a 30-character name cost 30 of those
	// instead of two or three. The cells produced are the same either way: the
	// style is identical across a run.
	matched := base.Foreground(accent)
	styleFor := func(on bool) lipgloss.Style {
		if on {
			return matched
		}
		return base
	}

	var b strings.Builder
	b.Grow(len(display) + 32)
	runStart := 0
	_, runOn := hit[0]
	for i := 1; i <= len(displayRunes); i++ {
		on := false
		if i < len(displayRunes) {
			_, on = hit[i]
		}
		if i == len(displayRunes) || on != runOn {
			b.WriteString(styleFor(runOn).Render(string(displayRunes[runStart:i])))
			runStart, runOn = i, on
		}
	}
	return b.String()
}

// sectionHeader renders an inline section title followed by a fill line
// (crush / gh-dash convention). No surrounding box — titled tiles compete
// inside an already-bordered panel.
//
//	Keyboard Shortcuts ───────────────────────
//
// The rule fades out along its length instead of running at one flat value.
// A uniform line reads as a divider that means something; a fading one reads
// as the title trailing off, which is what a section header actually is.
func sectionHeader(title string, width int) string {
	c := theme.Current().Colors
	titleStyled := lipgloss.NewStyle().Foreground(c.Primary).Bold(true).Render(title)
	fillWidth := width - lipgloss.Width(titleStyled) - 1
	if fillWidth < 1 {
		return titleStyled
	}
	return titleStyled + " " + fadingRule(fillWidth, c.Border, c.Overlay)
}

// fadingRule draws a horizontal rule of n cells that blends from `from` to
// `to`, so it dissolves into whatever it is drawn on.
//
// The colour changes every cell, so this writes the SGR sequences directly rather
// than calling Style.Render per cell. Render re-measures the string it is given
// with full grapheme segmentation, which for a one-cell string is pure overhead —
// and a rule can be 200 cells wide, several times per frame. It was most of the
// help screen's render cost on its own.
func fadingRule(n int, from, to color.Color) string {
	if n < 1 {
		return ""
	}
	stops := lipgloss.Blend1D(n, from, to)

	var b strings.Builder
	b.Grow(n * 24)
	for _, stop := range stops {
		b.WriteString(fgSeq(stop))
		b.WriteString("─")
	}
	// One reset at the end, so the rule does not tint what follows it. Each cell
	// still carries only its own foreground: the next cell's sequence replaces it.
	b.WriteString("\x1b[m")
	return b.String()
}

// Style functions — always build fresh from theme.Current() so theme
// switches take effect on the next View() call.

// The three depth planes, and the rule for choosing between them:
//
//	canvasBg   the ground a frame sits on — the help bar, the gaps between cards
//	surfaceBg  anything *inside* a panel, so the panel reads as one lifted slab
//	overlayBg  dialogs, which float above the panels
//
// Getting this wrong is visible: a fill left on canvasBg inside a panel punches
// a hole straight through it.

func canvasBg() color.Color {
	return theme.Current().Colors.Background
}

func surfaceBg() color.Color {
	return theme.Current().Colors.Surface
}

// onPlane re-asserts bg for every cell of content that would otherwise fall
// back to the terminal's own background.
//
// Two things leave such cells behind. A style that sets only a foreground ends
// its run with a full SGR reset, which clears the background for everything
// after it on that line — the reason the row and group-header renderers below
// carry their tint per segment instead of wrapping the finished string. And
// captured pane output is foreign ANSI: it resets whenever it likes and knows
// nothing about the panel it is being drawn into.
//
// Both were invisible for as long as every panel shared the terminal's
// background. They stopped being invisible the moment panels were lifted onto
// their own plane, because each gap became a hole punched straight through the
// panel with the terminal showing through it.
//
// Wrapping content in an outer Background style cannot fix this — that is the
// bug, not the cure. The background has to be re-established after each reset.
func onPlane(content string, bg color.Color) string {
	if content == "" {
		return content
	}
	set := bgSeq(bg)
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = set + reassertBg(line, set)
	}
	return strings.Join(lines, "\n")
}

// fgSeq is the SGR sequence that sets c as the foreground. Same rationale as
// bgSeq: used where a per-cell or per-run colour makes Style.Render too costly.
func fgSeq(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r>>8, g>>8, b>>8)
}

// bgSeq is the SGR sequence that sets bg as the background. Truecolor is
// emitted unconditionally, exactly as every lipgloss style in nagare already
// does; Bubble Tea's renderer downsamples for the terminal's actual profile.
func bgSeq(bg color.Color) string {
	r, g, b, _ := bg.RGBA()
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r>>8, g>>8, b>>8)
}

// reassertBg re-emits set after every SGR sequence in line that drops the
// background, so no printable cell is left on the terminal's default.
func reassertBg(line, set string) string {
	var b strings.Builder
	b.Grow(len(line) + len(set))

	for i := 0; i < len(line); {
		if seq, n := scanSGR(line[i:]); n > 0 {
			b.WriteString(seq)
			i += n
			// Nothing follows on this line, so there is no cell left to fix and
			// re-asserting would only cost bytes on every single line.
			if i < len(line) && clearsBackground(seq) {
				b.WriteString(set)
			}
			continue
		}
		b.WriteByte(line[i])
		i++
	}
	return b.String()
}

// scanSGR matches a leading SGR ("CSI ... m") sequence, returning it and its
// byte length. Only SGR is of interest: it is the only sequence that changes
// the background.
func scanSGR(s string) (string, int) {
	if !strings.HasPrefix(s, "\x1b[") {
		return "", 0
	}
	for i := 2; i < len(s); i++ {
		c := s[i]
		if c == 'm' {
			return s[:i+1], i + 1
		}
		// Parameter bytes only; anything else means this is some other
		// sequence (a cursor move, an OSC 8 hyperlink) that we leave alone.
		if (c < '0' || c > '9') && c != ';' && c != ':' {
			return "", 0
		}
	}
	return "", 0
}

// clearsBackground reports whether an SGR sequence leaves the background unset:
// either a full reset, or an explicit default-background (49). A sequence that
// sets any background — truecolor, 256-color, or one of the ANSI pairs — does
// not need fixing up.
// extendedColorParams returns how many fields after fields[i] belong to an
// extended color introducer (38, 48, 58), so the caller can skip past them.
func extendedColorParams(fields []string, i int) int {
	if i+1 >= len(fields) {
		return 0
	}
	switch fields[i+1] {
	case "5": // 5;N
		return 2
	case "2": // 2;R;G;B
		return 4
	}
	return 0
}

func clearsBackground(seq string) bool {
	params := seq[2 : len(seq)-1]
	if params == "" {
		return true // "ESC [ m" is a reset
	}

	cleared := false
	fields := strings.Split(params, ";")
	for i := 0; i < len(fields); i++ {
		switch f := fields[i]; f {
		case "0", "00":
			cleared = true
		case "49":
			cleared = true
		case "48":
			// Extended background: 48;5;N or 48;2;R;G;B. Either way a
			// background is being set, so skip its parameters.
			cleared = false
			i += extendedColorParams(fields, i)
		case "38", "58":
			// Extended foreground or underline color. Its parameters must be
			// skipped too, or a value of "0" among them reads as a reset —
			// which is exactly how 38;5;0 was mistaken for one.
			i += extendedColorParams(fields, i)
		default:
			// 40-47 and 100-107 set one of the ANSI backgrounds.
			if len(f) == 2 && f[0] == '4' && f[1] >= '0' && f[1] <= '7' {
				cleared = false
			}
			if len(f) == 3 && f[0] == '1' && f[1] == '0' && f[2] >= '0' && f[2] <= '7' {
				cleared = false
			}
		}
	}
	return cleared
}

func baseStyle() lipgloss.Style {
	c := theme.Current().Colors
	return lipgloss.NewStyle().
		Foreground(c.Foreground).
		Background(c.Surface)
}

// panelStyle is the secondary/detail panel (preview metadata, empty states,
// grid cards) — a lifted surface inside quiet chrome.
func panelStyle() lipgloss.Style {
	c := theme.Current().Colors
	return baseStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(c.Border).
		BorderBackground(c.Surface).
		Padding(1)
}

// primaryPanelStyle is the "focus-worthy" panel — the list where keystrokes
// land. Its border is a gradient sweep through the theme's two accent stops,
// so focus reads instantly and from the shape of the color rather than from
// any text. BorderForegroundBlend walks the blend around the perimeter, which
// is why the corners resolve to different colors than the mid-edges.
func primaryPanelStyle() lipgloss.Style {
	c := theme.Current().Colors
	return baseStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForegroundBlend(c.GradientFrom, c.GradientTo).
		BorderBackground(c.Surface).
		Padding(1)
}

// previewPanelStyle wraps captured pane output. It sits on the same surface as
// every other panel.
//
// Two other planes were tried here and both were wrong. A sunken Recessed plane
// was meant to sell "a window onto another terminal"; the canvas plane was meant
// to put foreign ANSI back on the ground the agent drew it against. Both made
// the preview a different color from the detail panel directly above it, and a
// panel that differs from its neighbour for reasons the eye cannot infer just
// reads as a bug. Panels are panels: one surface.
func previewPanelStyle() lipgloss.Style {
	c := theme.Current().Colors
	return lipgloss.NewStyle().
		Foreground(c.Foreground).
		Background(c.Surface).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(c.Border).
		BorderBackground(c.Surface).
		Padding(0, 1)
}

func titleStyle() lipgloss.Style {
	c := theme.Current().Colors
	return lipgloss.NewStyle().
		Foreground(c.Primary).
		Bold(true)
}

// subtleStyle is the middle rung of the emphasis ladder — supporting values
// that should read after the primary content but before the labels.
func subtleStyle() lipgloss.Style {
	c := theme.Current().Colors
	return lipgloss.NewStyle().
		Foreground(c.Subtle)
}

func mutedStyle() lipgloss.Style {
	c := theme.Current().Colors
	return lipgloss.NewStyle().
		Foreground(c.Muted)
}

// dialogStyle is the floating plane. Its border takes the gradient too, which
// is what visually ties a dialog to the focused panel it was summoned from.
func dialogStyle() lipgloss.Style {
	c := theme.Current().Colors
	return lipgloss.NewStyle().
		Background(c.Overlay).
		Foreground(c.Foreground).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForegroundBlend(c.GradientFrom, c.GradientTo).
		BorderBackground(c.Overlay)
}
