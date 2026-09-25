package picker

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nemanja-micovic/nagare-go/internal/models"
)

// gridModel renders a grid of n sessions built by shape, with previews filled so
// cards are at their full content height.
func gridModel(t *testing.T, w, h int, sessions []models.Session) Model {
	t.Helper()
	m := driveModel(t, NewForTest(),
		tea.WindowSizeMsg{Width: w, Height: h},
		SessionsUpdatedMsg(sessions),
	)
	m.viewMode = GridView
	m.gridPreviews = map[string]string{}
	for i, s := range m.filtered {
		m.gridPreviews[sessionKey(s)] = strings.Repeat("a preview line of output\n", 30)
		// Seed activity history so the card tests cover the sparkline path. Without
		// it they missed a header sized against the card width but rendered into the
		// narrower column beside the agent art, which wrapped the header and made
		// the card a row too tall.
		trace := make([]uint8, 0, sparkSamples)
		for j := 0; j < sparkSamples; j++ {
			trace = append(trace, []uint8{1, 3, 4, 3}[(i+j)%4])
		}
		m.history[sessionKey(s)] = trace
	}
	return m
}

// runeAt returns the visible rune at a frame coordinate.
func runeAt(plain []string, x, y int) string {
	if y < 0 || y >= len(plain) {
		return "<row oob>"
	}
	r := []rune(plain[y])
	if x < 0 || x >= len(r) {
		return "<col oob>"
	}
	return string(r[x])
}

func plainFrame(frame string) []string {
	rows := strings.Split(frame, "\n")
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = ansi.Strip(r)
	}
	return out
}

// longSessions produces the shape that broke: a path and branch long enough that
// the card's meta line wraps to a second row.
func longSessions(n int) []models.Session {
	out := make([]models.Session, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, models.Session{
			Name:        fmt.Sprintf("cosmic-platform-frontend/claude_%02d", i),
			SessionName: "cosmic-platform-frontend",
			Path:        "/home/nemke/Projects/cosmic-platform-frontend",
			Status:      models.StatusIdle,
			AgentType:   models.AgentClaude,
			Details: models.SessionDetails{
				RepoName:  "cosmic-platform-frontend",
				Worktree:  "feat",
				GitBranch: "picker-depth-and-mouse",
			},
		})
	}
	return out
}

func shortSessions(n int) []models.Session {
	out := make([]models.Session, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, models.Session{
			Name:        fmt.Sprintf("s%d", i),
			SessionName: fmt.Sprintf("s%d", i),
			Path:        "/tmp/s",
			Status:      models.StatusIdle,
			AgentType:   models.AgentClaude,
		})
	}
	return out
}

// TestGridCardsAreClosedBoxes is the regression guard for the open-bottomed card.
//
// previewHeight used to be derived as cellHeight-7, which assumed the header block
// was exactly two rows. A long path plus a long branch wraps the meta line to a
// third row, making the card one row taller than its cell — and fitBox's MaxHeight
// then clipped that row, taking the bottom border with it. The card rendered with
// no bottom edge at all.
func TestGridCardsAreClosedBoxes(t *testing.T) {
	sizes := [][2]int{{207, 60}, {200, 50}, {160, 40}, {120, 30}, {90, 24}}
	shapes := map[string]func(int) []models.Session{
		"wrapping meta": longSessions,
		"short meta":    shortSessions,
	}

	for name, shape := range shapes {
		for _, count := range []int{1, 2, 4, 7, 9} {
			for _, sz := range sizes {
				w, h := sz[0], sz[1]
				t.Run(fmt.Sprintf("%s/%d sessions/%dx%d", name, count, w, h), func(t *testing.T) {
					m := gridModel(t, w, h, shape(count))
					frame, hits := m.view()
					plain := plainFrame(frame)

					if len(hits.cards) != count {
						t.Fatalf("got %d cards for %d sessions", len(hits.cards), count)
					}
					for _, card := range hits.cards {
						a := card.area
						corners := map[string]struct {
							x, y int
							want string
						}{
							"top-left":     {a.Min.X, a.Min.Y, "╭"},
							"top-right":    {a.Max.X - 1, a.Min.Y, "╮"},
							"bottom-left":  {a.Min.X, a.Max.Y - 1, "╰"},
							"bottom-right": {a.Max.X - 1, a.Max.Y - 1, "╯"},
						}
						for corner, c := range corners {
							if got := runeAt(plain, c.x, c.y); got != c.want {
								t.Errorf("card %d %s corner at (%d,%d) = %q, want %q",
									card.index, corner, c.x, c.y, got, c.want)
							}
						}
					}
				})
			}
		}
	}
}

// TestGridCardBottomEdgeIsContinuous — a clipped card can still show corners if
// the clip lands elsewhere, so check the whole bottom edge is border, not content.
func TestGridCardBottomEdgeIsContinuous(t *testing.T) {
	m := gridModel(t, 207, 60, longSessions(7))
	frame, hits := m.view()
	plain := plainFrame(frame)

	for _, card := range hits.cards {
		a := card.area
		y := a.Max.Y - 1
		for x := a.Min.X + 1; x < a.Max.X-1; x++ {
			if got := runeAt(plain, x, y); got != "─" {
				t.Fatalf("card %d bottom edge at (%d,%d) = %q, want a border rule",
					card.index, x, y, got)
				break
			}
		}
	}
}

// TestGridFrameIsExactlySized — the card height arithmetic feeds the frame's, so
// an off-by-one there smears the alt screen.
func TestGridFrameIsExactlySized(t *testing.T) {
	for _, sz := range [][2]int{{207, 60}, {160, 40}, {120, 30}, {90, 24}} {
		w, h := sz[0], sz[1]
		m := gridModel(t, w, h, longSessions(7))
		lines, width := frameSize(m.View().Content)
		if lines != h || width != w {
			t.Errorf("%dx%d: frame is %dx%d", w, h, width, lines)
		}
	}
}

// TestDetailPanelIsAClosedBox guards the third instance of the measure-don't-assume
// bug in this package.
//
// detailOuter was derived from strings.Count(content, "\n"), which counts string
// lines rather than rendered rows. A path too long for a narrow panel occupies two
// rows while counting as one, leaving the panel a row short — and fitBox then
// clipped its bottom border away. The wide layout was immune only by accident,
// because JoinHorizontal renders the info column at a fixed width first, so its
// wraps were already newlines by the time they were counted.
func TestDetailPanelIsAClosedBox(t *testing.T) {
	sessions := [][]models.Session{
		{{
			Name: "svc", SessionName: "svc",
			Path:      "/home/nemke/Projects/deeply/nested/monorepo/packages/service-gateway-internal",
			Status:    models.StatusIdle,
			AgentType: models.AgentClaude,
		}},
		{{
			Name: "cosmic-platform-frontend/claude_01", SessionName: "cosmic-platform-frontend",
			Path:   "/home/nemke/Projects/cosmic-platform-frontend",
			Status: models.StatusWaitingInput, AgentType: models.AgentClaude,
			Details: models.SessionDetails{
				RepoName: "cosmic-platform-frontend", Worktree: "feat/procosmic",
				GitBranch: "picker-depth-and-mouse", LastActivity: "2026-08-18T06:00:00Z",
			},
			LastMessage: "a fairly long last assistant message that has to wrap somewhere",
		}},
	}

	// Narrow widths matter most: the detail block is only left unwrapped below the
	// threshold where the agent art is dropped.
	for si, set := range sessions {
		for _, w := range []int{200, 120, 80, 60, 55, 50, 45, 40, 35} {
			for _, h := range []int{60, 40, 30, 20} {
				t.Run(fmt.Sprintf("set%d/%dx%d", si, w, h), func(t *testing.T) {
					m := driveModel(t, NewForTest(),
						tea.WindowSizeMsg{Width: w, Height: h},
						SessionsUpdatedMsg(set),
					)
					out := m.viewRight(w-w/5, h-2)

					var tops, bottoms int
					for _, row := range strings.Split(out, "\n") {
						switch pl := ansi.Strip(row); {
						case strings.HasPrefix(pl, "╭"):
							tops++
						case strings.HasPrefix(pl, "╰"):
							bottoms++
						}
					}
					// Two stacked boxes: the detail panel and the preview well.
					if tops != 2 || bottoms != 2 {
						t.Errorf("right column has %d box tops and %d bottoms, want 2 and 2",
							tops, bottoms)
					}
				})
			}
		}
	}
}

// TestFrameIsExactlyTerminalSized is what replaced the whole-frame MaxWidth clamp
// in View, which cost 18% of every frame because Render measures each line with
// full grapheme segmentation.
//
// The invariant it guards is that width is established where content is built —
// fitBox pins each panel, grid rows and the help bar render at an explicit width,
// placeOverlay clamps itself — rather than being patched up at the end. If a panel
// ever renders wider than its budget the terminal wraps it and the UI smears, so
// this covers both view modes across a spread of sizes and session counts.
func TestFrameIsExactlyTerminalSized(t *testing.T) {
	sizes := [][2]int{{207, 60}, {200, 50}, {160, 40}, {120, 30}, {90, 24}, {70, 20}, {60, 16}}
	for _, grid := range []bool{false, true} {
		for _, count := range []int{0, 1, 3, 9, 30} {
			for _, sz := range sizes {
				w, h := sz[0], sz[1]
				name := fmt.Sprintf("list/%d/%dx%d", count, w, h)
				if grid {
					name = fmt.Sprintf("grid/%d/%dx%d", count, w, h)
				}
				t.Run(name, func(t *testing.T) {
					m := gridModel(t, w, h, longSessions(count))
					if !grid {
						m.viewMode = ListView
					}
					frame := m.View().Content
					rows := strings.Split(frame, "\n")
					if len(rows) > h {
						t.Fatalf("frame has %d rows, more than the %d available", len(rows), h)
					}
					for i, row := range rows {
						if got := ansi.StringWidth(row); got != w {
							t.Errorf("row %d is %d cells wide, want exactly %d", i, got, w)
						}
					}
				})
			}
		}
	}
}

// TestGridCardsDoNotAnimate is the guard on a deliberate decision: nothing about a
// card moves or changes colour over time. A trail on the card being left behind, a
// flash on a card whose agent changed state, and a staggered arrival were all tried
// and all rejected — a card is a large object, and animating one pulls the eye to
// the card rather than to what is written on it.
//
// The status dot inside a card still breathes, so this compares frames with the
// breath phase held still. That is the only moving part left in grid view.
func TestGridCardsDoNotAnimate(t *testing.T) {
	m := gridModel(t, 200, 40, longSessions(6))
	m.animEnabled = true

	// Force on everything that would previously have animated a card. The slide is
	// started directly rather than through a key press: startSlide refuses in grid
	// view, so going through it would leave the render side untested and this test
	// would pass with a card trail re-added — which it did, the first time.
	m.cursor = 1
	m.slide.start(0)
	for _, s := range m.filtered {
		m.flashes[sessionKey(s)] = flashState{kind: flashAttention, level: 1}
	}

	first, _ := m.view()
	for frame := 1; frame <= 3*animFPS; frame++ {
		next, _ := m.Update(tickAnimMsg{})
		m = next.(Model)
		// Hold the breath still: it is allowed to move, and it is not a card.
		m.breath = 0

		got, _ := m.view()
		if got != first {
			t.Fatalf("grid frame changed at frame %d; cards must not animate", frame)
		}
	}
}
