package picker

import (
	"fmt"
	"image/color"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nemanja-micovic/nagare-go/internal/models"
	"github.com/nemanja-micovic/nagare-go/internal/theme"
)

// bgSGR returns the SGR parameter fragment a truecolor background for c is
// rendered with, so a test can assert that a particular depth plane actually
// reached the frame. Colors are resolved through RGBA() rather than from a hex
// literal, so the assertion follows whichever half of an adaptive Pair the
// terminal resolved to.
func bgSGR(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("48;2;%d;%d;%d", r>>8, g>>8, b>>8)
}

// frameSize reports the line count and the width of the widest line, measured
// in terminal cells.
func frameSize(frame string) (lines, width int) {
	ls := strings.Split(frame, "\n")
	for _, l := range ls {
		if w := ansi.StringWidth(l); w > width {
			width = w
		}
	}
	return len(ls), width
}

// newVisualModel builds a picker with a representative session set, for tests
// that assert on a whole rendered frame.
func newVisualModel(t *testing.T, width, height int) Model {
	t.Helper()
	sessions := []models.Session{
		{Name: "nagare-go", SessionName: "nagare-go", Path: "/tmp/nagare-go",
			Status: models.StatusRunning, AgentType: models.AgentClaude,
			Details: models.SessionDetails{GitBranch: "main", RepoName: "nagare-go"}},
		{Name: "cosmo/feat-x", SessionName: "cosmo", Path: "/tmp/cosmo/.worktrees/feat-x",
			Status: models.StatusWaitingInput, AgentType: models.AgentOpenCode,
			Details: models.SessionDetails{GitBranch: "feat-x", RepoName: "cosmo", Worktree: "feat-x"}},
	}
	return driveModel(t, NewForTest(),
		tea.WindowSizeMsg{Width: width, Height: height},
		SessionsUpdatedMsg(sessions),
	)
}

func testBackdrop(width, height int) string {
	return lipgloss.NewStyle().
		Background(theme.Current().Colors.Surface).
		Width(width).
		Height(height).
		Render("backdrop")
}

func testDialog() string {
	return dialogStyle().Padding(1, 2).Render("Dialog\ncontent")
}

// TestOverlayIsExactlyFrameSized is the invariant the whole compositor rewrite
// exists to hold. A frame even one row too tall scrolls the alt screen and
// smears the UI, and one column too wide wraps every row.
func TestOverlayIsExactlyFrameSized(t *testing.T) {
	const w, h = 80, 24
	out := placeOverlay(w, h, testDialog(), testBackdrop(w, h), 0)

	lines, width := frameSize(out)
	if lines != h {
		t.Errorf("frame has %d lines, want exactly %d", lines, h)
	}
	if width != w {
		t.Errorf("frame is %d cells wide, want exactly %d", width, w)
	}
}

// TestOverlayCastsShadow — the shadow is the point of using layers rather than
// splicing strings, so its absence should fail loudly rather than silently
// flattening the dialog back onto the backdrop.
func TestOverlayCastsShadow(t *testing.T) {
	const w, h = 80, 24
	out := placeOverlay(w, h, testDialog(), testBackdrop(w, h), 0)

	shadow := bgSGR(theme.Current().Colors.Shadow)
	if !strings.Contains(out, shadow) {
		t.Errorf("frame carries no shadow-colored cells (looked for %q)", shadow)
	}
}

// TestOverlayShadowClampedAtEdges — a dialog bigger than the frame is a real
// case (the help screen on a short terminal). The shadow must clamp to the room
// left rather than pushing the composite past its bounds.
func TestOverlayShadowClampedAtEdges(t *testing.T) {
	const w, h = 40, 12
	// Deliberately larger than the frame in both dimensions.
	oversized := dialogStyle().Width(w + 10).Height(h + 6).Render("too big")

	out := placeOverlay(w, h, oversized, testBackdrop(w, h), 0)

	lines, width := frameSize(out)
	if lines != h {
		t.Errorf("oversized dialog produced %d lines, want %d", lines, h)
	}
	if width != w {
		t.Errorf("oversized dialog produced %d cells of width, want %d", width, w)
	}
}

// TestOverlayZeroSizeIsSafe — View() renders before the first WindowSizeMsg
// arrives, and a zero-size compositor must not panic or invent a frame.
func TestOverlayZeroSizeIsSafe(t *testing.T) {
	bg := "backdrop"
	if got := placeOverlay(0, 0, testDialog(), bg, 0); got != bg {
		t.Errorf("zero-size overlay = %q, want the backdrop unchanged", got)
	}
}

// TestDepthPlanesReachTheFrame guards the depth ladder end to end: the four
// planes have to be visibly different backgrounds in a real rendered frame, not
// merely different values in the palette. Collapsing any two of them is exactly
// the regression that makes nagare look flat again.
func TestDepthPlanesReachTheFrame(t *testing.T) {
	m := newVisualModel(t, 130, 40)
	frame := m.View().Content
	c := theme.Current().Colors

	planes := []struct {
		name string
		col  color.Color
	}{
		{"canvas (help bar, preview well)", c.Background},
		{"surface (panels)", c.Surface},
	}
	for _, p := range planes {
		if sgr := bgSGR(p.col); !strings.Contains(frame, sgr) {
			t.Errorf("plane %s missing from frame (looked for %q)", p.name, sgr)
		}
	}

	// The overlay plane only appears when a dialog is open.
	m.showThemePick = true
	m.themeNames = theme.Names()
	withDialog := m.View().Content
	if sgr := bgSGR(c.Overlay); !strings.Contains(withDialog, sgr) {
		t.Errorf("plane overlay (dialogs) missing from frame (looked for %q)", sgr)
	}
}

// TestPlanesAreMutuallyDistinct — derivation could in principle round two tiers
// onto the same value on an unusual palette, which would defeat the ladder
// without failing any per-plane check.
func TestPlanesAreMutuallyDistinct(t *testing.T) {
	for _, name := range theme.Names() {
		th := theme.Get(name)
		c := th.Colors
		seen := map[string]string{}
		for label, col := range map[string]color.Color{
			"Background": c.Background, "Surface": c.Surface, "Overlay": c.Overlay,
		} {
			sgr := bgSGR(col)
			if prev, dup := seen[sgr]; dup {
				t.Errorf("theme %q: planes %s and %s resolve to the same color %s",
					name, prev, label, sgr)
			}
			seen[sgr] = label
		}
	}
}

// TestFadingRuleFadesAndFits — the rule is drawn per cell, so an off-by-one in
// the blend loop would silently change every section header's width.
func TestFadingRuleFadesAndFits(t *testing.T) {
	c := theme.Current().Colors
	const n = 20
	rule := fadingRule(n, c.Border, c.Overlay)

	if got := ansi.StringWidth(rule); got != n {
		t.Errorf("fadingRule(%d) is %d cells wide, want %d", n, got, n)
	}
	// A fade means more than one distinct color along its length.
	if strings.Count(rule, "38;2;") < 2 {
		t.Error("fadingRule emitted a single flat color; it should blend")
	}
	if got := fadingRule(0, c.Border, c.Overlay); got != "" {
		t.Errorf("fadingRule(0) = %q, want empty", got)
	}
}

// stripOSC removes OSC sequences (notably OSC 8 hyperlinks) so a cell walker
// does not count their payload as printable content.
func stripOSC(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], "\x1b]") {
			if j := strings.Index(s[i:], "\x07"); j >= 0 {
				i += j + 1
				continue
			}
			if j := strings.Index(s[i:], "\x1b\\"); j >= 0 {
				i += j + 2
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

var testSGR = regexp.MustCompile("^\x1b\\[[0-9;:]*m")

// cellBackgrounds walks a rendered line and returns the background in effect
// for each printable cell. "default" means none was set, so the terminal's own
// background shows through.
func cellBackgrounds(line string) []string {
	line = stripOSC(line)
	cur := "default"
	var out []string
	for i := 0; i < len(line); {
		if seq := testSGR.FindString(line[i:]); seq != "" {
			params := seq[2 : len(seq)-1]
			if clearsBackground(seq) {
				cur = "default"
			} else if j := strings.Index(params, "48;2;"); j >= 0 {
				parts := strings.Split(params[j:], ";")
				if len(parts) >= 5 {
					cur = strings.Join(parts[:5], ";")
				}
			}
			i += len(seq)
			continue
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		if r != utf8.RuneError || size > 1 {
			out = append(out, cur)
		}
		i += size
	}
	return out
}

// TestNoDefaultBackgroundCells is the regression guard for the holes that
// lifting panels onto their own plane exposed.
//
// Any cell left on the terminal's default background shows the terminal
// *through* the frame. It went unnoticed for as long as every panel shared the
// terminal's background, so this asserts on cells rather than on the palette:
// the palette was never the thing that was wrong.
func TestNoDefaultBackgroundCells(t *testing.T) {
	// Captured pane output as tmux really hands it over: styled runs ending in
	// full resets, which is what nagare's own styles cannot control.
	preview := strings.Join([]string{
		"\x1b[1;38;2;255;255;255mReport what you changed\x1b[0m and what you found.",
		"\x1b[48;2;60;60;70m recap: \x1b[0m improving the issues UI",
		"a plain line carrying no ansi at all",
		"\x1b[38;2;150;150;150mRan\x1b[m 2 shell commands",
	}, "\n")

	cases := []struct {
		name  string
		setup func(Model) Model
	}{
		{"list view", func(m Model) Model { return m }},
		{"grid view", func(m Model) Model { m.viewMode = GridView; return m }},
		{"help overlay", func(m Model) Model { m.showHelp = true; return m }},
		{"theme picker", func(m Model) Model {
			m.showThemePick = true
			m.themeNames = theme.Names()
			return m
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newVisualModel(t, 120, 30)
			m, _ = driveModel(t, m, PreviewUpdatedMsg(preview)), 0
			m = tc.setup(m)

			for row, line := range strings.Split(m.View().Content, "\n") {
				for col, bg := range cellBackgrounds(line) {
					if bg == "default" {
						t.Fatalf("row %d col %d sits on the terminal's default background; "+
							"content reaching a panel must be wrapped with onPlane", row, col)
					}
				}
			}
		})
	}
}

// TestOnPlanePreservesForeignBackgrounds — re-asserting the plane must not
// flatten the colors captured pane output brought with it. A preview that loses
// the agent's own highlighting is worse than one with holes in it.
func TestOnPlanePreservesForeignBackgrounds(t *testing.T) {
	const foreign = "48;2;60;60;70"
	in := "\x1b[" + foreign + "m recap: \x1b[0m tail text"

	out := onPlane(in, theme.Current().Colors.Background)

	bgs := cellBackgrounds(out)
	var sawForeign, sawPlane bool
	plane := bgSGR(theme.Current().Colors.Background)
	for _, bg := range bgs {
		switch bg {
		case foreign:
			sawForeign = true
		case plane:
			sawPlane = true
		case "default":
			t.Fatal("onPlane left a default-background cell")
		}
	}
	if !sawForeign {
		t.Error("onPlane overwrote the foreign background it should have kept")
	}
	if !sawPlane {
		t.Error("onPlane did not apply the plane background after the reset")
	}
}

// TestClearsBackground covers the SGR parsing directly, including the extended
// forms whose parameters must not be mistaken for a reset.
func TestClearsBackground(t *testing.T) {
	cases := map[string]bool{
		"\x1b[m":              true,  // bare reset
		"\x1b[0m":             true,  // explicit reset
		"\x1b[49m":            true,  // default background
		"\x1b[1;38;2;1;2;3m":  false, // foreground only — no bg to clear...
		"\x1b[48;2;10;20;30m": false, // truecolor bg
		"\x1b[48;5;236m":      false, // 256-color bg
		"\x1b[41m":            false, // ANSI bg
		"\x1b[103m":           false, // bright ANSI bg
		"\x1b[0;48;2;1;2;3m":  false, // reset then set a bg: ends up set
		"\x1b[48;2;1;2;3;49m": true,  // set then unset: ends up cleared
		"\x1b[38;5;0m":        false, // 256-color *foreground*, params must not read as a reset
	}
	for seq, want := range cases {
		if got := clearsBackground(seq); got != want {
			t.Errorf("clearsBackground(%q) = %v, want %v", seq, got, want)
		}
	}
}
