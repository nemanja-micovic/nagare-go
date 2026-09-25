package picker

import (
	tea "charm.land/bubbletea/v2"

	"github.com/nemanja-micovic/nagare-go/internal/models"
)

// nextAttention returns the index of the next session that is waiting on the
// user, searching forward from `from` and wrapping around. It returns -1 when
// nothing is waiting.
//
// Forward-and-wrap rather than most-urgent-first, deliberately: repeated presses
// then walk every waiting session exactly once before coming back, which is what
// makes the key usable as "deal with the queue". Jumping to the most urgent one
// each time would bounce between the same two sessions forever.
//
// Only "waiting for input" counts. A running agent does not need anything from
// the user yet, and offering to jump there would train the reflex to interrupt
// work in progress.
func nextAttention(sessions []models.Session, from int) int {
	n := len(sessions)
	if n == 0 {
		return -1
	}
	for step := 1; step <= n; step++ {
		i := (from + step) % n
		if sessions[i].Status == models.StatusWaitingInput {
			return i
		}
	}
	return -1
}

// waitingCount reports how many sessions are waiting on the user, for the footer
// hint. It counts what is on screen, so a search that hides a waiting session
// does not advertise a jump the key will not make.
func waitingCount(sessions []models.Session) int {
	n := 0
	for _, s := range sessions {
		if s.Status == models.StatusWaitingInput {
			n++
		}
	}
	return n
}

// jumpToNextAttention moves the cursor to the next waiting session, or says that
// there is none. Saying so matters: a key that silently does nothing reads as
// broken, and "nothing is waiting" is genuinely useful information.
func (m Model) jumpToNextAttention() (tea.Model, tea.Cmd) {
	idx := nextAttention(m.filtered, m.cursor)
	if idx < 0 {
		m.statusNote = "nothing is waiting for you"
		return m, nil
	}
	m.cursor = idx
	return m, m.doPreview()
}
