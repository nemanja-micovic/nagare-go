package picker

import (
	"math"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/nemanja-micovic/nagare-go/internal/models"
	"github.com/nemanja-micovic/nagare-go/internal/theme"
)

// TestFlashTransitions — the flash deliberately fires on the same two moments the
// notification layer does, and on nothing else. Flashing on every status change
// would make the list twitch constantly and mean nothing.
func TestFlashTransitions(t *testing.T) {
	cases := []struct {
		from, to models.SessionStatus
		want     flashKind
	}{
		{models.StatusRunning, models.StatusWaitingInput, flashAttention},
		{models.StatusIdle, models.StatusWaitingInput, flashAttention},
		{models.StatusRunning, models.StatusIdle, flashDone},
		{models.StatusIdle, models.StatusRunning, flashNone},
		{models.StatusWaitingInput, models.StatusRunning, flashNone},
		{models.StatusRunning, models.StatusDead, flashNone},
		{models.StatusIdle, models.StatusIdle, flashNone},
		{models.StatusWaitingInput, models.StatusWaitingInput, flashNone},
	}
	for _, tc := range cases {
		if got := flashTransition(tc.from, tc.to); got != tc.want {
			t.Errorf("flashTransition(%s, %s) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

// TestFirstSightDoesNotFlash — without this, every waiting agent flashes the
// instant the picker opens, which is noise rather than news.
func TestFirstSightDoesNotFlash(t *testing.T) {
	sessions := waitingSet("wwiw")
	if got := detectFlashes(nil, sessions); len(got) != 0 {
		t.Errorf("first scan produced %d flashes, want none", len(got))
	}

	// Once seen, a change is news.
	prev := statusesOf(sessions)
	sessions[2].Status = models.StatusWaitingInput
	got := detectFlashes(prev, sessions)
	if len(got) != 1 {
		t.Fatalf("got %d flashes, want 1", len(got))
	}
	for _, f := range got {
		if f.kind != flashAttention || f.level != 1 {
			t.Errorf("flash = %+v, want attention at full level", f)
		}
	}
}

// TestFlashEaseArrivesBrightAndLetsGo — a linear fade reads as a light being
// switched off. This checks the curve is monotone and front-loaded.
func TestFlashEaseArrivesBrightAndLetsGo(t *testing.T) {
	if got := flashEase(1); math.Abs(got-1) > 1e-9 {
		t.Errorf("flashEase(1) = %v, want 1", got)
	}
	if got := flashEase(0); got != 0 {
		t.Errorf("flashEase(0) = %v, want 0", got)
	}
	prev := flashEase(1)
	for level := 1.0; level > 0; level = flashDecay(level) {
		cur := flashEase(level)
		if cur > prev+1e-9 {
			t.Fatalf("ease rose at level %.2f", level)
		}
		prev = cur
	}
	// Front-loaded: it holds near full brightness longer than it lingers near zero.
	if flashEase(0.9) < 0.9 {
		t.Errorf("flashEase(0.9) = %.3f; the fade should still be near full", flashEase(0.9))
	}
	if flashEase(0.1) > 0.3 {
		t.Errorf("flashEase(0.1) = %.3f; the fade should be nearly gone", flashEase(0.1))
	}
}

// TestFlashesDecayAndClearThemselves — a flash left in the map keeps the clock
// running for the life of the picker.
func TestFlashesDecayAndClearThemselves(t *testing.T) {
	flashes := map[string]flashState{
		"a": {kind: flashAttention, level: 1},
		"b": {kind: flashDone, level: 0.5},
	}
	frames := 0
	for stepFlashes(flashes) {
		frames++
		if frames > 100 {
			t.Fatal("flashes never cleared")
		}
	}
	if len(flashes) != 0 {
		t.Errorf("%d flashes left behind", len(flashes))
	}
	// About the advertised duration, at the clock's rate.
	want := int(float64(flashDuration) / float64(1e9/breathFPS))
	if frames < want-2 || frames > want+2 {
		t.Errorf("fade took %d frames, want about %d", frames, want)
	}
}

// TestFlashReachesTheRow — the tint has to show up in the rendered frame, and be
// gone once the fade finishes.
func TestFlashReachesTheRow(t *testing.T) {
	m := driveModel(t, NewForTest(),
		tea.WindowSizeMsg{Width: 120, Height: 30},
		SessionsUpdatedMsg(waitingSet("iiii")),
	)

	// A session starts waiting: that is a transition, so it should flash.
	changed := waitingSet("iiii")
	changed[1].Status = models.StatusWaitingInput
	m = driveModel(t, m, SessionsUpdatedMsg(changed))

	if len(m.flashes) != 1 {
		t.Fatalf("got %d flashes after a transition, want 1", len(m.flashes))
	}

	tinted, _ := m.view()
	c := theme.Current().Colors
	if !hasFlashTint(tinted, c) {
		t.Error("no flash tint reached the frame")
	}

	// Run the clock out; the tint must go away and the clock must stop.
	var cmd tea.Cmd
	for i := 0; i < 5*breathFPS; i++ {
		var next tea.Model
		next, cmd = m.Update(tickBreathMsg{})
		m = next.(Model)
		if len(m.flashes) == 0 {
			break
		}
	}
	if len(m.flashes) != 0 {
		t.Fatal("flashes did not clear")
	}
	settled, _ := m.view()
	if hasFlashTint(settled, c) {
		t.Error("flash tint still on screen after the fade finished")
	}
	// Nothing is waiting in this fixture except the one that flashed, which is
	// still waiting — so the breath keeps the clock alive. That is expected; what
	// matters is that a fully idle list stops it, covered in breath_test.go.
	_ = cmd
}

// hasFlashTint reports whether any cell carries a background between the surface
// and the warning colour — the mid-fade tint.
func hasFlashTint(frame string, c theme.Colors) bool {
	for _, level := range []float64{1, 0.89, 0.78, 0.67, 0.56} {
		tint := flashTint(c.Surface, flashState{kind: flashAttention, level: level}, flashRowDepth)
		if strings.Contains(frame, bgSGR(tint)) {
			return true
		}
	}
	return false
}

// TestWorktreeArrivalFlashes — the spinner sits above the list and the new pane
// lands somewhere inside it, sorted among everything else. Without a hand-off the
// wait ends by the spinner vanishing and leaving the user to find what it made.
func TestWorktreeArrivalFlashes(t *testing.T) {
	m := driveModel(t, NewForTest(),
		tea.WindowSizeMsg{Width: 120, Height: 30},
		SessionsUpdatedMsg(waitingSet("ii")),
	)
	m.pending = &pendingWorktree{name: "feat-x", deadline: farFuture()}

	// The scan that first reports the new pane.
	arrived := append(waitingSet("ii"), models.Session{
		Name: "repo/feat-x", SessionName: "repo", Path: "/tmp/repo/feat-x",
		Status: models.StatusIdle, AgentType: models.AgentClaude,
		Details: models.SessionDetails{RepoName: "repo", Worktree: "feat-x"},
	})
	m = driveModel(t, m, SessionsUpdatedMsg(arrived))

	if m.pending != nil {
		t.Error("pending worktree not cleared once its pane appeared")
	}
	if len(m.flashes) != 1 {
		t.Fatalf("got %d flashes on arrival, want 1", len(m.flashes))
	}
	for key, f := range m.flashes {
		if f.kind != flashDone {
			t.Errorf("arrival flash is %v, want flashDone", f.kind)
		}
		if !strings.Contains(key, "repo") {
			t.Errorf("flash landed on %q, not the new worktree's pane", key)
		}
	}
	if m.arrived != "" {
		t.Errorf("arrival marker not cleared: %q", m.arrived)
	}
}

func farFuture() time.Time { return time.Now().Add(time.Hour) }
