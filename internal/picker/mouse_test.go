package picker

import (
	"image"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nemanja-micovic/nagare-go/internal/models"
	"github.com/nemanja-micovic/nagare-go/internal/theme"
)

func click(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func wheel(x, y int, button tea.MouseButton) tea.MouseWheelMsg {
	return tea.MouseWheelMsg{X: x, Y: y, Button: button}
}

// mouseSessions is a set with two groups, so the row list contains group
// headers interleaved with sessions — the case a naive "row N is session N"
// mapping gets wrong.
func mouseSessions() []models.Session {
	return []models.Session{
		{Name: "alpha", SessionName: "alpha", Path: "/tmp/alpha",
			Status: models.StatusIdle, AgentType: models.AgentClaude},
		{Name: "repo/one", SessionName: "repo", Path: "/tmp/repo/one",
			Status: models.StatusRunning, AgentType: models.AgentClaude,
			Details: models.SessionDetails{RepoName: "repo", Worktree: "one"}},
		{Name: "repo/two", SessionName: "repo", Path: "/tmp/repo/two",
			Status: models.StatusWaitingInput, AgentType: models.AgentClaude,
			Details: models.SessionDetails{RepoName: "repo", Worktree: "two"}},
		{Name: "zeta", SessionName: "zeta", Path: "/tmp/zeta",
			Status: models.StatusIdle, AgentType: models.AgentPi},
	}
}

func mouseModel(t *testing.T, w, h int) Model {
	t.Helper()
	m := driveModel(t, NewForTest(),
		tea.WindowSizeMsg{Width: w, Height: h},
		SessionsUpdatedMsg(mouseSessions()),
	)
	m.mouseEnabled = true
	return m
}

// TestHitMapMatchesRenderedRows is the test that matters for mouse support: the
// recorded row for each session has to be the row its name was actually drawn
// on. Group headers and the wrapping stats line both shift the list down, so an
// arithmetic guess drifts; this checks the map against the pixels.
func TestHitMapMatchesRenderedRows(t *testing.T) {
	m := mouseModel(t, 120, 30)
	frame, hits := m.view()
	lines := strings.Split(frame, "\n")

	if len(hits.sessionAt) == 0 {
		t.Fatal("no rows were recorded as clickable")
	}

	labels := make(map[int]string)
	for _, listRow := range buildRows(m.filtered) {
		if listRow.SessionIdx >= 0 {
			labels[listRow.SessionIdx] = listRow.Label
		}
	}
	for row, idx := range hits.sessionAt {
		if row < 0 || row >= len(lines) {
			t.Errorf("row %d recorded for session %d is outside the frame", row, idx)
			continue
		}
		want := m.filtered[idx]
		leaf := labels[idx]
		got := ansi.Strip(lines[row])
		if !strings.Contains(got, leaf) {
			t.Errorf("row %d maps to session %q but renders %q", row, want.Name, strings.TrimSpace(got))
		}
	}

	// And every visible session should be clickable, not just some.
	seen := map[int]bool{}
	for _, idx := range hits.sessionAt {
		seen[idx] = true
	}
	for i := range m.filtered {
		if !seen[i] {
			t.Errorf("session %d (%s) has no clickable row", i, m.filtered[i].Name)
		}
	}
}

// TestClickSelectsThenActivates — a single click must not abandon the picker.
// One stray click while reaching for a row would otherwise jump away.
func TestClickSelectsThenActivates(t *testing.T) {
	m := mouseModel(t, 120, 30)
	_, hits := m.view()

	// Find a row belonging to something other than the current selection.
	target, row := -1, -1
	for r, idx := range hits.sessionAt {
		if idx != m.cursor {
			target, row = idx, r
			break
		}
	}
	if target < 0 {
		t.Fatal("need a session other than the selected one")
	}

	got := hits.resolve(click(1, row), m.cursor)
	sel, ok := got.(mouseSelectMsg)
	if !ok {
		t.Fatalf("first click on an unselected row = %T, want mouseSelectMsg", got)
	}
	if sel.index != target {
		t.Errorf("selected %d, want %d", sel.index, target)
	}

	// Clicking the row again, now that it is selected, activates it.
	got = hits.resolve(click(1, row), target)
	if act, ok := got.(mouseActivateMsg); !ok {
		t.Fatalf("second click = %T, want mouseActivateMsg", got)
	} else if act.index != target {
		t.Errorf("activated %d, want %d", act.index, target)
	}
}

// TestClicksOutsideTheListAreIgnored — the detail and preview panels are not
// targets, and neither is empty space below the last row.
func TestClicksOutsideTheListAreIgnored(t *testing.T) {
	m := mouseModel(t, 120, 30)
	_, hits := m.view()

	row := -1
	for r := range hits.sessionAt {
		row = r
		break
	}

	// Same row, but over on the right-hand panels.
	if got := hits.resolve(click(hits.listWidth+5, row), m.cursor); got != nil {
		t.Errorf("click on the detail panel produced %T, want nil", got)
	}
	// A row inside the list panel that holds no session.
	empty := 0
	for y := 0; y < 30; y++ {
		if _, ok := hits.sessionAt[y]; !ok {
			empty = y
			break
		}
	}
	if got := hits.resolve(click(1, empty), m.cursor); got != nil {
		t.Errorf("click on an empty list row produced %T, want nil", got)
	}
}

// TestWheelMovesCursor covers the two wheel directions and nothing else — a
// middle-click or a horizontal wheel must not move the selection.
func TestWheelMovesCursor(t *testing.T) {
	var hits hitTargets

	if got := hits.resolve(wheel(0, 0, tea.MouseWheelUp), 0); got != (mouseScrollMsg{delta: -1}) {
		t.Errorf("wheel up = %v, want delta -1", got)
	}
	if got := hits.resolve(wheel(0, 0, tea.MouseWheelDown), 0); got != (mouseScrollMsg{delta: 1}) {
		t.Errorf("wheel down = %v, want delta 1", got)
	}
	if got := hits.resolve(wheel(0, 0, tea.MouseWheelLeft), 0); got != nil {
		t.Errorf("horizontal wheel = %v, want nil", got)
	}
	if got := hits.resolve(tea.MouseClickMsg{Button: tea.MouseRight}, 0); got != nil {
		t.Errorf("right click = %v, want nil", got)
	}
}

// TestScrollClampsAtBothEnds — a fast scroll must not walk the cursor out of
// range, which would panic the next render.
func TestScrollClampsAtBothEnds(t *testing.T) {
	m := mouseModel(t, 120, 30)

	for i := 0; i < 50; i++ {
		m = driveModel(t, m, mouseScrollMsg{delta: 1})
	}
	if m.cursor != len(m.filtered)-1 {
		t.Errorf("cursor after scrolling past the end = %d, want %d", m.cursor, len(m.filtered)-1)
	}
	for i := 0; i < 50; i++ {
		m = driveModel(t, m, mouseScrollMsg{delta: -1})
	}
	if m.cursor != 0 {
		t.Errorf("cursor after scrolling past the start = %d, want 0", m.cursor)
	}
}

// TestGridCardsAreClickable checks the card rectangles cover the frame where
// the cards were drawn, and that each card maps to its own session.
func TestGridCardsAreClickable(t *testing.T) {
	m := mouseModel(t, 120, 30)
	m.viewMode = GridView
	_, hits := m.view()

	if len(hits.cards) != len(m.filtered) {
		t.Fatalf("recorded %d cards for %d sessions", len(hits.cards), len(m.filtered))
	}
	for _, card := range hits.cards {
		if card.area.Empty() {
			t.Errorf("card for session %d has an empty area", card.index)
		}
		// A click in the middle of the card resolves to that card.
		mid := image.Pt(
			(card.area.Min.X+card.area.Max.X)/2,
			(card.area.Min.Y+card.area.Max.Y)/2,
		)
		got := hits.resolve(click(mid.X, mid.Y), -1)
		sel, ok := got.(mouseSelectMsg)
		if !ok {
			t.Errorf("click inside card %d = %T, want mouseSelectMsg", card.index, got)
			continue
		}
		if sel.index != card.index {
			t.Errorf("click inside card %d selected %d", card.index, sel.index)
		}
	}
	// Cards must not overlap, or a click would be ambiguous.
	for i, a := range hits.cards {
		for _, b := range hits.cards[i+1:] {
			if !a.area.Intersect(b.area).Empty() {
				t.Errorf("cards %d and %d overlap", a.index, b.index)
			}
		}
	}
}

// TestOverlayOwnsTheMouse — while a dialog is open, a click must not reach the
// list behind it, and a click outside a dismissable one closes it.
func TestOverlayOwnsTheMouse(t *testing.T) {
	base := mouseModel(t, 120, 30)

	t.Run("dismissable overlay closes on an outside click", func(t *testing.T) {
		m := base
		m.showThemePick = true
		m.themeNames = theme.Names()
		_, hits := m.view()

		if hits.dialog.Empty() {
			t.Fatal("no dialog bounds were recorded")
		}
		if got := hits.resolve(click(0, 0), m.cursor); got == nil {
			t.Error("click outside the theme picker did not dismiss it")
		} else if _, ok := got.(mouseDismissMsg); !ok {
			t.Errorf("outside click = %T, want mouseDismissMsg", got)
		}

		// A click inside it is the dialog's own business, not a dismissal.
		inside := image.Pt(
			(hits.dialog.Min.X+hits.dialog.Max.X)/2,
			(hits.dialog.Min.Y+hits.dialog.Max.Y)/2,
		)
		if got := hits.resolve(click(inside.X, inside.Y), m.cursor); got != nil {
			t.Errorf("click inside the dialog = %T, want nil", got)
		}
	})

	t.Run("modal overlay ignores an outside click", func(t *testing.T) {
		m := base
		m.confirmMode = true
		m.confirmOn = m.filtered[0]
		_, hits := m.view()

		if hits.dialog.Empty() {
			t.Fatal("no dialog bounds were recorded")
		}
		if got := hits.resolve(click(0, 0), m.cursor); got != nil {
			t.Errorf("outside click on a destructive confirm = %T, want nil", got)
		}
	})
}

// TestDismissRestoresPreviewedTheme — cancelling the theme picker with the mouse
// has to undo the live preview, exactly as Esc does. Leaving the previewed theme
// applied would make a stray click silently change the user's theme.
func TestDismissRestoresPreviewedTheme(t *testing.T) {
	m := mouseModel(t, 120, 30)
	original := theme.Current().Name

	m.showThemePick = true
	m.themeNames = theme.Names()
	m.themeOriginal = original
	// Preview a different theme, the way ↑/↓ does.
	for i, name := range m.themeNames {
		if name != original {
			m.themeCursor = i
			theme.Set(name)
			break
		}
	}
	if theme.Current().Name == original {
		t.Fatal("could not preview a different theme")
	}

	m = driveModel(t, m, mouseDismissMsg{})

	if m.showThemePick {
		t.Error("theme picker stayed open after a dismiss")
	}
	if got := theme.Current().Name; got != original {
		t.Errorf("theme after dismiss = %q, want the original %q", got, original)
	}
}

// TestMouseDisabledEmitsNoHandler — the config switch has to actually reach the
// view, because enabling mouse reporting takes text selection away from the
// terminal and some people will want it off.
func TestMouseDisabledEmitsNoHandler(t *testing.T) {
	m := mouseModel(t, 120, 30)

	if v := m.View(); v.OnMouse == nil || v.MouseMode == tea.MouseModeNone {
		t.Error("mouse enabled but the view reports no mouse handling")
	}

	m.mouseEnabled = false
	v := m.View()
	if v.OnMouse != nil {
		t.Error("mouse disabled but the view still installed a handler")
	}
	if v.MouseMode != tea.MouseModeNone {
		t.Errorf("mouse disabled but MouseMode = %v", v.MouseMode)
	}
}
