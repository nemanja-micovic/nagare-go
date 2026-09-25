package picker

import (
	"testing"
	"time"
)

// fakeWatcher is an outputWatcher driven by the test.
type fakeWatcher struct {
	ch       chan string
	healthy  bool
	sessions []string
	closed   bool
}

func (f *fakeWatcher) Watch(s []string)      { f.sessions = s }
func (f *fakeWatcher) Events() <-chan string { return f.ch }
func (f *fakeWatcher) Healthy() bool         { return f.healthy }
func (f *fakeWatcher) Close()                { f.closed = true }

func watchedModel(t *testing.T, tiles int) (Model, *fakeWatcher) {
	t.Helper()
	fw := &fakeWatcher{ch: make(chan string, 16), healthy: true}
	base, _ := splitModel(t, 240, 60, 5, 1)
	base = base.leaveFocus()
	base.newWatcher = func() outputWatcher { return fw }
	base, _ = base.enterFocus(base.filtered[0])
	for range tiles - 1 {
		base, _ = base.addTile()
	}
	return base, fw
}

// TestWatcherFollowsTiles — the watcher watches exactly the sessions with a
// tiled pane, and nothing once focus mode closes.
func TestWatcherFollowsTiles(t *testing.T) {
	m, fw := watchedModel(t, 2)
	if len(fw.sessions) != 2 {
		t.Errorf("watching %v with two tiles", fw.sessions)
	}
	m = m.leaveFocus()
	if len(fw.sessions) != 0 {
		t.Errorf("still watching %v after leaving focus mode", fw.sessions)
	}
	m.Close()
	if !fw.closed {
		t.Error("Close did not stop the watcher")
	}
}

// TestOutputCapturesTheTile — output in the active tile's pane captures it at
// once; output within the same frame waits for the end of the frame.
func TestOutputCapturesTheTile(t *testing.T) {
	m, _ := watchedModel(t, 1)
	pane := m.focus.cur().pane

	m.focus.tiles[m.focus.active].lastCap = time.Now().Add(-time.Second)
	_, cmd := m.onPaneOutput(paneOutputMsg{panes: map[string]bool{pane: true}})
	if !runsPoll(cmd, m.focus.seq+1) {
		t.Error("output in the active tile did not capture it")
	}

	m.focus.tiles[m.focus.active].lastCap = time.Now()
	next, _ := m.onPaneOutput(paneOutputMsg{panes: map[string]bool{pane: true}})
	if next.focus.seq == m.focus.seq {
		t.Error("output within the frame was dropped rather than scheduled")
	}

	_, cmd = m.onPaneOutput(paneOutputMsg{panes: map[string]bool{"%not-tiled": true}})
	if runsPoll(cmd, m.focus.seq+1) {
		t.Error("output in a pane that is not tiled triggered a capture")
	}
}

// TestEventsSlowThePolling — while events are healthy, polling is only a
// once-a-second safety net; without them, the adaptive polling is unchanged.
func TestEventsSlowThePolling(t *testing.T) {
	m, fw := watchedModel(t, 2)
	snap := func(m Model) Model {
		next, _ := m.Update(focusSnapMsg{seq: m.focus.seq, n: m.focus.applied + 1,
			snaps: []tileSnap{{pane: m.focus.cur().pane, screen: agentScreen(20, 5)}}})
		return next.(Model)
	}
	if got := snap(m).focus.delay; got != focusFallbackPoll {
		t.Errorf("delay with healthy events = %v, want %v", got, focusFallbackPoll)
	}
	fw.healthy = false
	if got := snap(m).focus.delay; got > focusBackgroundPoll {
		t.Errorf("delay without events = %v, want the adaptive polling back", got)
	}
}
