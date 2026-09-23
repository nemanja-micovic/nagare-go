package picker

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/nemke/nagare-go/internal/log"
	"github.com/nemke/nagare-go/internal/models"
	"github.com/nemke/nagare-go/internal/session"
	"github.com/nemke/nagare-go/internal/tmux"
)

// Focus mode hosts an agent's live terminal inside nagare.
//
// The picker used to end every interaction by handing the user to tmux: pick a
// session, get switched away, come back with a keybinding to pick the next one.
// Focus mode keeps them here instead. The selected agent's pane is drawn in
// place of the preview, keystrokes are forwarded to it, and the list stays
// alongside as a sidebar — so the moment another agent needs something, it is
// one keypress away rather than a trip back through the picker.
//
// tmux remains the backend and does all the terminal work. nagare fits the
// pane's window to the space it is shown in, polls tmux for the rendered screen,
// and sends keys back through send-keys. Agents keep running when nagare exits,
// exactly as before, and nagare no longer needs to run inside tmux at all.

// Polling bounds. A changing screen is re-captured at ~30fps so streaming output
// reads as streaming; a still one backs off to 4fps, so an idle agent costs
// almost nothing. A keystroke snaps straight back to the fast end.
const (
	focusFastPoll = 33 * time.Millisecond
	focusSlowPoll = 250 * time.Millisecond
	focusKeyPoll  = 12 * time.Millisecond
	// focusRefitEvery rate-limits correcting a window someone else resized, so
	// two nagare instances focused on one agent cannot fight every frame.
	focusRefitEvery = time.Second
	// focusSidebarMin/Max bound the sidebar. It shows names and status, not
	// detail, so it gets no more than it needs to leave the agent room.
	focusSidebarMin = 26
	focusSidebarMax = 36
)

type focusState struct {
	on     bool
	pane   string // tmux target: the pane id, stable across window moves
	key    string // sessionKey of the focused session
	name   string // display name, for messages once the session is gone
	agent  models.AgentType
	screen tmux.Screen
	have   bool // screen holds at least one capture

	fitW, fitH int // size the window was fitted to; 0 when not fitted
	zoomedByUs bool
	lastFit    time.Time

	zoom   bool // sidebar hidden, terminal takes the full width
	shell  bool // showing the agent's companion shell rather than the agent
	scroll int  // rows scrolled back into history; 0 is live

	seq     int           // poll-loop token; bumping it retires the running loop
	delay   time.Duration // next poll interval
	applied uint64        // newest capture applied, so late replies cannot rewind

	q        *tmux.Queue // shared by every copy of the Model
	captures *uint64     // capture counter, only touched on q's worker
}

// Messages driving focus mode.
type (
	focusTickMsg struct{ seq int }
	focusSnapMsg struct {
		seq    int
		n      uint64
		pane   string
		screen tmux.Screen
		err    error
	}
	// attachDoneMsg arrives when a full tmux attach started from focus mode
	// returns — the user detached, and is back in nagare.
	attachDoneMsg struct{ err error }
)

// focusGeometry is the single source of truth for where focus mode puts things.
// View draws from it and the window is fitted from it, so the pane is always
// exactly the size of the hole it is drawn into.
type focusGeometry struct {
	sidebarW     int // outer width of the sidebar, 0 when zoomed
	panelX       int // left edge of the terminal panel
	panelW       int // outer width of the terminal panel
	height       int // outer height of both panels
	termW, termH int // the pane itself
}

func (m Model) focusGeometry() focusGeometry {
	height := m.height
	if m.showHelpBar {
		height-- // the hint bar is pinned to exactly one row
	}
	g := focusGeometry{height: max(height, 3)}
	if !m.focus.zoom {
		g.sidebarW = min(max(m.width/5, focusSidebarMin), focusSidebarMax)
		// A terminal too narrow for both gives the agent everything.
		if m.width-g.sidebarW < 40 {
			g.sidebarW = 0
		}
	}
	g.panelX = g.sidebarW
	g.panelW = m.width - g.sidebarW
	// Border on every side, plus a column of padding left and right so the
	// agent's output does not sit flush against the frame.
	g.termW = max(g.panelW-4, 1)
	g.termH = max(g.height-2, 1)
	return g
}

// enterFocus opens s in focus mode. A saved session is started first and
// focused once its pane shows up in a scan.
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

	target := agentTarget(s)
	if m.focus.on && m.focus.pane == target {
		return m, nil
	}
	m, cmd := m.focusPane(s, target)
	m.focus.shell = false
	return m, cmd
}

// agentTarget is the tmux target for a session's agent pane.
func agentTarget(s models.Session) string {
	if s.PaneID != "" {
		return s.PaneID
	}
	return tmux.PaneTarget(s.SessionName, s.WindowIndex, s.PaneIndex)
}

// focusPane shows target — the agent's pane, or its companion shell — as the
// focused terminal, on behalf of session s.
func (m Model) focusPane(s models.Session, target string) (Model, tea.Cmd) {
	m.releaseFocus()

	if m.focus.q == nil {
		m.focus.q = m.newQueue()
		m.focus.captures = new(uint64)
	}
	f := &m.focus
	f.on = true
	f.pane = target
	f.key = sessionKey(s)
	f.name = s.Name
	f.agent = s.AgentType
	f.have = false
	f.scroll = 0
	f.screen = tmux.Screen{}
	f.fitW, f.fitH = 0, 0
	f.zoomedByUs = false
	f.delay = focusFastPoll

	// The sidebar lists every agent, not whatever the search last narrowed to;
	// a stale filter would hide the sessions the user came here to keep an eye on.
	if m.searchInput.Value() != "" {
		m.searchInput.SetValue("")
		m.applyFilter()
	}
	m.selectKey(f.key)

	log.Info("focus %s (%s)", s.Name, target)
	m.fitFocus(true)
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

// leaveFocus returns to the list, handing the pane's window back to tmux.
func (m Model) leaveFocus() Model {
	m.releaseFocus()
	return m
}

// releaseFocus un-fits the focused window and stops the poll loop. Safe to call
// when nothing is focused.
func (m *Model) releaseFocus() {
	f := &m.focus
	if !f.on {
		return
	}
	if f.fitW > 0 {
		f.q.Release(f.pane, f.zoomedByUs)
	}
	log.Info("unfocus %s", f.name)
	f.on = false
	f.seq++
	f.fitW, f.fitH = 0, 0
	f.zoomedByUs = false
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

// fitFocus sizes the focused pane's window to the terminal panel. force skips
// the check against the last fit, for a fresh focus.
func (m *Model) fitFocus(force bool) {
	f := &m.focus
	if !f.on || m.width == 0 {
		return
	}
	g := m.focusGeometry()
	if !force && f.fitW == g.termW && f.fitH == g.termH {
		return
	}
	f.fitW, f.fitH = g.termW, g.termH
	f.lastFit = time.Now()
	// Zoom only on the first fit, and only when the pane shares its window;
	// otherwise the pane would get a share of the window rather than all of it.
	zoom := !f.zoomedByUs && f.have && f.screen.Panes > 1 && !f.screen.Zoomed
	if zoom {
		f.zoomedByUs = true
	}
	f.q.Fit(f.pane, g.termW, g.termH, zoom)
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

func (m Model) focusCapture(seq int) tea.Cmd {
	f := m.focus
	q, counter := f.q, f.captures
	pane, scroll, height := f.pane, f.scroll, f.screen.Height
	return func() tea.Msg {
		var n uint64
		// Numbered on the worker, in the order the captures actually ran.
		q.Do(func() { *counter++; n = *counter })
		sc, err := q.Capture(pane, scroll, height)
		return focusSnapMsg{seq: seq, n: n, pane: pane, screen: sc, err: err}
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
		if !f.on || msg.pane != f.pane || msg.n <= f.applied {
			return m, nil
		}
		f.applied = msg.n
		if msg.err != nil {
			// A shell that exits hands back to its agent, like closing a
			// terminal tab; only the agent itself exiting ends focus mode.
			if f.shell {
				if s, ok := m.focusedSession(); ok {
					m.statusNote = "shell closed"
					return m.enterFocus(s)
				}
			}
			name := f.name
			m.releaseFocus()
			m.statusNote = fmt.Sprintf("%s has exited", name)
			return m, doScan(m.statesDir)
		}
		first := !f.have
		changed := first || !sameScreen(f.screen, msg.screen)
		f.screen = msg.screen
		f.have = true
		// Only a capture can say whether the pane shares its window, so a pane
		// that does is zoomed onto the moment the first one arrives.
		if first && msg.screen.Panes > 1 && !msg.screen.Zoomed {
			m.fitFocus(true)
		}
		if f.scroll > f.screen.History {
			f.scroll = f.screen.History
		}

		// Someone else resized the window — a client attached to it, or a
		// second nagare. Take it back, but not every frame.
		if f.fitW > 0 && (msg.screen.Width != f.fitW || msg.screen.Height != f.fitH) &&
			time.Since(f.lastFit) > focusRefitEvery {
			m.fitFocus(true)
			changed = true
		}

		if msg.seq != f.seq {
			return m, nil // a newer loop owns the schedule
		}
		if changed {
			f.delay = focusFastPoll
		} else {
			f.delay = min(f.delay*2, focusSlowPoll)
		}
		return m, m.focusPoll(f.delay)

	case attachDoneMsg:
		if msg.err != nil {
			m.statusErr = "tmux attach: " + msg.err.Error()
		}
		// The window was released for the attach; take it back.
		if m.focus.on {
			m.fitFocus(true)
			return m, tea.Batch(m.focusPoll(0), doScan(m.statesDir))
		}
		return m, doScan(m.statesDir)
	}
	return m, nil
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
	keyFocusLeave = "ctrl+]" // the default; picker.focus_leave_key overrides it
	keyFocusPrev  = "alt+up"
	keyFocusNext  = "alt+down"
	keyFocusZoom  = "alt+z"
	keyFocusShell = "alt+s"
	keyFocusBack  = "shift+pgup"
	keyFocusFwd   = "shift+pgdown"
	keyJumpTmux   = "f5"
)

// handleFocusKey routes a keypress in focus mode: nagare's own chords first,
// then everything else to the agent.
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
	case keyFocusShell:
		return m.toggleShell()
	case keyFocusZoom:
		m.focus.zoom = !m.focus.zoom
		m.fitFocus(false)
		return m, m.focusPoll(focusKeyPoll)
	case keyFocusBack, keyFocusFwd:
		page := max(m.focus.fitH/2, 1)
		if key == keyFocusFwd {
			page = -page
		}
		return m.scrollFocus(page)
	case keyJumpTmux:
		return m.jumpToTmux()
	}

	k, ok := translateKey(msg.Key())
	if !ok {
		return m, nil
	}
	f := &m.focus
	// Typing is about the live screen: a keystroke while scrolled back returns
	// to it, as in any terminal.
	f.scroll = 0
	if k.text != "" {
		f.q.Type(f.pane, k.text)
	} else {
		f.q.Keys(f.pane, k.name)
	}
	f.delay = focusFastPoll
	return m, m.focusPoll(focusKeyPoll)
}

// scrollFocus moves the view by delta rows into history (positive is back).
func (m Model) scrollFocus(delta int) (tea.Model, tea.Cmd) {
	f := &m.focus
	f.scroll = min(max(f.scroll+delta, 0), f.screen.History)
	return m, m.focusPoll(0)
}

// pasteFocus sends pasted text to the focused agent in one piece.
func (m Model) pasteFocus(text string) (tea.Model, tea.Cmd) {
	f := &m.focus
	f.scroll = 0
	f.q.Paste(f.pane, text)
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
	// Hand the window back first: an attached client should see it at its own
	// size, not nagare's.
	// It has to have happened before the attach starts, so wait for it.
	if m.focus.on {
		if m.focus.fitW > 0 {
			m.focus.q.Release(m.focus.pane, m.focus.zoomedByUs)
			m.focus.sync(func() {})
		}
		m.focus.fitW, m.focus.fitH, m.focus.zoomedByUs = 0, 0, false
		m.focus.seq++ // pause polling until the attach returns
	}
	tmux.RunTmux("select-window", "-t", fmt.Sprintf("%s:%d", s.SessionName, s.WindowIndex))
	tmux.RunTmux("select-pane", "-t", tmux.PaneTarget(s.SessionName, s.WindowIndex, s.PaneIndex))
	c := exec.Command("tmux", "attach-session", "-t", s.SessionName)
	return m, tea.ExecProcess(c, func(err error) tea.Msg { return attachDoneMsg{err: err} })
}

// FocusWhenReady returns the model set to focus the given tmux session as soon
// as a scan finds an agent in it — for a session created while nagare was
// showing a form.
func (m Model) FocusWhenReady(sessionName string) Model {
	m.awaitFocus(sessionName)
	return m
}

// toggleShell switches between the focused agent and a shell in its directory.
// The shell is where the rest of the work happens — git, tests, logs — so
// having it one chord away is most of what keeps the user from leaving nagare
// to open a terminal of their own.
func (m Model) toggleShell() (tea.Model, tea.Cmd) {
	s, ok := m.focusedSession()
	if !ok {
		m.statusErr = "the agent is not listed yet; try again after the next scan"
		return m, nil
	}
	if m.focus.shell {
		return m.enterFocus(s)
	}
	pane, err := tmux.CompanionShell(m.focus.q.Run, agentTarget(s), s.SessionName, s.Path)
	if err != nil {
		m.statusErr = err.Error()
		return m, nil
	}
	m, cmd := m.focusPane(s, pane)
	m.focus.shell = true
	return m, cmd
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
