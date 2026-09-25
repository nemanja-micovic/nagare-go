package picker

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/nemke/nagare-go/internal/tmux"
)

// Event-driven capture. While the watcher is healthy, a tile is captured when
// its pane prints — not on a timer — so an idle agent costs nothing and an echo
// is drawn as soon as tmux has it. Polling stays as a slow safety net, and takes
// over entirely if control mode is unavailable.

// outputWatcher is tmux.Watcher, behind an interface so tests can run without
// tmux.
type outputWatcher interface {
	Watch(sessions []string)
	Events() <-chan string
	Healthy() bool
	Close()
}

const (
	// focusFallbackPoll is how often tiles are captured anyway while output
	// events are flowing: a resize reflow, say, prints nothing.
	focusFallbackPoll = time.Second
	// focusEventGap throttles captures of the active tile to ~30fps however
	// fast its agent prints — the same ceiling polling had.
	focusEventGap = 33 * time.Millisecond
	// outputCoalesce gathers a burst of output events into one capture.
	outputCoalesce = 4 * time.Millisecond
)

// paneOutputMsg names the panes that printed since the last one.
type paneOutputMsg struct{ panes map[string]bool }

func defaultWatcher() outputWatcher { return tmux.NewWatcher() }

// waitOutput blocks until a pane prints, then gathers the rest of the burst.
func waitOutput(w outputWatcher) tea.Cmd {
	return func() tea.Msg {
		ev := w.Events()
		p, ok := <-ev
		if !ok {
			return nil
		}
		panes := map[string]bool{p: true}
		deadline := time.After(outputCoalesce)
		for {
			select {
			case p := <-ev:
				panes[p] = true
			case <-deadline:
				return paneOutputMsg{panes: panes}
			}
		}
	}
}

// eventsLive reports whether captures can be driven by output events.
func (m Model) eventsLive() bool {
	return m.watcher != nil && m.watcher.Healthy()
}

// syncWatch points the watcher at the sessions holding a tiled pane, starting
// the event loop the first time. It returns that loop's command, or nil.
func (m *Model) syncWatch() tea.Cmd {
	if m.watcher == nil {
		if m.newWatcher == nil {
			return nil
		}
		if m.watcher = m.newWatcher(); m.watcher == nil {
			return nil
		}
	}
	var sessions []string
	if m.focus.on {
		for i := range m.focus.n {
			if s, ok := m.sessionFor(m.focus.tiles[i].key); ok {
				sessions = append(sessions, s.SessionName)
			}
		}
	}
	m.watcher.Watch(sessions)
	if m.watchLoop {
		return nil
	}
	m.watchLoop = true
	return waitOutput(m.watcher)
}

// onPaneOutput captures the tiles whose panes printed.
func (m Model) onPaneOutput(msg paneOutputMsg) (Model, tea.Cmd) {
	next := waitOutput(m.watcher)
	f := &m.focus
	if !f.on {
		return m, next
	}
	now := time.Now()
	due := false
	for i := range f.n {
		t := f.tiles[i]
		if !msg.panes[t.pane] {
			continue
		}
		if i == f.active {
			if gap := now.Sub(t.lastCap); gap < focusEventGap {
				// Printed again within the frame: catch it at the end of the gap.
				return m, tea.Batch(next, m.focusPoll(focusEventGap-gap))
			}
			due = true
		} else if now.Sub(t.lastCap) >= focusBackgroundPoll {
			due = true
		}
	}
	if !due {
		return m, next
	}
	return m, tea.Batch(next, m.focusPoll(0))
}
