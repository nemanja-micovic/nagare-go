package picker

import (
	"os"
	"testing"

	"github.com/nemke/nagare-go/internal/models"
	"github.com/nemke/nagare-go/internal/paths"
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

// TestLayoutForgottenFromTheList — quitting from the list means the list is
// where the user wanted to be.
func TestLayoutForgottenFromTheList(t *testing.T) {
	t.Setenv(paths.DataDirEnv, t.TempDir())
	m, _ := splitModel(t, 200, 50, 3, 2)
	m.restoreEnabled = true
	m.saveLayout()
	if loadLayout() == nil {
		t.Fatal("layout not saved from focus mode")
	}
	m = m.leaveFocus()
	m.saveLayout()
	if _, err := os.Stat(layoutPath()); !os.IsNotExist(err) {
		t.Error("layout survived quitting from the list")
	}
}

// TestLayoutSkipsAgentsThatAreGone — a saved tile whose agent has exited is
// dropped, and with none left nagare starts on the list.
func TestLayoutSkipsAgentsThatAreGone(t *testing.T) {
	m := newVisualModel(t, 200, 50)
	m.restore = &savedLayout{Tiles: []savedTile{{Pane: "%999", Key: "gone:0.0"}}}
	m = driveModel(t, m, SessionsUpdatedMsg(append([]models.Session(nil), m.sessions...)))
	if m.focus.on || m.restore != nil {
		t.Errorf("focus on=%v restore=%v; want the list and the layout consumed", m.focus.on, m.restore)
	}
}
