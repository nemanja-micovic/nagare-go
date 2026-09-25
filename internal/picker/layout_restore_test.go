package picker

import (
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/nemanja-micovic/nagare-go/internal/models"
	"github.com/nemanja-micovic/nagare-go/internal/paths"
)

// TestLayoutRoundTrip — closing in focus mode saves the tiles; the next start
// reopens them once the scan lists their agents, with the same tile active.
func TestLayoutRoundTrip(t *testing.T) {
	t.Setenv(paths.DataDirEnv, t.TempDir())

	m, _ := splitModel(t, 240, 60, 5, 3)
	m.restoreEnabled = true
	activeKey := m.focus.cur().key
	var keys []string
	for i := range m.focus.n {
		keys = append(keys, m.focus.tiles[i].key)
	}
	m.saveLayout()

	l := loadLayout()
	if l == nil || len(l.Tiles) != 3 {
		t.Fatalf("saved layout = %+v", l)
	}

	fresh, _ := splitModel(t, 240, 60, 5, 1)
	fresh = fresh.leaveFocus()
	fresh.restore = l
	fresh = driveModel(t, fresh, SessionsUpdatedMsg(append([]models.Session(nil), fresh.sessions...)))
	if !fresh.focus.on || fresh.focus.n != 3 {
		t.Fatalf("restored %d tiles (on=%v), want 3", fresh.focus.n, fresh.focus.on)
	}
	for i, k := range keys {
		if fresh.focus.tiles[i].key != k {
			t.Errorf("tile %d is %q, want %q", i, fresh.focus.tiles[i].key, k)
		}
	}
	if fresh.focus.cur().key != activeKey {
		t.Errorf("active tile is %q, want %q", fresh.focus.cur().key, activeKey)
	}
}

// TestLayoutRememberedWhenLeavingFocus — focus mode has no quit key, so the
// usual way out is Ctrl+] then Esc; the layout must survive that.
func TestLayoutRememberedWhenLeavingFocus(t *testing.T) {
	t.Setenv(paths.DataDirEnv, t.TempDir())
	m, _ := splitModel(t, 200, 50, 3, 2)
	m.restoreEnabled = true
	m = driveModel(t, m, tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	m.Close()
	if l := loadLayout(); l == nil || len(l.Tiles) != 2 {
		t.Fatalf("layout after Ctrl+] and quit = %+v, want 2 tiles", l)
	}
}

// TestLayoutForgottenWhenLastTileCloses — closing the last tile is the
// deliberate "done".
func TestLayoutForgottenWhenLastTileCloses(t *testing.T) {
	t.Setenv(paths.DataDirEnv, t.TempDir())
	m, _ := splitModel(t, 200, 50, 3, 1)
	m.restoreEnabled = true
	m.saveLayout()
	m = driveModel(t, m, tea.KeyPressMsg{Code: 'x', Mod: tea.ModAlt})
	if _, err := os.Stat(layoutPath()); !os.IsNotExist(err) {
		t.Error("layout survived closing the last tile")
	}
}

// TestLayoutSkipsAgentsThatAreGone — a saved tile whose agent has exited is
// dropped, and with none left nagare starts on the list.
func TestLayoutSkipsAgentsThatAreGone(t *testing.T) {
	m := newVisualModel(t, 200, 50)
	m.restore = &savedLayout{Tiles: []savedTile{{Pane: "%999", Session: "gone", Agent: "claude"}}}
	m = driveModel(t, m, SessionsUpdatedMsg(append([]models.Session(nil), m.sessions...)))
	if m.focus.on || m.restore != nil {
		t.Errorf("focus on=%v restore=%v; want the list and the layout consumed", m.focus.on, m.restore)
	}
}

// TestLayoutRejectsReusedPaneID — after a tmux restart a saved pane id can
// belong to a different agent; it must not be reopened as if it were the same.
func TestLayoutRejectsReusedPaneID(t *testing.T) {
	m, _ := splitModel(t, 200, 50, 3, 1)
	m = m.leaveFocus()
	s := m.sessions[0]
	m.restore = &savedLayout{Tiles: []savedTile{{Pane: s.PaneID, Session: "some-other-repo", Agent: string(s.AgentType)}}}
	m = driveModel(t, m, SessionsUpdatedMsg(append([]models.Session(nil), m.sessions...)))
	if m.focus.on {
		t.Error("a pane id now in another session was restored")
	}
}

// TestLayoutActiveMapsAfterSkip — the saved active index refers to the saved
// tiles; when one before it is skipped, the right agent still gets the keyboard.
func TestLayoutActiveMapsAfterSkip(t *testing.T) {
	m, _ := splitModel(t, 240, 60, 4, 1)
	m = m.leaveFocus()
	a, b := m.sessions[0], m.sessions[1]
	tile := func(s models.Session) savedTile {
		return savedTile{Pane: s.PaneID, Session: s.SessionName, Agent: string(s.AgentType)}
	}
	m.restore = &savedLayout{Active: 2, Tiles: []savedTile{tile(a), {Pane: "%404", Session: "x", Agent: "claude"}, tile(b)}}
	m = driveModel(t, m, SessionsUpdatedMsg(append([]models.Session(nil), m.sessions...)))
	if m.focus.n != 2 || m.focus.cur().key != sessionKey(b) {
		t.Errorf("n=%d active=%q; want 2 tiles with %q active", m.focus.n, m.focus.cur().key, sessionKey(b))
	}
}
