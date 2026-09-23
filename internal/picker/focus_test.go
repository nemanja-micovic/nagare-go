package picker

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nemke/nagare-go/internal/models"
	"github.com/nemke/nagare-go/internal/tmux"
)

// sentKeys records what focus mode would have sent to tmux.
type sentKeys struct {
	mu   sync.Mutex
	cmds [][]string
}

func (s *sentKeys) run(args ...string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cmds = append(s.cmds, args)
	return "", nil
}

// sends returns the send-keys commands, waiting for the queue to drain first.
func (s *sentKeys) sends(q *tmux.Queue) [][]string {
	done := make(chan struct{})
	q.Do(func() { close(done) })
	<-done
	s.mu.Lock()
	defer s.mu.Unlock()
	var out [][]string
	for _, c := range s.cmds {
		if len(c) > 0 && c[0] == "send-keys" {
			out = append(out, c)
		}
	}
	return out
}

// agentScreen is a capture as tmux hands it over: styled runs ending in full
// resets, a background run, a line wider than any panel.
func agentScreen(width, height int) tmux.Screen {
	lines := []string{
		"\x1b[38;5;208m✻\x1b[0m \x1b[1mWelcome to Claude Code!\x1b[0m",
		"",
		"\x1b[48;2;34;68;34m 41 + for attempt := 0; attempt < maxRetries; attempt++ { \x1b[0m",
		strings.Repeat("wide ", 80),
		"\x1b[38;2;150;150;150mRan\x1b[m 2 shell commands",
		"❯ ",
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return tmux.Screen{
		Lines: lines[:height], CursorX: 2, CursorY: 5, CursorVisible: true,
		Width: width, Height: height, Panes: 1,
	}
}

// focusedModel puts a model into focus mode on its first session without a tmux
// server: the queue records instead of sending, and the screen is synthetic.
func focusedModel(t *testing.T, m Model) (Model, *sentKeys) {
	t.Helper()
	if len(m.filtered) == 0 {
		t.Fatal("focusedModel needs at least one session")
	}
	rec := &sentKeys{}
	s := m.filtered[0]
	m.focus.q = tmux.NewQueueWith(rec.run)
	m.focus.captures = new(uint64)
	m.focus.on = true
	m.focus.pane = "%1"
	m.focus.key = sessionKey(s)
	m.focus.name = s.Name
	m.focus.agent = s.AgentType
	g := m.focusGeometry()
	m.focus.fitW, m.focus.fitH = g.termW, g.termH
	m.focus.screen = agentScreen(g.termW, g.termH)
	m.focus.have = true
	return m, rec
}

// TestFocusFrameIsExactlyTerminalSized — the terminal panel is drawn by hand,
// so nothing but this test pins its size. Asserted on the unclamped frame, since
// the clamp in View is what would hide an extra row.
func TestFocusFrameIsExactlyTerminalSized(t *testing.T) {
	sizes := [][2]int{{207, 60}, {200, 50}, {150, 40}, {120, 30}, {90, 24}, {70, 20}, {50, 12}}
	for _, zoom := range []bool{false, true} {
		for _, count := range []int{1, 3, 30} {
			for _, sz := range sizes {
				w, h := sz[0], sz[1]
				t.Run(fmt.Sprintf("zoom=%v/%d/%dx%d", zoom, count, w, h), func(t *testing.T) {
					m, _ := focusedModel(t, gridModel(t, w, h, longSessions(count)))
					m.viewMode = ListView
					m.focus.zoom = zoom
					frame, _ := m.view()
					rows := strings.Split(frame, "\n")
					if len(rows) != h {
						t.Fatalf("frame has %d rows, want exactly %d", len(rows), h)
					}
					for i, row := range rows {
						if got := ansi.StringWidth(row); got != w {
							t.Errorf("row %d is %d cells wide, want exactly %d", i, got, w)
						}
					}
				})
			}
		}
	}
}

// TestFocusPanelMatchesFittedPane — the pane is resized to termW×termH and the
// panel must draw exactly that many rows between its borders, or the agent's
// bottom row (its prompt) is lost or a gap opens under it.
func TestFocusPanelMatchesFittedPane(t *testing.T) {
	m, _ := focusedModel(t, newVisualModel(t, 140, 36))
	g := m.focusGeometry()
	panel := strings.Split(m.renderTermPanel(g), "\n")
	if len(panel) != g.termH+2 {
		t.Fatalf("panel has %d rows, want termH+2 = %d", len(panel), g.termH+2)
	}
	if !strings.HasPrefix(ansi.Strip(panel[0]), "╭") || !strings.HasPrefix(ansi.Strip(panel[len(panel)-1]), "╰") {
		t.Error("panel is not a closed box")
	}
	// The prompt row of the capture lands on panel row 1+5, one column in.
	row := ansi.Strip(panel[1+5])
	if !strings.HasPrefix(row, "│ ❯") {
		t.Errorf("capture row 5 drawn as %q, want it inside the frame after one column of padding", row)
	}
}

func TestFocusNoDefaultBackgroundCells(t *testing.T) {
	for _, zoom := range []bool{false, true} {
		m, _ := focusedModel(t, newVisualModel(t, 120, 30))
		m.focus.zoom = zoom
		frame, _ := m.view()
		for row, line := range strings.Split(frame, "\n") {
			for col, bg := range cellBackgrounds(line) {
				if bg == "default" {
					t.Fatalf("zoom=%v: row %d col %d sits on the terminal's default background", zoom, row, col)
				}
			}
		}
	}
}

// TestFocusCursorTracksAgent — the real cursor goes where the agent's is, offset
// by the sidebar, the border and the padding; and disappears when it should.
func TestFocusCursorTracksAgent(t *testing.T) {
	m, _ := focusedModel(t, newVisualModel(t, 140, 36))
	g := m.focusGeometry()
	c := m.focusCursor()
	if c == nil {
		t.Fatal("no cursor for a visible agent cursor")
	}
	if c.X != g.panelX+2+2 || c.Y != 1+5 {
		t.Errorf("cursor at %d,%d, want %d,%d", c.X, c.Y, g.panelX+4, 6)
	}

	scrolled := m
	scrolled.focus.scroll = 3
	if scrolled.focusCursor() != nil {
		t.Error("cursor shown while scrolled back into history")
	}
	hidden := m
	hidden.focus.screen.CursorVisible = false
	if hidden.focusCursor() != nil {
		t.Error("cursor shown although the agent hides it")
	}
	help := m
	help.showHelp = true
	if help.focusCursor() != nil {
		t.Error("cursor shown over the help overlay")
	}
}

// TestFocusForwardsKeys — typing reaches the pane in order, as text or as named
// keys, and Esc is the agent's (it interrupts Claude), not nagare's quit.
func TestFocusForwardsKeys(t *testing.T) {
	m, rec := focusedModel(t, newVisualModel(t, 140, 36))
	m = typeString(t, m, "hi")
	m = driveModel(t, m,
		tea.KeyPressMsg{Code: tea.KeyEnter},
		tea.KeyPressMsg{Code: tea.KeyEscape},
		tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl},
	)
	if !m.focus.on {
		t.Fatal("a forwarded key left focus mode")
	}

	var got []string
	for _, c := range rec.sends(m.focus.q) {
		// Everything after the target: "-- text" for typed text, or the key.
		for i := range c {
			if c[i] == "-t" {
				got = append(got, strings.Join(c[i+2:], " "))
				break
			}
		}
	}
	// "h" and "i" may or may not coalesce depending on timing; flatten text.
	joined := strings.Join(got, "|")
	joined = strings.ReplaceAll(joined, "-- h|-- i", "-- hi")
	want := "-- hi|Enter|Escape|C-c"
	if joined != want {
		t.Errorf("sent %q, want %q", joined, want)
	}
}

func TestFocusReservedKeys(t *testing.T) {
	m, rec := focusedModel(t, newVisualModel(t, 140, 36))

	zoomed := driveModel(t, m, tea.KeyPressMsg{Code: 'z', Mod: tea.ModAlt})
	if !zoomed.focus.zoom {
		t.Error("Alt+z did not zoom")
	}

	next := driveModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModAlt})
	if next.focus.key == m.focus.key || next.cursor != 1 {
		t.Errorf("Alt+Down did not move focus to the next agent (cursor %d)", next.cursor)
	}

	left := driveModel(t, m, tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	if left.focus.on {
		t.Error("Ctrl+] did not leave focus mode")
	}

	if sends := rec.sends(m.focus.q); len(sends) != 0 {
		t.Errorf("reserved keys reached the agent: %q", sends)
	}
}

// TestFocusNextWaiting — F4 means the same thing in focus mode as in the list:
// go to the next agent that is blocked on the user.
func TestFocusNextWaiting(t *testing.T) {
	m, _ := focusedModel(t, newVisualModel(t, 140, 36))
	// newVisualModel's waiting session sorts first; focus the other one.
	idle := 1 - m.cursor
	if m.filtered[idle].Status == models.StatusWaitingInput {
		t.Fatal("fixture changed: expected one waiting session and one not")
	}
	m.cursor = idle
	m.focus.key = sessionKey(m.filtered[idle])
	m = driveModel(t, m, tea.KeyPressMsg{Code: tea.KeyF4})
	if s, _ := m.selectedSession(); s.Status != models.StatusWaitingInput || m.focus.key != sessionKey(s) {
		t.Errorf("F4 focused %q (%s), want the waiting session", s.Name, s.Status)
	}
}

func TestFocusPasteIsOnePaste(t *testing.T) {
	m, rec := focusedModel(t, newVisualModel(t, 140, 36))
	m = driveModel(t, m, tea.PasteMsg{Content: "line one\nline two"})
	done := make(chan struct{})
	m.focus.q.Do(func() { close(done) })
	<-done
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.cmds) != 1 || rec.cmds[0][0] != "set-buffer" {
		t.Fatalf("paste sent %q, want a single set-buffer/paste-buffer", rec.cmds)
	}
}

// TestFocusScrollClampsToHistory — the view cannot scroll past what tmux keeps,
// and typing returns to the live screen.
func TestFocusScrollClampsToHistory(t *testing.T) {
	m, _ := focusedModel(t, newVisualModel(t, 140, 36))
	m.focus.screen.History = 10
	m = driveModel(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModShift})
	m = driveModel(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModShift})
	if m.focus.scroll != 10 {
		t.Errorf("scroll = %d, want clamped to history (10)", m.focus.scroll)
	}
	m = typeString(t, m, "x")
	if m.focus.scroll != 0 {
		t.Errorf("typing left the view scrolled back by %d", m.focus.scroll)
	}
}

// TestFocusStaleCaptureIgnored — captures run on a worker and reply through the
// event loop; a late reply must not rewind the screen to an older frame.
func TestFocusStaleCaptureIgnored(t *testing.T) {
	m, _ := focusedModel(t, newVisualModel(t, 140, 36))
	newer := agentScreen(10, 3)
	newer.Lines[0] = "newer"
	older := agentScreen(10, 3)
	older.Lines[0] = "older"
	m = driveModel(t, m,
		focusSnapMsg{seq: -1, n: 2, pane: "%1", screen: newer},
		focusSnapMsg{seq: -1, n: 1, pane: "%1", screen: older},
	)
	if m.focus.screen.Lines[0] != "newer" {
		t.Errorf("screen shows %q after a stale capture, want %q", m.focus.screen.Lines[0], "newer")
	}
}

// TestFocusExitWhenPaneGone — an agent that exits takes its pane with it; focus
// mode returns to the list and says why rather than showing a dead frame.
func TestFocusExitWhenPaneGone(t *testing.T) {
	m, _ := focusedModel(t, newVisualModel(t, 140, 36))
	name := m.focus.name
	m = driveModel(t, m, focusSnapMsg{seq: m.focus.seq, n: 1, pane: "%1", err: tmux.ErrPaneGone})
	if m.focus.on {
		t.Fatal("still focused on a pane that is gone")
	}
	if !strings.Contains(m.statusNote, name) {
		t.Errorf("status note %q does not name the exited agent", m.statusNote)
	}
}

// TestFocusFooterNeverOffersEscAsQuit — Esc goes to the agent in focus mode, so
// the footer advertising "Esc Quit" there would send users the wrong way.
func TestFocusFooterNeverOffersEscAsQuit(t *testing.T) {
	m, _ := focusedModel(t, newVisualModel(t, 200, 36))
	bar := ansi.Strip(helpBar(m, 200))
	if strings.Contains(bar, "Esc") {
		t.Errorf("focus footer mentions Esc: %q", bar)
	}
	if !strings.Contains(bar, "^]") {
		t.Errorf("focus footer does not show the way back: %q", bar)
	}
}

func TestVisibleRowsKeepsTheBottom(t *testing.T) {
	rows, off := visibleRows([]string{"a", "b", "c", "d"}, 2)
	if !reflect.DeepEqual(rows, []string{"c", "d"}) || off != 2 {
		t.Errorf("visibleRows = %q, %d", rows, off)
	}
	rows, off = visibleRows([]string{"a"}, 3)
	if !reflect.DeepEqual(rows, []string{"a"}) || off != 0 {
		t.Errorf("visibleRows = %q, %d", rows, off)
	}
}

func BenchmarkViewFocus30(b *testing.B) {
	m := benchModel(30, 200, 50)
	rec := &sentKeys{}
	m.focus.q = tmux.NewQueueWith(rec.run)
	m.focus.on = true
	m.focus.key = sessionKey(m.filtered[0])
	m.focus.name = m.filtered[0].Name
	g := m.focusGeometry()
	m.focus.screen = agentScreen(g.termW, g.termH)
	m.focus.have = true
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}

// BenchmarkTermPanel is the part that repaints at the capture rate.
func BenchmarkTermPanel(b *testing.B) {
	m := benchModel(30, 200, 50)
	m.focus.on = true
	m.focus.name = "bench"
	g := m.focusGeometry()
	m.focus.screen = agentScreen(g.termW, g.termH)
	m.focus.have = true
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.renderTermPanel(g)
	}
}

// TestFocusSidebarCacheIsInvisible — the cached sidebar must be the sidebar: the
// same bytes as a fresh render, and never stale after something it shows changed.
func TestFocusSidebarCacheIsInvisible(t *testing.T) {
	m, _ := focusedModel(t, newVisualModel(t, 140, 36))
	g := m.focusGeometry()
	fresh := func(m Model) string {
		out, _ := m.viewList(g.sidebarW, g.height, true)
		return out
	}
	cached := func(m Model) string {
		out, _ := m.cachedSidebar(g)
		return out
	}

	if cached(m) != fresh(m) {
		t.Fatal("cached sidebar differs from a fresh render")
	}

	changes := map[string]func(Model) Model{
		"status change": func(m Model) Model {
			sessions := append([]models.Session(nil), m.sessions...)
			for i := range sessions {
				sessions[i].Status = models.StatusRunning
			}
			return driveModel(t, m, SessionsUpdatedMsg(sessions))
		},
		"breath": func(m Model) Model { m.breath = 0.5; return m },
		"cursor": func(m Model) Model { m.cursor = 1 - m.cursor; return m },
		"note":   func(m Model) Model { m.statusNote = "agent exited"; return m },
		"flash": func(m Model) Model {
			m.flashes[sessionKey(m.filtered[0])] = flashState{kind: flashAttention, level: 1}
			return m
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			base, _ := focusedModel(t, newVisualModel(t, 140, 36))
			_ = cached(base) // warm the cache
			changed := change(base)
			if got, want := cached(changed), fresh(changed); got != want {
				t.Errorf("sidebar is stale after a %s", name)
			}
		})
	}
}

// recordingModel returns a test model whose focus-mode tmux commands are
// recorded rather than sent.
func recordingModel(t *testing.T, w, h int) (Model, *sentKeys) {
	t.Helper()
	m := newVisualModel(t, w, h)
	rec := &sentKeys{}
	m.newQueue = func() *tmux.Queue { return tmux.NewQueueWith(rec.run) }
	return m, rec
}

func (s *sentKeys) all(q *tmux.Queue) [][]string {
	done := make(chan struct{})
	q.Do(func() { close(done) })
	<-done
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]string(nil), s.cmds...)
}

// TestEnterOpensFocus — Enter keeps the user in nagare: the selected agent's
// window is fitted to exactly the panel it will be drawn in.
func TestEnterOpensFocus(t *testing.T) {
	m, rec := recordingModel(t, 140, 36)
	m = driveModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.focus.on {
		t.Fatal("Enter did not open focus mode")
	}
	g := m.focusGeometry()
	want := []string{"resize-window", "-t", m.focus.pane, "-x", fmt.Sprint(g.termW), "-y", fmt.Sprint(g.termH)}
	found := false
	for _, c := range rec.all(m.focus.q) {
		if len(c) >= len(want) && reflect.DeepEqual(c[:len(want)], want) {
			found = true
		}
	}
	if !found {
		t.Errorf("no fit to %dx%d among %q", g.termW, g.termH, rec.all(m.focus.q))
	}

	// Leaving hands the window back to tmux.
	m = driveModel(t, m, tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	released := false
	for _, c := range rec.all(m.focus.q) {
		if len(c) > 1 && c[0] == "resize-window" && c[1] == "-A" {
			released = true
		}
	}
	if !released {
		t.Error("leaving focus mode did not release the window")
	}
}

// TestFocusRefitsOnResize — the agent is resized along with nagare.
func TestFocusRefitsOnResize(t *testing.T) {
	m, _ := recordingModel(t, 140, 36)
	m = driveModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = driveModel(t, m, tea.WindowSizeMsg{Width: 180, Height: 44})
	g := m.focusGeometry()
	if m.focus.fitW != g.termW || m.focus.fitH != g.termH {
		t.Errorf("fitted to %dx%d after resize, want %dx%d", m.focus.fitW, m.focus.fitH, g.termW, g.termH)
	}
}

// TestFocusWhenReady — a session created from the new-session form opens in
// focus mode as soon as a scan finds its agent, instead of the form switching
// the terminal away from nagare.
func TestFocusWhenReady(t *testing.T) {
	m, _ := recordingModel(t, 140, 36)
	m = m.FocusWhenReady("fresh")
	if m.focus.on {
		t.Fatal("focused before the session existed")
	}
	sessions := append([]models.Session(nil), m.sessions...)
	sessions = append(sessions, models.Session{
		Name: "fresh", SessionName: "fresh", Path: "/tmp/fresh", PaneID: "%9",
		Status: models.StatusIdle, AgentType: models.AgentCodex,
	})
	m = driveModel(t, m, SessionsUpdatedMsg(sessions))
	if !m.focus.on || m.focus.pane != "%9" {
		t.Fatalf("did not focus the new session (on=%v pane=%q)", m.focus.on, m.focus.pane)
	}
	if s, _ := m.selectedSession(); s.Name != "fresh" {
		t.Errorf("sidebar selection is %q, want the focused session", s.Name)
	}
}

// TestFocusSelectionFollowsResort — status changes re-sort the list; the sidebar
// must keep highlighting the focused agent, not whatever row it used to be on.
func TestFocusSelectionFollowsResort(t *testing.T) {
	m, _ := recordingModel(t, 140, 36)
	m = driveModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	focused := m.focus.key
	sessions := append([]models.Session(nil), m.sessions...)
	for i := range sessions {
		if sessionKey(sessions[i]) == focused {
			sessions[i].Status = models.StatusIdle
		} else {
			sessions[i].Status = models.StatusWaitingInput
		}
	}
	m = driveModel(t, m, SessionsUpdatedMsg(sessions))
	if s, _ := m.selectedSession(); sessionKey(s) != focused {
		t.Errorf("selection moved to %q after a re-sort; want the focused agent", s.Name)
	}
}

func TestKeyLabel(t *testing.T) {
	cases := []struct {
		key         string
		short, long string
	}{
		{"ctrl+]", "^]", "Ctrl+]"},
		{"ctrl+q", "^q", "Ctrl+q"},
		{"alt+q", "Alt+q", "Alt+q"},
		{"f12", "F12", "F12"},
		{"ctrl+alt+b", "Ctrl+Alt+b", "Ctrl+Alt+b"},
	}
	for _, tc := range cases {
		if got := keyLabel(tc.key, true); got != tc.short {
			t.Errorf("keyLabel(%q, short) = %q, want %q", tc.key, got, tc.short)
		}
		if got := keyLabel(tc.key, false); got != tc.long {
			t.Errorf("keyLabel(%q, long) = %q, want %q", tc.key, got, tc.long)
		}
	}
}

// TestFocusLeaveKeyIsConfigurable — on layouts where Ctrl+] is hard to reach,
// the configured key leaves focus mode, is what the footer shows, and Ctrl+]
// goes to the agent like any other key.
func TestFocusLeaveKeyIsConfigurable(t *testing.T) {
	m, rec := focusedModel(t, newVisualModel(t, 200, 36))
	m.leaveKey = "ctrl+q"

	if bar := ansi.Strip(helpBar(m, 200)); !strings.Contains(bar, "^q Sessions") {
		t.Errorf("footer does not show the configured key: %q", bar)
	}
	if help := ansi.Strip(helpOverlayFor(200, 60, keyLabel(m.leaveKey, false))); !strings.Contains(help, "Ctrl+q") {
		t.Error("help screen does not show the configured key")
	}

	m = driveModel(t, m, tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	if !m.focus.on {
		t.Fatal("Ctrl+] left focus mode although another key is configured")
	}
	if sends := rec.sends(m.focus.q); len(sends) != 1 {
		t.Errorf("Ctrl+] was not forwarded to the agent: %q", sends)
	}
	m = driveModel(t, m, tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if m.focus.on {
		t.Error("the configured key did not leave focus mode")
	}
}
