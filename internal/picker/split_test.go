package picker

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nemke/nagare-go/internal/theme"
	"github.com/nemke/nagare-go/internal/tmux"
)

// splitModel opens focus mode on the first of count sessions with Enter, then
// splits tiles-1 times, recording tmux commands instead of sending them.
func splitModel(t *testing.T, w, h, count, tiles int) (Model, *sentKeys) {
	t.Helper()
	sessions := longSessions(count)
	for i := range sessions {
		sessions[i].WindowIndex = i
		sessions[i].PaneID = fmt.Sprintf("%%%d", 10+i)
	}
	m := gridModel(t, w, h, sessions)
	m.viewMode = ListView
	rec := &sentKeys{}
	m.newQueue = func() *tmux.Queue { return tmux.NewQueueWith(rec.run) }
	m = driveModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	for range tiles - 1 {
		m = driveModel(t, m, tea.KeyPressMsg{Code: 'v', Mod: tea.ModAlt})
	}
	if m.focus.n != tiles {
		t.Fatalf("wanted %d tiles, have %d (note: %q)", tiles, m.focus.n, m.statusNote)
	}
	return m, rec
}

// TestTileLayoutCoversArea — every layout splits the area exactly: no cell is
// left undrawn and none is drawn twice, which is what lets the frame be joined
// row by row.
func TestTileLayoutCoversArea(t *testing.T) {
	for _, n := range []int{1, 2, 3, 4} {
		for _, sz := range [][3]int{{30, 170, 49}, {0, 90, 30}, {26, 240, 60}, {0, 61, 17}} {
			x, w, h := sz[0], sz[1], sz[2]
			t.Run(fmt.Sprintf("%d/%dx%d", n, w, h), func(t *testing.T) {
				rects := tileLayout(n, x, w, h)
				if len(rects) != n {
					t.Fatalf("%d rects for %d tiles", len(rects), n)
				}
				cover := make([]int, w*h)
				for _, r := range rects {
					for yy := r.y; yy < r.y+r.h; yy++ {
						for xx := r.x; xx < r.x+r.w; xx++ {
							cover[yy*w+(xx-x)]++
						}
					}
				}
				for i, c := range cover {
					if c != 1 {
						t.Fatalf("cell %d,%d covered %d times", i%w, i/w, c)
					}
				}
			})
		}
	}
}

// TestTileLayoutPrefersSideBySide — agents need width, so tiles go side by side
// while there is room, and stack only on a narrow terminal.
func TestTileLayoutPrefersSideBySide(t *testing.T) {
	wide := tileLayout(2, 0, 200, 50)
	if wide[0].y != wide[1].y || wide[0].x == wide[1].x {
		t.Errorf("two tiles on a wide terminal are not side by side: %+v", wide)
	}
	narrow := tileLayout(2, 0, 80, 50)
	if narrow[0].x != narrow[1].x {
		t.Errorf("two tiles on a narrow terminal are not stacked: %+v", narrow)
	}
	grid := tileLayout(4, 0, 200, 50)
	if grid[0].y != grid[1].y || grid[0].x != grid[2].x {
		t.Errorf("four tiles are not a 2x2 grid in reading order: %+v", grid)
	}
}

func TestSplitFrameIsExactlyTerminalSized(t *testing.T) {
	for _, tiles := range []int{2, 3, 4} {
		for _, sz := range [][2]int{{240, 60}, {200, 50}, {160, 44}} {
			w, h := sz[0], sz[1]
			t.Run(fmt.Sprintf("%d/%dx%d", tiles, w, h), func(t *testing.T) {
				m, _ := splitModel(t, w, h, 6, tiles)
				frame, _ := m.view()
				rows := strings.Split(frame, "\n")
				if len(rows) != h {
					t.Fatalf("frame has %d rows, want exactly %d", len(rows), h)
				}
				for i, row := range rows {
					if got := ansi.StringWidth(row); got != w {
						t.Fatalf("row %d is %d cells wide, want exactly %d", i, got, w)
					}
				}
			})
		}
	}
}

// TestSplitAddsNextAgentAndFitsEveryTile — Alt+v shows an agent that is not on
// screen yet, gives it the keyboard, and refits every tile to the new layout.
func TestSplitAddsNextAgentAndFitsEveryTile(t *testing.T) {
	m, rec := splitModel(t, 200, 50, 4, 2)
	if m.focus.active != 1 {
		t.Errorf("the new tile does not have the keyboard (active %d)", m.focus.active)
	}
	if m.focus.tiles[0].key == m.focus.tiles[1].key {
		t.Fatal("the split shows the same agent twice")
	}
	g := m.focusGeometry()
	fits := map[string]string{}
	for _, c := range rec.all(m.focus.q) {
		if len(c) >= 7 && c[0] == "resize-window" && c[1] == "-t" {
			fits[c[2]] = c[4] + "x" + c[6]
		}
	}
	for i := range m.focus.n {
		want := fmt.Sprintf("%dx%d", g.tiles[i].termW(), g.tiles[i].termH())
		if got := fits[m.focus.tiles[i].pane]; got != want {
			t.Errorf("tile %d last fitted to %q, want %q", i, got, want)
		}
	}
}

func TestSplitStopsAtMaxAndWhenEveryAgentIsShown(t *testing.T) {
	m, _ := splitModel(t, 240, 60, 6, maxTiles)
	m = driveModel(t, m, tea.KeyPressMsg{Code: 'v', Mod: tea.ModAlt})
	if m.focus.n != maxTiles || m.statusNote == "" {
		t.Errorf("a fifth split was not refused with a note (n=%d, note=%q)", m.focus.n, m.statusNote)
	}

	few, _ := splitModel(t, 240, 60, 2, 2)
	few = driveModel(t, few, tea.KeyPressMsg{Code: 'v', Mod: tea.ModAlt})
	if few.focus.n != 2 || !strings.Contains(few.statusNote, "already on screen") {
		t.Errorf("splitting with every agent shown: n=%d note=%q", few.focus.n, few.statusNote)
	}
}

// TestSplitRefusedWhenTooSmall — a split that would make tiles unreadable is
// refused with a way forward, rather than drawn.
func TestSplitRefusedWhenTooSmall(t *testing.T) {
	m, _ := splitModel(t, 80, 14, 4, 1)
	m = driveModel(t, m, tea.KeyPressMsg{Code: 'v', Mod: tea.ModAlt})
	if m.focus.n != 1 {
		t.Fatalf("split into %d tiles on an 80x14 terminal", m.focus.n)
	}
	if !strings.Contains(m.statusNote, "no room") {
		t.Errorf("refusal note = %q", m.statusNote)
	}
}

// TestKeysGoToTheActiveTile — typing reaches exactly the tile with the
// keyboard, and moving between tiles moves where it goes.
func TestKeysGoToTheActiveTile(t *testing.T) {
	m, rec := splitModel(t, 200, 50, 4, 2)
	typeString(t, m, "a")
	m = driveModel(t, m, tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt})
	if m.focus.active != 0 {
		t.Fatalf("Alt+Left left the keyboard on tile %d", m.focus.active)
	}
	if s, _ := m.selectedSession(); sessionKey(s) != m.focus.tiles[0].key {
		t.Error("the sidebar selection did not follow the keyboard to tile 0")
	}
	typeString(t, m, "b")

	var got []string
	for _, c := range rec.sends(m.focus.q) {
		got = append(got, c[3]+"="+c[len(c)-1])
	}
	want := []string{m.focus.tiles[1].pane + "=a", m.focus.tiles[0].pane + "=b"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("sent %q, want %q", got, want)
	}
}

func TestCloseTile(t *testing.T) {
	m, rec := splitModel(t, 200, 50, 4, 3)
	closing := m.focus.cur().pane
	m = driveModel(t, m, tea.KeyPressMsg{Code: 'x', Mod: tea.ModAlt})
	if m.focus.n != 2 {
		t.Fatalf("%d tiles after closing one of 3", m.focus.n)
	}
	for i := range m.focus.n {
		if m.focus.tiles[i].pane == closing {
			t.Error("the closed tile is still on screen")
		}
	}
	released := false
	for _, c := range rec.all(m.focus.q) {
		if len(c) > 3 && c[0] == "resize-window" && c[1] == "-A" && c[3] == closing {
			released = true
		}
	}
	if !released {
		t.Error("the closed tile's window was not handed back to tmux")
	}

	m = driveModel(t, m,
		tea.KeyPressMsg{Code: 'x', Mod: tea.ModAlt},
		tea.KeyPressMsg{Code: 'x', Mod: tea.ModAlt})
	if m.focus.on {
		t.Error("closing the last tile did not leave focus mode")
	}
}

// TestEnterFocusOnAgentAlreadyShownActivatesIt — selecting an agent that is
// already in a tile moves the keyboard there instead of showing it twice.
func TestEnterFocusOnAgentAlreadyShownActivatesIt(t *testing.T) {
	m, _ := splitModel(t, 200, 50, 4, 2)
	first := m.focus.tiles[0].key
	s, _ := m.sessionFor(first)
	m, _ = m.enterFocus(s)
	if m.focus.active != 0 || m.focus.n != 2 || m.focus.tiles[1].key == first {
		t.Errorf("active=%d n=%d; want tile 0 activated and nothing duplicated", m.focus.active, m.focus.n)
	}
}

// TestBackgroundTilesPollSlower — the active tile is captured on every poll;
// background tiles only once their cadence is due, so four tiles do not cost
// four times as much.
func TestBackgroundTilesPollSlower(t *testing.T) {
	m, _ := splitModel(t, 240, 60, 6, 3)
	now := time.Now()
	for i := range m.focus.n {
		m.focus.tiles[i].have = true
		m.focus.tiles[i].lastCap = now
	}
	msg := m.focusCapture(m.focus.seq)().(focusSnapMsg)
	if len(msg.snaps) != 1 || msg.snaps[0].pane != m.focus.cur().pane {
		t.Errorf("captured %d panes right after a capture; want only the active tile", len(msg.snaps))
	}
	m.focus.tiles[0].lastCap = now.Add(-2 * focusBackgroundPoll)
	msg = m.focusCapture(m.focus.seq)().(focusSnapMsg)
	if len(msg.snaps) != 2 {
		t.Errorf("captured %d panes; want the active tile plus the one that is due", len(msg.snaps))
	}
}

// TestBackgroundTileExitCloses — an agent exiting in a background tile removes
// just that tile.
func TestBackgroundTileExitCloses(t *testing.T) {
	m, _ := splitModel(t, 200, 50, 4, 2)
	gone := m.focus.tiles[0].pane
	stay := m.focus.tiles[1].pane
	m = driveModel(t, m, focusSnapMsg{seq: m.focus.seq, n: 99, snaps: []tileSnap{{pane: gone, err: tmux.ErrPaneGone}}})
	if !m.focus.on || m.focus.n != 1 || m.focus.cur().pane != stay {
		t.Errorf("on=%v n=%d pane=%q; want the surviving tile alone", m.focus.on, m.focus.n, m.focus.cur().pane)
	}
}

// TestCursorSitsInTheActiveTile — the real cursor is offset by the active
// tile's position, not the tile area's.
func TestCursorSitsInTheActiveTile(t *testing.T) {
	m, _ := splitModel(t, 200, 50, 4, 2)
	g := m.focusGeometry()
	t1 := m.focus.cur()
	t1.have = true
	t1.screen = agentScreen(g.tiles[1].termW(), g.tiles[1].termH())
	c := m.focusCursor()
	r := g.tiles[1]
	if c == nil || c.X != r.x+2+2 || c.Y != r.y+1+5 {
		t.Errorf("cursor %+v, want at %d,%d inside tile 1", c, r.x+4, r.y+6)
	}
}

// TestClickActivatesTile — clicking a tile gives it the keyboard; the wheel
// scrolls whichever tile it is over.
func TestClickActivatesTile(t *testing.T) {
	m, _ := splitModel(t, 200, 50, 4, 2)
	_, hits := m.view()
	r := hits.tiles[0]
	if msg := hits.resolveClick(r.Min.X+5, r.Min.Y+5, m.cursor); msg != (mouseTileMsg{tile: 0}) {
		t.Fatalf("click on tile 0 resolved to %#v", msg)
	}
	m = driveModel(t, m, mouseTileMsg{tile: 0})
	if m.focus.active != 0 {
		t.Error("clicking tile 0 did not activate it")
	}
	m.focus.tiles[1].screen.History = 50
	m = driveModel(t, m, mouseTermScrollMsg{tile: 1, delta: 3})
	if m.focus.tiles[1].scroll != 3 || m.focus.active != 0 {
		t.Errorf("wheel over tile 1: scroll=%d active=%d", m.focus.tiles[1].scroll, m.focus.active)
	}
}

// TestInactiveTileIsQuiet — only the active tile wears the gradient; a
// background tile's frame is the plain border colour.
func TestInactiveTileIsQuiet(t *testing.T) {
	m, _ := splitModel(t, 200, 50, 4, 2)
	g := m.focusGeometry()
	quiet := fgSeq(themeColors().Border)
	active := m.renderTile(1, g.tiles[1])
	inactive := m.renderTile(0, g.tiles[0])
	if strings.Contains(strings.Split(active, "\n")[1], quiet+"│") {
		t.Error("the active tile's frame is the quiet colour")
	}
	if !strings.HasPrefix(strings.Split(inactive, "\n")[1], bgSeq(themeColors().Surface)+quiet+"│") {
		t.Error("the inactive tile's frame is not the quiet colour")
	}
}

func themeColors() theme.Colors { return theme.Current().Colors }

// BenchmarkViewFocusSplit4 is the heaviest focus-mode frame: four live tiles.
func BenchmarkViewFocusSplit4(b *testing.B) {
	t := &testing.T{}
	m, _ := splitModel(t, 240, 60, 6, maxTiles)
	g := m.focusGeometry()
	for i := range m.focus.n {
		m.focus.tiles[i].screen = agentScreen(g.tiles[i].termW(), g.tiles[i].termH())
		m.focus.tiles[i].have = true
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}
