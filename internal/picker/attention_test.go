package picker

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nemanja-micovic/nagare-go/internal/models"
)

// ansiStrip drops escape sequences so an assertion reads the visible text.
func ansiStrip(s string) string { return ansi.Strip(s) }

// waitingSet builds sessions from a compact status string, so a test reads as
// the shape it is testing: "w" waiting, "r" running, "i" idle.
func waitingSet(pattern string) []models.Session {
	out := make([]models.Session, 0, len(pattern))
	for i, c := range pattern {
		status := models.StatusIdle
		switch c {
		case 'w':
			status = models.StatusWaitingInput
		case 'r':
			status = models.StatusRunning
		}
		out = append(out, models.Session{
			Name:        string(rune('a' + i)),
			SessionName: string(rune('a' + i)),
			Path:        "/tmp/" + string(rune('a'+i)),
			Status:      status,
			AgentType:   models.AgentClaude,
		})
	}
	return out
}

func TestNextAttention(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		from    int
		want    int
	}{
		{"finds the next one forward", "iwiw", 0, 1},
		{"skips non-waiting sessions", "iiiw", 0, 3},
		{"wraps around the end", "wiii", 2, 0},
		{"skips itself when nothing else waits", "iwii", 1, 1},
		{"running does not count", "irrr", 0, -1},
		{"idle does not count", "iiii", 0, -1},
		{"nothing at all", "", 0, -1},
		{"every session waiting advances by one", "wwww", 1, 2},
		{"wraps from the last index", "wwww", 3, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextAttention(waitingSet(tc.pattern), tc.from); got != tc.want {
				t.Errorf("nextAttention(%q, %d) = %d, want %d", tc.pattern, tc.from, got, tc.want)
			}
		})
	}
}

// TestNextAttentionVisitsEveryWaitingSession is the property that makes the key
// usable as "work through the queue": repeated presses reach all of them before
// repeating. Jumping to the most urgent each time would ping-pong instead.
func TestNextAttentionVisitsEveryWaitingSession(t *testing.T) {
	sessions := waitingSet("wirwiiww")
	want := map[int]bool{}
	for i, s := range sessions {
		if s.Status == models.StatusWaitingInput {
			want[i] = true
		}
	}
	if len(want) < 3 {
		t.Fatalf("fixture should hold at least 3 waiting sessions, has %d", len(want))
	}

	seen := map[int]bool{}
	at := 0
	for i := 0; i < len(want); i++ {
		at = nextAttention(sessions, at)
		if at < 0 {
			t.Fatalf("ran out of waiting sessions after %d of %d", i, len(want))
		}
		if seen[at] {
			t.Fatalf("revisited session %d before covering all of %v", at, want)
		}
		seen[at] = true
	}
	for idx := range want {
		if !seen[idx] {
			t.Errorf("waiting session %d was never visited", idx)
		}
	}
}

func TestWaitingCount(t *testing.T) {
	for pattern, want := range map[string]int{
		"":     0,
		"iiii": 0,
		"w":    1,
		"wiwi": 2,
		"wwww": 4,
		"rrrr": 0,
	} {
		if got := waitingCount(waitingSet(pattern)); got != want {
			t.Errorf("waitingCount(%q) = %d, want %d", pattern, got, want)
		}
	}
}

// TestJumpToNextAttentionMovesCursor drives it through the model, including the
// sort the picker applies — the jump has to work in display order, not in the
// order the scanner happened to report.
func TestJumpToNextAttentionMovesCursor(t *testing.T) {
	m := driveModel(t, NewForTest(),
		tea.WindowSizeMsg{Width: 120, Height: 30},
		SessionsUpdatedMsg(waitingSet("iwiw")),
	)

	next, _ := m.jumpToNextAttention()
	got := next.(Model)

	if got.statusNote != "" {
		t.Errorf("jump reported %q despite waiting sessions", got.statusNote)
	}
	if s, ok := got.selectedSession(); !ok || s.Status != models.StatusWaitingInput {
		t.Errorf("cursor landed on %+v, want a waiting session", s.Status)
	}
}

// TestJumpToNextAttentionSaysWhenIdle — a key that silently does nothing reads
// as broken, and "nothing is waiting" is real information.
func TestJumpToNextAttentionSaysWhenIdle(t *testing.T) {
	m := driveModel(t, NewForTest(),
		tea.WindowSizeMsg{Width: 120, Height: 30},
		SessionsUpdatedMsg(waitingSet("iiii")),
	)
	before := m.cursor

	next, _ := m.jumpToNextAttention()
	got := next.(Model)

	if got.statusNote == "" {
		t.Error("jump with nothing waiting said nothing")
	}
	if got.cursor != before {
		t.Errorf("cursor moved to %d with nothing waiting", got.cursor)
	}
	// The note has to actually reach the screen.
	if !strings.Contains(ansiStrip(got.statusLine()), "nothing is waiting") {
		t.Errorf("status line does not show the note: %q", ansiStrip(got.statusLine()))
	}
}

// TestStatusNoteClearsOnNextKey — notes are transient, like the error line. One
// left on screen would look like a permanent state.
func TestStatusNoteClearsOnNextKey(t *testing.T) {
	m := driveModel(t, NewForTest(),
		tea.WindowSizeMsg{Width: 120, Height: 30},
		SessionsUpdatedMsg(waitingSet("iiii")),
	)
	next, _ := m.jumpToNextAttention()
	m = next.(Model)
	if m.statusNote == "" {
		t.Fatal("expected a note to clear")
	}

	m = driveModel(t, m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if m.statusNote != "" {
		t.Errorf("note survived a keypress: %q", m.statusNote)
	}
}

// TestFooterOffersTheQueueOnlyWhenThereIsOne, and names its size — "3 waiting"
// is a different situation from "1 waiting".
func TestFooterOffersTheQueueOnlyWhenThereIsOne(t *testing.T) {
	quiet := driveModel(t, NewForTest(),
		tea.WindowSizeMsg{Width: 120, Height: 30},
		SessionsUpdatedMsg(waitingSet("iiii")),
	)
	if hasKey(quiet, "F4") {
		t.Error("footer offers F4 with nothing waiting")
	}

	one := driveModel(t, NewForTest(),
		tea.WindowSizeMsg{Width: 120, Height: 30},
		SessionsUpdatedMsg(waitingSet("iwii")),
	)
	if !hasKey(one, "F4") {
		t.Fatal("footer does not offer F4 with a session waiting")
	}
	if got := footerText(one, 120); !strings.Contains(got, "Next waiting") {
		t.Errorf("footer for one waiting session = %q", got)
	}

	many := driveModel(t, NewForTest(),
		tea.WindowSizeMsg{Width: 120, Height: 30},
		SessionsUpdatedMsg(waitingSet("wwiw")),
	)
	if got := footerText(many, 120); !strings.Contains(got, "Next of 3 waiting") {
		t.Errorf("footer for three waiting sessions = %q", got)
	}
}

// TestWaitingCountFollowsTheSearch — the footer counts what is on screen, so a
// search that hides a waiting session must not advertise a jump the key will not
// make.
func TestWaitingCountFollowsTheSearch(t *testing.T) {
	sessions := waitingSet("wwii")
	sessions[0].Name = "alpha-waiting"
	sessions[0].SessionName = "alpha-waiting"
	sessions[1].Name = "beta-waiting"
	sessions[1].SessionName = "beta-waiting"

	m := driveModel(t, NewForTest(),
		tea.WindowSizeMsg{Width: 120, Height: 30},
		SessionsUpdatedMsg(sessions),
	)
	if got := waitingCount(m.filtered); got != 2 {
		t.Fatalf("unfiltered waiting count = %d, want 2", got)
	}

	m = typeString(t, m, "alpha")
	if got := waitingCount(m.filtered); got != 1 {
		t.Errorf("waiting count after searching \"alpha\" = %d, want 1", got)
	}
}
