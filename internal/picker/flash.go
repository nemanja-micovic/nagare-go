package picker

import (
	"image/color"
	"math"
	"time"

	"github.com/nemanja-micovic/nagare-go/internal/models"
	"github.com/nemanja-micovic/nagare-go/internal/theme"
)

// Row flash.
//
// When a session changes to a state that wants the user, its row briefly lights up
// and fades back. The picker already knows about these two moments — they are the
// same transitions the notification layer fires on — so this is the on-screen half
// of a signal that otherwise only existed as a toast the user may have missed.
//
// The point is peripheral vision. A status dot changing colour is easy to miss on a
// list of thirty; a row that pulses draws the eye to itself without the user having
// to be watching for it.
//
// It runs on the same slow clock as the breath, and only while something is
// actually fading, so a settled list costs nothing.
const (
	// flashDuration is how long a row takes to fade back. Long enough to catch
	// after glancing away, short enough not to sit there insisting.
	flashDuration = 900 * time.Millisecond
	// flashRowDepth is how far a list row's background moves toward the flash
	// colour at its peak. A row has to stay readable while it flashes, so this is a
	// tint, not a takeover.
	flashRowDepth = 0.55
	// flashBorderDepth is the equivalent for a grid card, where the flash goes on
	// the border rather than the fill. A card is large enough that tinting all of it
	// would shout; its border is already the signal that carries focus, so lighting
	// that up says the same thing quietly.
	flashBorderDepth = 0.9
)

// flashKind distinguishes the two transitions worth announcing.
type flashKind int

const (
	flashNone flashKind = iota
	// flashAttention: an agent started waiting on the user.
	flashAttention
	// flashDone: an agent stopped working and wants nothing.
	flashDone
)

// flashState is one row's fade in progress.
type flashState struct {
	kind flashKind
	// level runs from 1 down to 0.
	level float64
}

// flashTransition reports which flash, if any, a status change deserves. It
// deliberately matches the notification events: reaching waiting_input is
// "needs_input", and running to idle is "task_complete". Anything else — a scan
// filling in a status for the first time, a session appearing, a pane dying — is
// not a transition the user asked to be told about.
func flashTransition(from, to models.SessionStatus) flashKind {
	if from == to {
		return flashNone
	}
	switch {
	case to == models.StatusWaitingInput:
		return flashAttention
	case from == models.StatusRunning && to == models.StatusIdle:
		return flashDone
	}
	return flashNone
}

// flashDecay steps a level down by one frame of the breath clock.
func flashDecay(level float64) float64 {
	step := float64(time.Second/breathFPS) / float64(flashDuration)
	if level-step <= 0 {
		return 0
	}
	return level - step
}

// flashEase shapes the fade so it arrives bright and lets go gently, rather than
// dimming at a constant rate — a linear fade reads as a light being switched off.
func flashEase(level float64) float64 {
	return math.Sin(level * math.Pi / 2)
}

// flashTint blends a colour toward the flash colour for its kind: warning for an
// agent that wants the user, success for one that has just finished.
//
// Only list rows use this. Flashing a grid card's border was tried and rejected
// along with the other card animations.
func flashTint(base color.Color, f flashState, depth float64) color.Color {
	if f.kind == flashNone || f.level <= 0 {
		return base
	}
	c := theme.Current().Colors
	toward := c.Warning
	if f.kind == flashDone {
		toward = c.Success
	}
	return theme.Mix(base, toward, depth*flashEase(f.level))
}

// detectFlashes compares a new scan against the previous statuses and returns the
// rows that should start flashing, keyed the same way the cursor tracks sessions.
func detectFlashes(prev map[string]models.SessionStatus, next []models.Session) map[string]flashState {
	out := map[string]flashState{}
	for _, s := range next {
		key := sessionKey(s)
		was, seen := prev[key]
		if !seen {
			// First sight of a session is not a transition. Without this every
			// waiting agent flashes the moment the picker opens.
			continue
		}
		if kind := flashTransition(was, s.Status); kind != flashNone {
			out[key] = flashState{kind: kind, level: 1}
		}
	}
	return out
}

// statusesOf snapshots the statuses a scan reported, for the next comparison.
func statusesOf(sessions []models.Session) map[string]models.SessionStatus {
	out := make(map[string]models.SessionStatus, len(sessions))
	for _, s := range sessions {
		out[sessionKey(s)] = s.Status
	}
	return out
}

// stepFlashes decays every active flash and drops the finished ones. It reports
// whether any remain, so the clock knows to keep going.
func stepFlashes(flashes map[string]flashState) bool {
	for key, f := range flashes {
		f.level = flashDecay(f.level)
		if f.level <= 0 {
			delete(flashes, key)
			continue
		}
		flashes[key] = f
	}
	return len(flashes) > 0
}
