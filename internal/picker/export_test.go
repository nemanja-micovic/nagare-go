package picker

import "github.com/nemke/nagare-go/internal/tmux"

// NewForTest returns a Model with the live tmux scanner disabled. Tests drive
// the session list exclusively via SessionsUpdatedMsg, which keeps runs
// hermetic (no dependency on the developer's current tmux state).
func NewForTest() Model {
	m := New()
	m.testNoScan = true
	// Focus mode must never reach a real tmux server from a test: it would
	// resize, and type into, whatever pane on the developer's machine happened
	// to match.
	m.newQueue = func() *tmux.Queue {
		return tmux.NewQueueWith(func(...string) (string, error) { return "", nil })
	}
	return m
}
