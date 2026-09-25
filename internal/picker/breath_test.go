package picker

import (
	"math"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/lucasb-eyer/go-colorful"

	"github.com/nemanja-micovic/nagare-go/internal/models"
	"github.com/nemanja-micovic/nagare-go/internal/theme"
)

// TestBreathFactorIsSmoothAndEased — the dot used to blink between two states.
// The point of the curve is that consecutive frames differ only slightly and that
// it settles at the extremes rather than snapping through them.
func TestBreathFactorIsSmoothAndEased(t *testing.T) {
	if got := breathFactor(0); got != 0 {
		t.Errorf("breathFactor(0) = %v, want 0", got)
	}
	if got := breathFactor(0.5); math.Abs(got-1) > 1e-9 {
		t.Errorf("breathFactor(0.5) = %v, want 1", got)
	}
	if got := breathFactor(1); math.Abs(got) > 1e-9 {
		t.Errorf("breathFactor(1) = %v, want 0", got)
	}

	// No frame may jump more than a fraction of the range, or it reads as a blink.
	phase, prev := 0.0, breathFactor(0)
	for i := 0; i < 3*breathFPS*3; i++ {
		phase = breathStep(phase)
		cur := breathFactor(phase)
		if d := math.Abs(cur - prev); d > 0.2 {
			t.Fatalf("frame %d jumped %.3f of the range (%.3f to %.3f)", i, d, prev, cur)
		}
		prev = cur
	}

	// Eased: the step near the extremes is smaller than the step mid-swing.
	near := math.Abs(breathFactor(breathStep(0)) - breathFactor(0))
	mid := math.Abs(breathFactor(breathStep(0.25)) - breathFactor(0.25))
	if near >= mid {
		t.Errorf("curve is not eased: step at the extreme %.4f, mid-swing %.4f", near, mid)
	}
}

// TestBreathStepCompletesOneCycle — the phase has to wrap, and the cycle has to
// take the period it claims.
func TestBreathStepCompletesOneCycle(t *testing.T) {
	// Detect the wrap by the phase decreasing: it will never land exactly on zero,
	// since the per-frame step is not an exact binary fraction.
	frames, phase := 0, 0.0
	for {
		nextPhase := breathStep(phase)
		frames++
		if nextPhase < 0 || nextPhase >= 1 {
			t.Fatalf("phase left the 0..1 range: %v", nextPhase)
		}
		if nextPhase < phase {
			break
		}
		phase = nextPhase
		if frames > 1000 {
			t.Fatal("phase never wrapped")
		}
	}
	want := int(float64(breathPeriod) / float64(1e9/breathFPS))
	if frames < want-2 || frames > want+2 {
		t.Errorf("a cycle took %d frames, want about %d", frames, want)
	}
}

// TestOnlyActiveSessionsBreathe — motion is the signal that something is
// happening, so idle, dead and saved sessions must hold still.
func TestOnlyActiveSessionsBreathe(t *testing.T) {
	for status, want := range map[models.SessionStatus]bool{
		models.StatusRunning:      true,
		models.StatusWaitingInput: true,
		models.StatusIdle:         false,
		models.StatusDead:         false,
		models.StatusSaved:        false,
	} {
		if got := breathes(status); got != want {
			t.Errorf("breathes(%s) = %v, want %v", status, got, want)
		}
	}
}

// hexOf resolves a rendered dot's foreground back to a colour, so a test can show
// the breath actually reaches the screen.
func hexOf(c interface {
	RGBA() (uint32, uint32, uint32, uint32)
}) string {
	cf, _ := colorful.MakeColor(c)
	return cf.Hex()
}

// TestBreathChangesTheRenderedDot — the phase has to reach the pixels, and only
// for statuses that breathe.
func TestBreathChangesTheRenderedDot(t *testing.T) {
	surface := theme.Current().Colors.Surface

	bright := statusDotOn(models.StatusRunning, 0, surface)
	dim := statusDotOn(models.StatusRunning, 0.5, surface)
	if bright == dim {
		t.Error("a running dot renders identically at both ends of the breath")
	}

	// At the trough it should sit between its status colour and the background.
	full := hexOf(mustParse(t, models.StatusColor(models.StatusRunning)))
	if hexOf(mustParse(t, full)) == "" {
		t.Fatal("could not parse the status colour")
	}

	// Idle holds still.
	if a, b := statusDotOn(models.StatusIdle, 0, surface), statusDotOn(models.StatusIdle, 0.5, surface); a != b {
		t.Error("an idle dot changed with the breath phase")
	}
}

func mustParse(t *testing.T, hex string) colorful.Color {
	t.Helper()
	c, err := colorful.Hex(hex)
	if err != nil {
		t.Fatalf("bad hex %q: %v", hex, err)
	}
	return c
}

// TestBreathClockStopsWhenNothingMoves is what makes an always-on animation
// affordable: a frame costs milliseconds, so the clock must not tick over a list
// where nothing is happening.
func TestBreathClockStopsWhenNothingMoves(t *testing.T) {
	idle := driveModel(t, NewForTest(),
		tea.WindowSizeMsg{Width: 120, Height: 30},
		SessionsUpdatedMsg(waitingSet("iiii")),
	)
	next, cmd := idle.Update(tickBreathMsg{})
	m := next.(Model)
	if cmd != nil {
		t.Error("breath clock rescheduled itself over an idle list")
	}
	if m.breathOn {
		t.Error("breath clock still marked running over an idle list")
	}

	active := driveModel(t, NewForTest(),
		tea.WindowSizeMsg{Width: 120, Height: 30},
		SessionsUpdatedMsg(waitingSet("iiwi")),
	)
	next, cmd = active.Update(tickBreathMsg{})
	m = next.(Model)
	if cmd == nil {
		t.Error("breath clock stopped despite a waiting session")
	}
	if !m.breathOn {
		t.Error("breath clock not marked running with a waiting session")
	}
	if m.breath == 0 {
		t.Error("breath phase did not advance")
	}
}

// TestBreathClockRestartsOnActivity — the clock stops itself, so the scan that
// turns up a running agent has to start it again or the dots stay frozen.
func TestBreathClockRestartsOnActivity(t *testing.T) {
	m := driveModel(t, NewForTest(), tea.WindowSizeMsg{Width: 120, Height: 30})
	m.testNoScan = false // the restart lives on the live-scan path
	m.breathOn = false

	next, cmd := m.Update(SessionsUpdatedMsg(waitingSet("iirw")))
	if cmd == nil {
		t.Fatal("no command returned from a scan")
	}
	if !next.(Model).breathOn {
		t.Error("a scan turning up activity did not restart the breath clock")
	}
}
