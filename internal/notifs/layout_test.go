package notifs

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nemanja-micovic/nagare-go/internal/notifications"
)

// seeded builds a notification centre with a fixed item list, so the test does
// not depend on whatever is in the developer's notification store.
func seeded(t *testing.T, w, h, count, tab, cursor int) Model {
	t.Helper()
	m := New()
	m.items = make([]notifications.Notification, 0, count)
	for i := 0; i < count; i++ {
		m.items = append(m.items, notifications.Notification{
			ID:          fmt.Sprintf("n%d", i),
			SessionName: "cosmic-platform-backend",
			Message:     "Claude finished a long task and left a message worth reading",
			Timestamp:   "2026-08-18 09:15:0" + fmt.Sprint(i%10),
			Read:        i%2 == 0,
		})
	}
	m.tab = tab
	m.cursor = cursor
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return next.(Model)
}

var notifSizes = [][2]int{{200, 50}, {120, 30}, {90, 24}, {70, 20}, {50, 16}, {40, 12}}

// TestNotifsFillsItsFrameExactly asserts on the unclamped frame, because View's
// MaxWidth/MaxHeight is what hid this: the notification list was windowed by item
// *count* while each item renders two rows, so it emitted roughly twice the rows
// it had budgeted — 92 rows into a 50-row frame — and the clamp trimmed the
// overflow off the bottom, which is where the hint bar lives.
func TestNotifsFillsItsFrameExactly(t *testing.T) {
	for _, tab := range []int{0, 1} {
		for _, count := range []int{0, 1, 3, 30} {
			for _, sz := range notifSizes {
				w, h := sz[0], sz[1]
				t.Run(fmt.Sprintf("tab%d/%ditems/%dx%d", tab, count, w, h), func(t *testing.T) {
					raw := seeded(t, w, h, count, tab, 0).view()

					if got := lipgloss.Height(raw); got != h {
						t.Errorf("frame is %d rows, want %d", got, h)
					}
					for i, row := range strings.Split(raw, "\n") {
						if got := ansi.StringWidth(row); got != w {
							t.Errorf("row %d is %d cells, want %d", i, got, w)
						}
					}
				})
			}
		}
	}
}

// TestNotifsHintBarSurvives — it is the only thing telling the user how to leave.
func TestNotifsHintBarSurvives(t *testing.T) {
	for _, tab := range []int{0, 1} {
		for _, sz := range notifSizes {
			w, h := sz[0], sz[1]
			m := seeded(t, w, h, 30, tab, 0)

			if got := lipgloss.Height(m.renderHintBar()); got != 1 {
				t.Errorf("tab %d %dx%d: hint bar is %d rows, want 1", tab, w, h, got)
			}
			if !strings.Contains(ansi.Strip(m.view()), "Esc") {
				t.Errorf("tab %d %dx%d: hint bar missing from the frame", tab, w, h)
			}
		}
	}
}

// TestNotifsKeepsCursorVisible — a window that scrolls by rows still has to show
// the selected item, wherever it is in the list.
func TestNotifsKeepsCursorVisible(t *testing.T) {
	const count = 30
	for _, cursor := range []int{0, 1, 15, count - 1} {
		m := seeded(t, 120, 30, count, 0, cursor)
		frame := ansi.Strip(m.view())
		want := m.items[cursor].Timestamp
		if !strings.Contains(frame, want) {
			t.Errorf("cursor %d: selected item (%s) is not on screen", cursor, want)
		}
	}
}

// TestRowWindowNeverExceedsBudget is the unit-level guard on the helper both tabs
// share. Item heights vary, which is exactly why counting items was wrong.
func TestRowWindowNeverExceedsBudget(t *testing.T) {
	cases := []struct {
		heights []int
		cursor  int
		height  int
	}{
		{[]int{2, 2, 2, 2, 2}, 0, 6},
		{[]int{2, 2, 2, 2, 2}, 4, 6},
		{[]int{3, 2, 4, 2, 3}, 2, 7},
		{[]int{2}, 0, 1},    // a single item taller than the budget
		{[]int{5, 5}, 1, 3}, // nothing fits cleanly
		{nil, 0, 10},
	}
	for _, tc := range cases {
		start, end := rowWindow(tc.heights, tc.cursor, tc.height)
		if start < 0 || end > len(tc.heights) || start > end {
			t.Fatalf("rowWindow(%v, %d, %d) = [%d,%d), out of range",
				tc.heights, tc.cursor, tc.height, start, end)
		}
		total := 0
		for _, h := range tc.heights[start:end] {
			total += h
		}
		// One item may exceed the budget on its own; more than one must not.
		if end-start > 1 && total > tc.height {
			t.Errorf("rowWindow(%v, %d, %d) selected %d rows, over budget",
				tc.heights, tc.cursor, tc.height, total)
		}
		if len(tc.heights) > 0 && (tc.cursor < start || tc.cursor >= end) {
			t.Errorf("rowWindow(%v, %d, %d) = [%d,%d), which excludes the cursor",
				tc.heights, tc.cursor, tc.height, start, end)
		}
	}
}
