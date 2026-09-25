package picker

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"

	"github.com/nemke/nagare-go/internal/theme"
)

// Agents write their messages in markdown — headings, lists, fenced code — so
// the mailbox renders it rather than showing the raw source. glamour does the
// rendering; this file only teaches it nagare's palette, so a message reads as
// part of the panel it sits in rather than as a pasted foreign document.

// markdownCache holds rendered markdown. The picker redraws up to ten times a
// second while anything is breathing, and a glamour render costs far more than
// a whole frame, so a message is rendered once per width and theme.
type markdownCache struct {
	rendered map[string]string
}

const markdownCacheMax = 256

func newMarkdownCache() *markdownCache {
	return &markdownCache{rendered: map[string]string{}}
}

// render returns src as styled text wrapped to width cells. The result always
// fits: glamour's wrap is a target rather than a guarantee (a long URL or code
// line runs past it), so lines are hard-cut to width afterwards.
func (c *markdownCache) render(src string, width int) string {
	if width < 1 {
		return ""
	}
	key := fmt.Sprintf("%s|%d|%s", theme.Current().Name, width, src)
	if out, ok := c.rendered[key]; ok {
		return out
	}
	out := renderMarkdown(src, width)
	// Resizing or switching themes strands every older entry, and the picker
	// runs for hours, so the cache is bounded; starting over costs one render
	// per visible message.
	if len(c.rendered) >= markdownCacheMax {
		clear(c.rendered)
	}
	c.rendered[key] = out
	return out
}

func renderMarkdown(src string, width int) string {
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(markdownStyle()),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return plainWrap(src, width)
	}
	out, err := r.Render(src)
	if err != nil {
		return plainWrap(src, width)
	}
	lines := strings.Split(strings.Trim(out, "\n"), "\n")
	for i, line := range lines {
		// glamour pads every line to the wrap width with trailing spaces; drop
		// them so the panel's own background fills the rest of the row.
		line = strings.TrimRight(line, " ")
		if lipgloss.Width(line) > width {
			line = truncate(line, width)
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

// plainWrap is the fallback when markdown cannot be rendered: the source,
// wrapped, so a message is never lost to a renderer error.
func plainWrap(src string, width int) string {
	return lipgloss.NewStyle().Width(width).Render(src)
}

// markdownStyle maps glamour's elements onto nagare's design tokens. It starts
// from glamour's own dark or light style for the structural choices — list
// indents, heading prefixes — and replaces every color, and strips the
// document margin, since the panel already has padding.
func markdownStyle() ansi.StyleConfig {
	c := theme.Current().Colors
	dark := isDark(c.Surface)
	s := styles.LightStyleConfig
	code := "github"
	if dark {
		s = styles.DarkStyleConfig
		code = "dracula"
	}

	fg, subtle, muted := colorHex(c.Foreground), colorHex(c.Subtle), colorHex(c.Muted)
	primary, secondary, accent := colorHex(c.Primary), colorHex(c.Secondary), colorHex(c.Accent)
	zero, codeIndent := uint(0), uint(2)
	yes, no := true, false

	s.Document = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: &fg}, Margin: &zero}
	s.Paragraph = ansi.StyleBlock{}
	s.Text = ansi.StylePrimitive{Color: &fg}
	s.Heading = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: &primary, Bold: &yes, BlockSuffix: "\n"}}
	// No background slab behind H1: every other heading is plain text on the
	// panel, and one filled block would outshout the message it titles.
	s.H1 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Prefix: "# ", Color: &primary, Bold: &yes}}
	s.H6 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Prefix: "###### ", Color: &subtle, Bold: &no}}
	s.BlockQuote = ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{Color: &subtle, Italic: &yes},
		Indent:         s.BlockQuote.Indent,
		IndentToken:    s.BlockQuote.IndentToken,
	}
	s.Strong = ansi.StylePrimitive{Bold: &yes, Color: &fg}
	s.Link = ansi.StylePrimitive{Color: &accent, Underline: &yes}
	s.LinkText = ansi.StylePrimitive{Color: &secondary, Bold: &yes}
	s.HorizontalRule = ansi.StylePrimitive{Color: &muted, Format: s.HorizontalRule.Format}
	s.Item = ansi.StylePrimitive{BlockPrefix: "• "}
	s.Enumeration = ansi.StylePrimitive{BlockPrefix: ". ", Color: &accent}
	s.Code = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{
		Prefix: s.Code.Prefix, Suffix: s.Code.Suffix, Color: &accent,
	}}
	s.CodeBlock = ansi.StyleCodeBlock{
		// Indented, so a code block reads as set apart from the prose around it
		// without a filled background fighting the panel's.
		StyleBlock: ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: &subtle}, Margin: &codeIndent},
		Theme:      code,
	}
	return s
}

// colorHex renders a color as "#rrggbb", the form glamour's styles take.
func colorHex(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

// isDark reports whether a surface is dark, by relative luminance.
func isDark(c color.Color) bool {
	if c == nil {
		return true
	}
	r, g, b, _ := c.RGBA()
	lum := (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)) / 0xffff
	return lum < 0.5
}
