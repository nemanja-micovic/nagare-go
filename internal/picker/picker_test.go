package picker

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nemanja-micovic/nagare-go/internal/models"
)

func fakeSessions() []models.Session {
	return []models.Session{
		{Name: "alpha", SessionName: "alpha", Path: "/tmp/alpha", Status: models.StatusIdle, AgentType: models.AgentClaude},
		{Name: "beta", SessionName: "beta", Path: "/tmp/beta", Status: models.StatusRunning, AgentType: models.AgentClaude},
		{Name: "gamma", SessionName: "gamma", Path: "/tmp/gamma", Status: models.StatusWaitingInput, AgentType: models.AgentOpenCode},
	}
}

// driveModel advances a Model through a sequence of messages and returns the
// resulting Model. It ignores returned tea.Cmd values — tests that need side
// effects should call Update directly.
func driveModel(t *testing.T, m Model, msgs ...tea.Msg) Model {
	t.Helper()
	for _, msg := range msgs {
		next, _ := m.Update(msg)
		var ok bool
		m, ok = next.(Model)
		if !ok {
			t.Fatalf("Update returned unexpected type %T", next)
		}
	}
	return m
}

// typeString drives the model as if the user typed each rune in order.
func typeString(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		m = driveModel(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

func filteredNames(m Model) []string {
	out := make([]string, 0, len(m.filtered))
	for _, s := range m.filtered {
		out = append(out, s.Name)
	}
	return out
}

// TestSessionsUpdatedPopulatesFiltered verifies SessionsUpdatedMsg seeds both
// sessions and the filtered view the UI renders from.
func TestSessionsUpdatedPopulatesFiltered(t *testing.T) {
	m := driveModel(t, NewForTest(),
		tea.WindowSizeMsg{Width: 120, Height: 40},
		SessionsUpdatedMsg(fakeSessions()),
	)

	got := filteredNames(m)
	want := []string{"alpha", "beta", "gamma"}
	if len(got) != len(want) {
		t.Fatalf("filtered length = %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for _, name := range want {
		if !contains(got, name) {
			t.Errorf("filtered missing %q (have %v)", name, got)
		}
	}
}

// TestFuzzySearchNarrows verifies typing into the search input filters the
// session list by fuzzy match.
func TestFuzzySearchNarrows(t *testing.T) {
	m := driveModel(t, NewForTest(),
		tea.WindowSizeMsg{Width: 120, Height: 40},
		SessionsUpdatedMsg(fakeSessions()),
	)

	m = typeString(t, m, "gam")

	got := filteredNames(m)
	if len(got) != 1 || got[0] != "gamma" {
		t.Errorf("after typing 'gam', filtered = %v; want [gamma]", got)
	}
}

// TestViewRendersSessionNames verifies the rendered frame includes every
// visible session's display name.
func TestViewRendersSessionNames(t *testing.T) {
	m := driveModel(t, NewForTest(),
		tea.WindowSizeMsg{Width: 160, Height: 40},
		SessionsUpdatedMsg(fakeSessions()),
	)

	view := m.View().Content
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(view, name) {
			t.Errorf("view missing %q", name)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		maxWidth int
		want     string
	}{
		{"fits exactly", "abcdef", 6, "abcdef"},
		{"room to spare", "abc", 10, "abc"},
		{"one over", "abcdefg", 6, "abcde" + ellipsis},
		{"zero width", "abc", 0, ""},
		// Each CJK rune is two cells, so five runes are ten cells: counting
		// runes instead of cells would have let this through untouched.
		{"wide runes measured in cells", "日本語です", 6, "日本" + ellipsis},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := truncate(tc.in, tc.maxWidth)
			if got != tc.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tc.in, tc.maxWidth, got, tc.want)
			}
			if w := lipgloss.Width(got); w > tc.maxWidth {
				t.Errorf("truncate(%q, %d) = %q which is %d cells, over budget", tc.in, tc.maxWidth, got, w)
			}
		})
	}
}

// TestListRowsFitWidthExactly guards the row layout arithmetic: every rendered
// row must be exactly the requested width. Too wide and it wraps or bleeds past
// the panel border; too narrow and the selection background stops short.
func TestListRowsFitWidthExactly(t *testing.T) {
	m := driveModel(t, NewForTest(),
		tea.WindowSizeMsg{Width: 120, Height: 40},
		SessionsUpdatedMsg([]models.Session{
			{Name: "short", SessionName: "short", Status: models.StatusIdle, AgentType: models.AgentClaude},
			{Name: "cosmic-platform-frontend", SessionName: "cpf", Status: models.StatusRunning, AgentType: models.AgentOpenCode},
			{Name: "a-really-long-session-name-that-cannot-possibly-fit", SessionName: "long", Status: models.StatusWaitingInput, AgentType: models.AgentGemini},
		}),
	)

	for _, width := range []int{20, 30, 41, 60, 100} {
		rendered, _ := m.renderListView(width, 10)
		for i, line := range strings.Split(rendered, "\n") {
			if got := lipgloss.Width(line); got != width {
				t.Errorf("width=%d: row %d rendered %d cells, want %d (%q)", width, i, got, width, ansi.Strip(line))
			}
		}
	}
}

// TestLongNameNotClippedWhenItFits is the regression test for the reported bug:
// the old budget reserved a flat 20 columns regardless of the actual agent
// badge, so a name with room to spare was still cut short with an ellipsis.
func TestLongNameNotClippedWhenItFits(t *testing.T) {
	const name = "cosmic-platform-frontend" // 24 cells
	m := driveModel(t, NewForTest(),
		tea.WindowSizeMsg{Width: 120, Height: 40},
		SessionsUpdatedMsg([]models.Session{
			{Name: name, SessionName: "cpf", Status: models.StatusIdle, AgentType: models.AgentClaude},
		}),
	)

	// 41 cells is the panel width from the reported screenshot. Overhead is
	// the dot, three spaces, the 8-cell "Claude" badge and the gutter — 13 —
	// leaving 28 for a 24-cell name.
	listOut, _ := m.renderListView(41, 10)
	rendered := ansi.Strip(listOut)
	if !strings.Contains(rendered, name) {
		t.Errorf("name %q was clipped at width 41; rendered %q", name, rendered)
	}
	if strings.Contains(rendered, ellipsis) {
		t.Errorf("name %q fits but was still ellipsized; rendered %q", name, rendered)
	}
}

// TestStatsCountVisibleSessions verifies the header agrees with the rows below
// it — saved sessions are hidden by default, so they must not inflate the
// headline count.
func TestStatsCountVisibleSessions(t *testing.T) {
	sessions := []models.Session{
		{Name: "live-1", SessionName: "live-1", Status: models.StatusIdle, AgentType: models.AgentClaude},
		{Name: "live-2", SessionName: "live-2", Status: models.StatusRunning, AgentType: models.AgentClaude},
		{Name: "saved-1", SessionName: "saved-1", Status: models.StatusSaved, AgentType: models.AgentClaude},
		{Name: "saved-2", SessionName: "saved-2", Status: models.StatusSaved, AgentType: models.AgentClaude},
	}
	m := driveModel(t, NewForTest(),
		tea.WindowSizeMsg{Width: 120, Height: 40},
		SessionsUpdatedMsg(sessions),
	)

	if len(m.filtered) != 2 {
		t.Fatalf("filtered = %v, want the 2 non-saved sessions", filteredNames(m))
	}
	stats := ansi.Strip(m.renderStats(2, 0, 1, 2))
	if !strings.Contains(stats, "2 sessions") {
		t.Errorf("stats = %q, want headline count to match the 2 visible rows", stats)
	}
	if !strings.Contains(stats, "2 saved") {
		t.Errorf("stats = %q, want hidden saved sessions reported separately", stats)
	}
	if strings.Contains(stats, "0 waiting") {
		t.Errorf("stats = %q, want zero counts omitted", stats)
	}
}

// TestFrameFillsTerminalExactly is the regression test for the v1→v2 lipgloss
// sizing change. In v1 Width/Height excluded the border, so panels were built
// as Width(outer-2); in v2 they include it, and carrying the -2 over left every
// panel two cells short. JoinHorizontal then padded the shorter column with
// *unstyled* spaces, so the terminal backdrop showed through at the bottom left.
//
// The frame must be exactly as tall as the terminal and never wider: one row
// too many scrolls the alt screen and smears the entire UI.
func TestFrameFillsTerminalExactly(t *testing.T) {
	sessions := []models.Session{
		{Name: "nagare", SessionName: "nagare", Path: "/home/nemke/HobbyProjects/nagare-go", Status: models.StatusIdle, AgentType: models.AgentClaude},
		{Name: "cosmic-platform-frontend", SessionName: "cpf", Path: "/home/nemke/Projects/cosmic-platform-frontend", Status: models.StatusWaitingInput, AgentType: models.AgentClaude},
		{Name: "cosmic-platform-backend", SessionName: "cpb", Path: "/home/nemke/Projects/cosmic-platform-backend", Status: models.StatusRunning, AgentType: models.AgentOpenCode},
	}
	// A pane captured from a terminal wider than the preview panel, with tabs
	// and trailing blank lines — what `capture-pane -e -p` actually returns.
	preview := strings.Join([]string{
		strings.Repeat("x", 190),
		"col1\tcol2\tcol3\t" + strings.Repeat("y", 120),
		"short line",
		strings.Repeat("z", 240),
		"", "",
	}, "\n")

	sizes := []struct{ w, h int }{
		{200, 50}, // the reported terminal
		{120, 40},
		{80, 24}, // classic default
		{60, 20}, // narrow: help bar soft-wraps past one line
		{40, 12}, // cramped
	}

	modes := []struct {
		name  string
		setup func(Model) Model
	}{
		{"list", func(m Model) Model { return m }},
		{"list+helpbar", func(m Model) Model { m.showHelpBar = true; return m }},
		{"list-no-helpbar", func(m Model) Model { m.showHelpBar = false; return m }},
		{"grid", func(m Model) Model { m.viewMode = GridView; return m }},
		{"help-overlay", func(m Model) Model { m.showHelp = true; return m }},
		{"theme-overlay", func(m Model) Model { m.showThemePick = true; return m }},
		{"prompt-overlay", func(m Model) Model { m.promptMode = true; return m }},
	}

	for _, size := range sizes {
		for _, mode := range modes {
			t.Run(fmt.Sprintf("%dx%d/%s", size.w, size.h, mode.name), func(t *testing.T) {
				m := driveModel(t, NewForTest(),
					tea.WindowSizeMsg{Width: size.w, Height: size.h},
					SessionsUpdatedMsg(sessions),
					PreviewUpdatedMsg(preview),
				)
				m = mode.setup(m)

				lines := strings.Split(m.View().Content, "\n")
				if len(lines) != size.h {
					t.Errorf("frame is %d rows, want exactly %d", len(lines), size.h)
				}
				for i, line := range lines {
					if w := ansi.StringWidth(line); w > size.w {
						t.Errorf("row %d is %d cells, want at most %d", i, w, size.w)
						break
					}
				}
			})
		}
	}
}

// TestEmptySessionListStillFillsFrame covers the no-sessions branches, which
// return their own panels and so have their own sizing arithmetic.
func TestEmptySessionListStillFillsFrame(t *testing.T) {
	for _, mode := range []ViewMode{ListView, GridView} {
		m := driveModel(t, NewForTest(),
			tea.WindowSizeMsg{Width: 100, Height: 30},
			SessionsUpdatedMsg(nil),
		)
		m.viewMode = mode
		if got := len(strings.Split(m.View().Content, "\n")); got != 30 {
			t.Errorf("viewMode %v: frame is %d rows, want 30", mode, got)
		}
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
