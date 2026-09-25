package picker

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestNavigationDoesNotStartPreviewLoops — a directly fetched preview is
// stored and schedules nothing. Each cursor move used to schedule a refresh
// of its own, so every move added a polling loop that never ended.
func TestNavigationDoesNotStartPreviewLoops(t *testing.T) {
	m := newVisualModel(t, 140, 36)
	next, cmd := m.Update(PreviewUpdatedMsg("content"))
	if cmd != nil {
		t.Error("a navigation preview scheduled a refresh")
	}
	if next.(Model).preview != "content" {
		t.Error("the preview was not stored")
	}
}

// TestOnlyTheCurrentPreviewLoopRuns — moving the cursor retires the loop
// running before it; the retired loop's tick does nothing.
func TestOnlyTheCurrentPreviewLoopRuns(t *testing.T) {
	m := newVisualModel(t, 140, 36)
	old := m.previewSeq
	m = driveModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.previewSeq == old {
		t.Fatal("moving the cursor did not restart the preview loop")
	}
	if _, cmd := m.Update(tickPreviewMsg{seq: old}); cmd != nil {
		t.Error("a retired preview loop kept running")
	}
	if _, cmd := m.Update(tickPreviewMsg{seq: m.previewSeq}); cmd == nil {
		t.Error("the current preview loop did not fetch")
	}
}

// TestPreviewBacksOffWhileUnchanged — an unchanged preview doubles the delay
// up to the slow rate; a change snaps back to the fast rate.
func TestPreviewBacksOffWhileUnchanged(t *testing.T) {
	m := newVisualModel(t, 140, 36)
	loop := func(content string) {
		next, _ := m.Update(previewLoopMsg{seq: m.previewSeq, msg: PreviewUpdatedMsg(content)})
		m = next.(Model)
	}
	loop("a")
	if m.previewDelay != previewFastPoll {
		t.Fatalf("first delay %v", m.previewDelay)
	}
	for range 6 {
		loop("a")
	}
	if m.previewDelay != previewSlowPoll {
		t.Errorf("unchanged preview settled at %v, want %v", m.previewDelay, previewSlowPoll)
	}
	loop("b")
	if m.previewDelay != previewFastPoll {
		t.Errorf("a changed preview left the delay at %v", m.previewDelay)
	}
}
