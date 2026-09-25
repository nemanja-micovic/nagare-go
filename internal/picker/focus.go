package picker

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/nemke/nagare-go/internal/log"
	"github.com/nemke/nagare-go/internal/models"
	"github.com/nemke/nagare-go/internal/session"
	"github.com/nemke/nagare-go/internal/tmux"
)

// Focus mode hosts agents' live terminals inside nagare.
//
// The picker used to end every interaction by handing the user to tmux: pick a
// session, get switched away, come back with a keybinding to pick the next one.
// Focus mode keeps them here instead. The selected agent's pane is drawn in
// place of the preview, keystrokes are forwarded to it, and the list stays
// alongside as a sidebar — so the moment another agent needs something, it is
// one keypress away rather than a trip back through the picker.
//
// Up to four agents can be on screen at once, as tiles. One tile is active: it
// takes the keyboard and wears the gradient frame. The rest stay live, polled
// less often, so a glance shows what each agent is doing without switching.
//
// tmux remains the backend and does all the terminal work. nagare fits each
// pane's window to the tile it is shown in, polls tmux for the rendered screen,
// and sends keys back through send-keys. Agents keep running when nagare exits,
// exactly as before, and nagare no longer needs to run inside tmux at all.

// Polling bounds. A changing screen is re-captured at ~30fps so streaming output
// reads as streaming; a still one backs off to 4fps, so an idle agent costs
// almost nothing. A keystroke snaps straight back to the fast end. Background
// tiles are captured at most every focusBackgroundPoll whatever the active one
// is doing: they are for glancing at, and four tiles at 30fps would be 120 tmux
// calls a second.
const (
	focusFastPoll       = 33 * time.Millisecond
	focusSlowPoll       = 250 * time.Millisecond
	focusKeyPoll        = 12 * time.Millisecond
	focusBackgroundPoll = 300 * time.Millisecond
	// focusRefitEvery rate-limits correcting a window someone else resized, so
	// two nagare instances focused on one agent cannot fight every frame.
	focusRefitEvery = time.Second
	// focusSidebarMin/Max bound the sidebar. It shows names and status, not
	// detail, so it gets no more than it needs to leave the agents room.
	focusSidebarMin = 26
	focusSidebarMax = 36
	// maxTiles is how many agents fit on screen at once. Past four, tiles are
	// too small to read, and the sidebar already shows the rest.
	maxTiles = 4
	// minTileTermW/H are the smallest usable agent screen. A split that would
	// go below them is refused rather than drawn unreadably.
	minTileTermW = 30
	minTileTermH = 5
)

// paneView is one tile: a pane shown in focus mode, and everything nagare knows
// about how it is shown.
type paneView struct {
	pane   string // tmux target: the pane id, stable across window moves
	key    string // sessionKey of the agent the tile belongs to
	name   string // display name, for messages once the session is gone
	agent  models.AgentType
	shell  bool // showing the agent's companion shell rather than the agent
	screen tmux.Screen
	have   bool // screen holds at least one capture

	fitW, fitH int // size the window was fitted to; 0 when not fitted
	zoomedByUs bool
	lastFit    time.Time
	lastCap    time.Time // when this tile was last captured

	scroll int // rows scrolled back into history; 0 is live
}

// focusState is focus mode. Tiles live in a fixed array, not a slice: the Model
// is copied by value on every update, and a shared backing array would let one
// copy's edits leak into another.
type focusState struct {
	on     bool
	tiles  [maxTiles]paneView
	n      int // tiles in use, in layout order
	active int // the tile with the keyboard
	zoom   bool

	seq     int           // poll-loop token; bumping it retires the running loop
	delay   time.Duration // next poll interval
	applied uint64        // newest capture applied, so late replies cannot rewind

	q        *tmux.Queue // shared by every copy of the Model
	captures *uint64     // capture counter, only touched on q's worker
}

// cur is the active tile.
func (f *focusState) cur() *paneView { return &f.tiles[f.active] }

// tileOf returns the tile showing the agent with the given session key.
func (f *focusState) tileOf(key string) int {
	for i := range f.n {
		if f.tiles[i].key == key {
			return i
		}
	}
	return -1
}

// onScreen reports whether an agent is shown in any tile.
func (f *focusState) onScreen(key string) bool { return f.on && f.tileOf(key) >= 0 }

// Messages driving focus mode.
type (
	focusTickMsg struct{ seq int }
	focusSnapMsg struct {
		seq   int
		n     uint64
		snaps []tileSnap
	}
	// attachDoneMsg arrives when a full tmux attach started from focus mode
	// returns — the user detached, and is back in nagare.
	attachDoneMsg struct{ err error }
)

// tileSnap is one pane's capture within a poll.
type tileSnap struct {
	pane   string
	screen tmux.Screen
	err    error
}

// tileRect is a tile's outer rectangle within the frame.
type tileRect struct{ x, y, w, h int }

// termW and termH are the pane inside the tile: a border on every side, plus a
// column of padding left and right so output does not sit flush on the frame.
func (r tileRect) termW() int { return max(r.w-4, 1) }
func (r tileRect) termH() int { return max(r.h-2, 1) }

// focusGeometry is the single source of truth for where focus mode puts things.
// View draws from it and windows are fitted from it, so every pane is exactly
// the size of the hole it is drawn into.
type focusGeometry struct {
	sidebarW     int        // outer width of the sidebar, 0 when zoomed
	panelX       int        // left edge of the tile area
	panelW       int        // width of the tile area
	height       int        // outer height of the sidebar and the tile area
	tiles        []tileRect // one per tile, in layout order
	termW, termH int        // the active tile's pane
}

func (m Model) focusGeometry() focusGeometry {
	return m.geometryFor(m.focus.n)
}

// geometryFor lays out n tiles; asking for n+1 is how a split is checked for
// room before it is made.
func (m Model) geometryFor(n int) focusGeometry {
	height := m.height
	if m.showHelpBar {
		height-- // the hint bar is pinned to exactly one row
	}
	g := focusGeometry{height: max(height, 3)}
	if !m.focus.zoom {
		g.sidebarW = min(max(m.width/5, focusSidebarMin), focusSidebarMax)
		// A terminal too narrow for both gives the agents everything.
		if m.width-g.sidebarW < 40 {
			g.sidebarW = 0
		}
	}
	g.panelX = g.sidebarW
	g.panelW = m.width - g.sidebarW
	g.tiles = tileLayout(max(n, 1), g.panelX, g.panelW, g.height)
	active := min(m.focus.active, len(g.tiles)-1)
	g.termW, g.termH = g.tiles[active].termW(), g.tiles[active].termH()
	return g
}

// tileLayout splits the area into n tiles. Side by side is preferred while each
// column keeps room for a real agent screen; below that tiles stack, because a
// coding agent needs width more than it needs height.
func tileLayout(n, x, w, h int) []tileRect {
	wide := w >= 2*(minTileTermW+4)+20
	half := w / 2
	col := func(x, w, count int) []tileRect {
		out := make([]tileRect, count)
		y := 0
		for i := range count {
			th := h / count
			if i == count-1 {
				th = h - y
			}
			out[i] = tileRect{x, y, w, th}
			y += th
		}
		return out
	}
	switch {
	case n <= 1:
		return []tileRect{{x, 0, w, h}}
	case !wide:
		return col(x, w, n)
	case n == 2:
		return []tileRect{{x, 0, half, h}, {x + half, 0, w - half, h}}
	case n == 3:
		return append([]tileRect{{x, 0, half, h}}, col(x+half, w-half, 2)...)
	default:
		left := col(x, half, 2)
		right := col(x+half, w-half, 2)
		return []tileRect{left[0], right[0], left[1], right[1]}
	}
}

// fits reports whether every tile of g is big enough to use.
func (g focusGeometry) fits() bool {
	for _, r := range g.tiles {
		if r.termW() < minTileTermW || r.termH() < minTileTermH {
			return false
		}
	}
	return true
}

// enterFocus shows s in the active tile, opening focus mode if it is not open.
// An agent already on screen is activated rather than shown twice. A saved
// session is started first and focused once its pane shows up in a scan.
func (m Model) enterFocus(s models.Session) (Model, tea.Cmd) {
	if s.Status == models.StatusSaved {
		agent := string(s.AgentType)
		if agent == "" || agent == "unknown" {
			agent = "claude"
		}
		name, err := session.Load(s.Path, s.Name, agent)
		if err != nil {
			log.Error("load session: %v", err)
			m.statusErr = err.Error()
			return m, nil
		}
		m.awaitFocus(name)
		return m, doScan(m.statesDir)
	}

	if m.focus.on {
		if i := m.focus.tileOf(sessionKey(s)); i >= 0 {
			if i == m.focus.active && !m.focus.cur().shell {
				return m, nil
			}
			m.focus.active = i
			if m.focus.cur().shell {
				// Selecting an agent means the agent, not its shell.
				return m.showInTile(s, agentTarget(s), false)
			}
			m.selectKey(sessionKey(s))
			return m, m.focusPoll(0)
		}
	}
	return m.showInTile(s, agentTarget(s), false)
}

// agentTarget is the tmux target for a session's agent pane.
func agentTarget(s models.Session) string {
	if s.PaneID != "" {
		return s.PaneID
	}
	return tmux.PaneTarget(s.SessionName, s.WindowIndex, s.PaneIndex)
}

// startFocus opens focus mode with no tiles, ready for the first.
func (m *Model) startFocus() {
	if m.focus.q == nil {
		m.focus.q = m.newQueue()
		m.focus.captures = new(uint64)
	}
	m.focus.on = true
	m.focus.n = 1
	m.focus.active = 0
	m.focus.tiles[0] = paneView{}
	m.focus.delay = focusFastPoll

	// The sidebar lists every agent, not whatever the search last narrowed to;
	// a stale filter would hide the sessions the user came here to keep an eye on.
	if m.searchInput.Value() != "" {
		m.searchInput.SetValue("")
		m.applyFilter()
	}
}

// showInTile puts target — an agent's pane, or its companion shell — into the
// active tile on behalf of session s, replacing whatever the tile showed.
func (m Model) showInTile(s models.Session, target string, shell bool) (Model, tea.Cmd) {
	if !m.focus.on {
		m.startFocus()
	} else {
		m.releaseTile(m.focus.active)
	}
	m.focus.tiles[m.focus.active] = paneView{
		pane:  target,
		key:   sessionKey(s),
		name:  s.Name,
		agent: s.AgentType,
		shell: shell,
	}
	m.focus.delay = focusFastPoll
	m.selectKey(sessionKey(s))
	log.Info("focus %s (%s) in tile %d", s.Name, target, m.focus.active)
	m.fitFocus(true)
	return m, m.focusPoll(0)
}

// addTile splits the screen to show another agent beside the ones already on
// it: the next one in the list that is not on screen yet.
func (m Model) addTile() (Model, tea.Cmd) {
	f := &m.focus
	if f.n >= maxTiles {
		m.statusNote = fmt.Sprintf("%d agents is the most that fit side by side", maxTiles)
		return m, nil
	}
	if !m.geometryFor(f.n + 1).fits() {
		m.statusNote = "no room for another split — widen the terminal or hide the sidebar (Alt+z)"
		return m, nil
	}
	next := -1
	for step := 1; step <= len(m.filtered); step++ {
		s := m.filtered[(m.cursor+step)%len(m.filtered)]
		if s.Status != models.StatusSaved && !f.onScreen(sessionKey(s)) {
			next = (m.cursor + step) % len(m.filtered)
			break
		}
	}
	if next < 0 {
		m.statusNote = "every agent is already on screen"
		return m, nil
	}
	s := m.filtered[next]
	f.tiles[f.n] = paneView{}
	f.active = f.n
	f.n++
	// Every tile's rectangle changes with the layout, so all of them refit.
	m, cmd := m.showInTile(s, agentTarget(s), false)
	return m, cmd
}

// closeTile removes the active tile. Closing the last one leaves focus mode.
func (m Model) closeTile() (Model, tea.Cmd) {
	f := &m.focus
	if f.n <= 1 {
		return m.leaveFocus(), m.doPreview()
	}
	m.releaseTile(f.active)
	copy(f.tiles[f.active:], f.tiles[f.active+1:f.n])
	f.n--
	f.tiles[f.n] = paneView{}
	f.active = min(f.active, f.n-1)
	m.selectKey(f.cur().key)
	m.fitFocus(true)
	return m, m.focusPoll(0)
}

// cycleTile moves the keyboard to the next (or previous) tile.
func (m Model) cycleTile(step int) (Model, tea.Cmd) {
	f := &m.focus
	if f.n <= 1 {
		return m, nil
	}
	f.active = (f.active + step + f.n) % f.n
	m.selectKey(f.cur().key)
	return m, m.focusPoll(0)
}

// activateTile gives the keyboard to tile i.
func (m Model) activateTile(i int) (Model, tea.Cmd) {
	if i < 0 || i >= m.focus.n || i == m.focus.active {
		return m, nil
	}
	m.focus.active = i
	m.selectKey(m.focus.cur().key)
	return m, m.focusPoll(0)
}

// awaitFocus arranges for the tmux session name to be focused as soon as a scan
// finds an agent in it — for sessions nagare has just started.
func (m *Model) awaitFocus(sessionName string) {
	m.pendingFocus = sessionName
	m.pendingFocusBy = time.Now().Add(worktreeWait)
	m.statusNote = fmt.Sprintf("starting %s…", sessionName)
}

// resolvePendingFocus focuses a session started from nagare once it is live.
func (m Model) resolvePendingFocus() (Model, tea.Cmd) {
	if m.pendingFocus == "" {
		return m, nil
	}
	if time.Now().After(m.pendingFocusBy) {
		m.statusErr = fmt.Sprintf("%s started, but no agent appeared in it", m.pendingFocus)
		m.pendingFocus = ""
		return m, nil
	}
	for _, s := range m.sessions {
		if s.SessionName == m.pendingFocus && s.Status != models.StatusSaved {
			m.pendingFocus = ""
			m.statusNote = ""
			return m.enterFocus(s)
		}
	}
	return m, nil
}

// selectKey moves the cursor onto the session with the given key, if listed.
func (m *Model) selectKey(key string) {
	if i := slices.IndexFunc(m.filtered, func(s models.Session) bool {
		return sessionKey(s) == key
	}); i >= 0 {
		m.cursor = i
	}
}

// leaveFocus returns to the list, handing every tile's window back to tmux.
func (m Model) leaveFocus() Model {
	m.releaseFocus()
	return m
}

// releaseTile un-fits one tile's window.
func (m *Model) releaseTile(i int) {
	t := &m.focus.tiles[i]
	if t.fitW > 0 {
		m.focus.q.Release(t.pane, t.zoomedByUs)
	}
	t.fitW, t.fitH, t.zoomedByUs = 0, 0, false
}

// releaseFocus un-fits every window and stops the poll loop. Safe to call when
// nothing is focused.
func (m *Model) releaseFocus() {
	f := &m.focus
	if !f.on {
		return
	}
	for i := range f.n {
		m.releaseTile(i)
	}
	log.Info("unfocus (%d tiles)", f.n)
	f.on = false
	f.n = 0
	f.active = 0
	f.seq++
}

// Close releases anything focus mode holds, and waits until tmux has it back.
// Called once the program has exited, so a window is never left pinned to
// nagare's size.
func (m Model) Close() {
	if m.focus.q == nil {
		return
	}
	m.releaseFocus()
	m.focus.sync(func() {})
}

// sync runs fn on the tmux queue and waits for it — and so for everything queued
// before it. Bounded, so a wedged tmux cannot hang nagare on the way out.
func (f focusState) sync(fn func()) {
	done := make(chan struct{})
	f.q.Do(func() { fn(); close(done) })
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}

// fitFocus sizes every tile's window to its tile. force skips the check against
// the last fit, for a fresh tile or a new layout.
func (m *Model) fitFocus(force bool) {
	f := &m.focus
	if !f.on || m.width == 0 {
		return
	}
	g := m.focusGeometry()
	for i := range f.n {
		t := &f.tiles[i]
		r := g.tiles[i]
		if t.pane == "" || (!force && t.fitW == r.termW() && t.fitH == r.termH()) {
			continue
		}
		t.fitW, t.fitH = r.termW(), r.termH()
		t.lastFit = time.Now()
		// Zoom only once, and only when the pane shares its window; otherwise
		// the pane would get a share of the window rather than all of it.
		zoom := !t.zoomedByUs && t.have && t.screen.Panes > 1 && !t.screen.Zoomed
		if zoom {
			t.zoomedByUs = true
		}
		f.q.Fit(t.pane, t.fitW, t.fitH, zoom)
	}
}

// focusPoll schedules the next capture after d, retiring any loop already
// running. Captures go through the key queue, so they always see every
// keystroke sent before them.
func (m *Model) focusPoll(d time.Duration) tea.Cmd {
	m.focus.seq++
	seq := m.focus.seq
	if d <= 0 {
		return m.focusCapture(seq)
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return focusTickMsg{seq: seq} })
}

// focusCapture captures the active tile, and any background tile that is due.
func (m Model) focusCapture(seq int) tea.Cmd {
	f := m.focus
	type want struct {
		pane           string
		scroll, height int
	}
	var wants []want
	now := time.Now()
	for i := range f.n {
		t := f.tiles[i]
		if t.pane == "" {
			continue
		}
		if i == f.active || !t.have || now.Sub(t.lastCap) >= focusBackgroundPoll {
			wants = append(wants, want{t.pane, t.scroll, t.screen.Height})
		}
	}
	q, counter := f.q, f.captures
	return func() tea.Msg {
		var n uint64
		// Numbered on the worker, in the order the captures actually ran.
		q.Do(func() { *counter++; n = *counter })
		snaps := make([]tileSnap, len(wants))
		for i, w := range wants {
			sc, err := q.Capture(w.pane, w.scroll, w.height)
			snaps[i] = tileSnap{pane: w.pane, screen: sc, err: err}
		}
		return focusSnapMsg{seq: seq, n: n, snaps: snaps}
	}
}

// updateFocus handles the focus-mode messages.
func (m Model) updateFocus(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case focusTickMsg:
		if !m.focus.on || msg.seq != m.focus.seq {
			return m, nil
		}
		return m, m.focusCapture(msg.seq)

	case focusSnapMsg:
		f := &m.focus
		if !f.on || msg.n <= f.applied {
			return m, nil
		}
		f.applied = msg.n
		changed := false
		var cmds []tea.Cmd
		for _, sn := range msg.snaps {
			i := -1
			for j := range f.n {
				if f.tiles[j].pane == sn.pane {
					i = j
				}
			}
			if i < 0 {
				continue // the tile was closed or repointed since
			}
			if sn.err != nil {
				var cmd tea.Cmd
				m, cmd = m.tileGone(i)
				cmds = append(cmds, cmd)
				if !m.focus.on {
					return m, tea.Batch(cmds...)
				}
				changed = true
				continue
			}
			if m.applySnap(i, sn.screen) && i == f.active {
				changed = true
			}
		}

		if msg.seq != f.seq {
			return m, tea.Batch(cmds...) // a newer loop owns the schedule
		}
		if changed {
			f.delay = focusFastPoll
		} else {
			f.delay = min(f.delay*2, focusSlowPoll)
		}
		// With background tiles open, never sleep past their cadence.
		if f.n > 1 {
			f.delay = min(f.delay, focusBackgroundPoll)
		}
		return m, tea.Batch(append(cmds, m.focusPoll(f.delay))...)

	case attachDoneMsg:
		if msg.err != nil {
			m.statusErr = "tmux attach: " + msg.err.Error()
		}
		// The windows were released for the attach; take them back.
		if m.focus.on {
			m.fitFocus(true)
			return m, tea.Batch(m.focusPoll(0), doScan(m.statesDir))
		}
		return m, doScan(m.statesDir)
	}
	return m, nil
}

// applySnap stores a capture in tile i and reports whether it changed what the
// tile shows.
func (m *Model) applySnap(i int, sc tmux.Screen) bool {
	t := &m.focus.tiles[i]
	first := !t.have
	changed := first || !sameScreen(t.screen, sc)
	t.screen = sc
	t.have = true
	t.lastCap = time.Now()
	if t.scroll > sc.History {
		t.scroll = sc.History
	}
	// Only a capture can say whether the pane shares its window, so a pane
	// that does is zoomed onto the moment the first one arrives. Someone else
	// resizing the window — a client attached to it, a second nagare — is
	// corrected too, but not every frame.
	resized := t.fitW > 0 && (sc.Width != t.fitW || sc.Height != t.fitH) &&
		time.Since(t.lastFit) > focusRefitEvery
	if (first && sc.Panes > 1 && !sc.Zoomed) || resized {
		m.fitFocus(true)
		changed = true
	}
	return changed
}

// tileGone handles a tile whose pane no longer exists. A shell that exits hands
// back to its agent, like closing a terminal tab; an agent that exits takes its
// tile with it, and the last tile going ends focus mode.
func (m Model) tileGone(i int) (Model, tea.Cmd) {
	t := m.focus.tiles[i]
	if t.shell {
		for _, s := range m.sessions {
			if sessionKey(s) == t.key {
				m.focus.active = i
				m.statusNote = "shell closed"
				return m.showInTile(s, agentTarget(s), false)
			}
		}
	}
	m.statusNote = fmt.Sprintf("%s has exited", t.name)
	m.focus.tiles[i].fitW = 0 // nothing left to release
	if m.focus.n <= 1 {
		m.releaseFocus()
		return m, doScan(m.statesDir)
	}
	m.focus.active = i
	m, _ = m.closeTile()
	return m, doScan(m.statesDir)
}

// sameScreen reports whether two captures would draw identically.
func sameScreen(a, b tmux.Screen) bool {
	return a.CursorX == b.CursorX && a.CursorY == b.CursorY &&
		a.CursorVisible == b.CursorVisible && a.Width == b.Width &&
		a.Height == b.Height && slices.Equal(a.Lines, b.Lines)
}

// Keys focus mode keeps for itself. Everything else goes to the agent, which is
// why these are chords an agent's own UI has no use for.
const (
	keyFocusLeave     = "ctrl+]" // the default; picker.focus_leave_key overrides it
	keyFocusPrev      = "alt+up"
	keyFocusNext      = "alt+down"
	keyFocusSplit     = "alt+v"
	keyFocusCloseTile = "alt+x"
	keyFocusTileLeft  = "alt+left"
	keyFocusTileRight = "alt+right"
	keyFocusZoom      = "alt+z"
	keyFocusShell     = "alt+s"
	keyFocusBack      = "shift+pgup"
	keyFocusFwd       = "shift+pgdown"
	keyJumpTmux       = "f5"
)

// handleFocusKey routes a keypress in focus mode: nagare's own chords first,
// then everything else to the active tile.
func (m Model) handleFocusKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.showHelp {
		if key == keyHelp || key == keyEscape {
			m.showHelp = false
		}
		return m, nil
	}

	if key == m.leaveKeyOrDefault() {
		return m.leaveFocus(), m.doPreview()
	}

	switch key {
	case keyHelp:
		m.showHelp = true
		return m, nil
	case keyFocusPrev, keyFocusNext:
		if len(m.filtered) == 0 {
			return m, nil
		}
		step := 1
		if key == keyFocusPrev {
			step = -1
		}
		m.cursor = (m.cursor + step + len(m.filtered)) % len(m.filtered)
		return m.enterFocus(m.filtered[m.cursor])
	case keyNextAttention:
		idx := nextAttention(m.filtered, m.cursor)
		if idx < 0 {
			m.statusNote = "nothing is waiting for you"
			return m, nil
		}
		m.cursor = idx
		return m.enterFocus(m.filtered[idx])
	case keyFocusSplit:
		return m.addTile()
	case keyFocusCloseTile:
		return m.closeTile()
	case keyFocusTileLeft:
		return m.cycleTile(-1)
	case keyFocusTileRight:
		return m.cycleTile(1)
	case keyFocusShell:
		return m.toggleShell()
	case keyFocusReview:
		if s, ok := m.focusedSession(); ok {
			return m.openReview(s)
		}
		return m, nil
	case keyFocusZoom:
		m.focus.zoom = !m.focus.zoom
		m.fitFocus(false)
		return m, m.focusPoll(focusKeyPoll)
	case keyFocusBack, keyFocusFwd:
		page := max(m.focus.cur().fitH/2, 1)
		if key == keyFocusFwd {
			page = -page
		}
		return m.scrollFocus(m.focus.active, page)
	case keyJumpTmux:
		return m.jumpToTmux()
	}

	k, ok := translateKey(msg.Key())
	if !ok {
		return m, nil
	}
	t := m.focus.cur()
	// Typing is about the live screen: a keystroke while scrolled back returns
	// to it, as in any terminal.
	t.scroll = 0
	if k.text != "" {
		m.focus.q.Type(t.pane, k.text)
	} else {
		m.focus.q.Keys(t.pane, k.name)
	}
	m.focus.delay = focusFastPoll
	return m, m.focusPoll(focusKeyPoll)
}

// scrollFocus moves tile i's view by delta rows into history (positive is back).
func (m Model) scrollFocus(i, delta int) (tea.Model, tea.Cmd) {
	if i < 0 || i >= m.focus.n {
		return m, nil
	}
	t := &m.focus.tiles[i]
	t.scroll = min(max(t.scroll+delta, 0), t.screen.History)
	t.lastCap = time.Time{} // re-capture it now, whether active or not
	return m, m.focusPoll(0)
}

// pasteFocus sends pasted text to the active tile in one piece.
func (m Model) pasteFocus(text string) (tea.Model, tea.Cmd) {
	t := m.focus.cur()
	t.scroll = 0
	m.focus.q.Paste(t.pane, text)
	return m, m.focusPoll(focusKeyPoll)
}

// jumpToTmux hands the selected session to tmux proper, for the times the full
// tmux window is wanted — splits, copy mode, another tool beside the agent.
//
// Inside tmux this switches the client, as the picker always did. Outside tmux
// it attaches as a child process, so detaching comes straight back to nagare
// instead of to a bare shell.
func (m Model) jumpToTmux() (tea.Model, tea.Cmd) {
	s, ok := m.selectedSession()
	if !ok || s.Status == models.StatusSaved {
		return m, nil
	}
	if os.Getenv("TMUX") != "" {
		m.releaseFocus()
		session.SwitchToPane(s)
		return m, tea.Quit
	}
	// Hand the windows back first: an attached client should see them at its
	// own size, not nagare's. That has to have happened before the attach
	// starts, so wait for it.
	if m.focus.on {
		for i := range m.focus.n {
			m.releaseTile(i)
		}
		m.focus.sync(func() {})
		m.focus.seq++ // pause polling until the attach returns
	}
	tmux.RunTmux("select-window", "-t", fmt.Sprintf("%s:%d", s.SessionName, s.WindowIndex))
	tmux.RunTmux("select-pane", "-t", tmux.PaneTarget(s.SessionName, s.WindowIndex, s.PaneIndex))
	c := tmux.Command("attach-session", "-t", s.SessionName)
	return m, tea.ExecProcess(c, func(err error) tea.Msg { return attachDoneMsg{err: err} })
}

// FocusWhenReady returns the model set to focus the given tmux session as soon
// as a scan finds an agent in it — for a session created while nagare was
// showing a form.
func (m Model) FocusWhenReady(sessionName string) Model {
	m.awaitFocus(sessionName)
	return m
}

// toggleShell switches the active tile between its agent and a shell in the
// agent's directory. The shell is where the rest of the work happens — git,
// tests, logs — so having it one chord away is most of what keeps the user from
// leaving nagare to open a terminal of their own.
func (m Model) toggleShell() (tea.Model, tea.Cmd) {
	s, ok := m.focusedSession()
	if !ok {
		m.statusErr = "the agent is not listed yet; try again after the next scan"
		return m, nil
	}
	if m.focus.cur().shell {
		return m.showInTile(s, agentTarget(s), false)
	}
	pane, err := tmux.CompanionShell(m.focus.q.Run, agentTarget(s), s.SessionName, s.Path)
	if err != nil {
		m.statusErr = err.Error()
		return m, nil
	}
	return m.showInTile(s, pane, true)
}

// leaveKeyOrDefault is the key that leaves focus mode.
func (m Model) leaveKeyOrDefault() string {
	if m.leaveKey == "" {
		return keyFocusLeave
	}
	return m.leaveKey
}

// keyLabel spells a key binding for display: "^]" in the footer's short form,
// "Ctrl+]" on the help screen.
func keyLabel(key string, short bool) string {
	mods, base := "", key
	if i := strings.LastIndex(key, "+"); i > 0 && i < len(key)-1 {
		mods, base = key[:i+1], key[i+1:]
	}
	if len(base) > 1 {
		base = strings.ToUpper(base[:1]) + base[1:] // f12 → F12, esc → Esc
	}
	if short && mods == "ctrl+" {
		return "^" + base
	}
	mods = strings.NewReplacer("ctrl+", "Ctrl+", "alt+", "Alt+", "shift+", "Shift+").Replace(mods)
	return mods + base
}
