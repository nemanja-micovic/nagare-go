package picker

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/nemke/nagare-go/internal/models"
)

// withStatuses returns the model's sessions with statuses changed by key.
func withStatuses(m Model, set map[string]models.SessionStatus) []models.Session {
	out := append([]models.Session(nil), m.sessions...)
	for i := range out {
		if st, ok := set[sessionKey(out[i])]; ok {
			out[i].Status = st
		}
	}
	return out
}

// TestToastForAgentsOffTheKeyboard — in focus mode, another agent starting to
// wait raises a toast; the agent with the keyboard does not, since the user is
// looking straight at it.
func TestToastForAgentsOffTheKeyboard(t *testing.T) {
	m, _ := splitModel(t, 200, 50, 4, 1)
	active := m.focus.cur().key
	var other string
	for _, s := range m.sessions {
		if sessionKey(s) != active {
			other = sessionKey(s)
			break
		}
	}
	m = driveModel(t, m, SessionsUpdatedMsg(withStatuses(m, map[string]models.SessionStatus{
		active: models.StatusWaitingInput,
		other:  models.StatusWaitingInput,
	})))
	if len(m.toasts) != 1 || m.toasts[0].key != other || m.toasts[0].kind != flashAttention {
		t.Fatalf("toasts = %+v, want one attention toast for the other agent", m.toasts)
	}
	if !strings.Contains(m.toasts[0].text, "needs you") {
		t.Errorf("toast text %q", m.toasts[0].text)
	}

	frame, _ := m.view()
	if !strings.Contains(ansi.Strip(frame), "needs you · F4") {
		t.Error("the toast is not drawn")
	}
	rows := strings.Split(frame, "\n")
	if len(rows) != 50 {
		t.Fatalf("frame has %d rows with a toast", len(rows))
	}
	for i, r := range rows {
		if w := ansi.StringWidth(r); w != 200 {
			t.Fatalf("row %d is %d wide with a toast", i, w)
		}
	}
}

func TestNoToastsInTheList(t *testing.T) {
	m := newVisualModel(t, 140, 36)
	var key string
	for _, s := range m.sessions {
		if s.Status != models.StatusWaitingInput {
			key = sessionKey(s)
		}
	}
	m = driveModel(t, m, SessionsUpdatedMsg(withStatuses(m, map[string]models.SessionStatus{key: models.StatusWaitingInput})))
	if len(m.toasts) != 0 {
		t.Errorf("toasts in the list view: %+v", m.toasts)
	}
}

func TestToastsExpireAndCap(t *testing.T) {
	now := time.Now()
	ts := []toast{{key: "a", until: now.Add(-time.Second)}, {key: "b", until: now.Add(time.Second)}}
	if got := pruneToasts(ts, now); len(got) != 1 || got[0].key != "b" {
		t.Errorf("pruneToasts = %+v", got)
	}

	m, _ := splitModel(t, 200, 50, 6, 1)
	fl := map[string]flashState{}
	for _, s := range m.sessions {
		if sessionKey(s) != m.focus.cur().key {
			fl[sessionKey(s)] = flashState{kind: flashDone, level: 1}
		}
	}
	m.pushToasts(fl, now)
	if len(m.toasts) != maxToasts {
		t.Errorf("%d toasts, want at most %d", len(m.toasts), maxToasts)
	}
}
