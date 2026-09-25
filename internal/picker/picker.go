package picker

import (
	"cmp"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/nemke/nagare-go/internal/config"
	"github.com/nemke/nagare-go/internal/demo"
	"github.com/nemke/nagare-go/internal/git"
	"github.com/nemke/nagare-go/internal/log"
	"github.com/nemke/nagare-go/internal/models"
	"github.com/nemke/nagare-go/internal/session"
	"github.com/nemke/nagare-go/internal/state"
	"github.com/nemke/nagare-go/internal/theme"
	"github.com/nemke/nagare-go/internal/tmux"
	"github.com/sahilm/fuzzy"
)

// ViewMode controls the session display layout.
type ViewMode int

const (
	ListView ViewMode = iota
	GridView
)

// SortMode controls the session sort order.
type SortMode int

const (
	SortByStatus SortMode = iota
	SortByName
	SortByAgent
)

// Message types for async updates.
type SessionsUpdatedMsg []models.Session
type PreviewUpdatedMsg string

type tickScanMsg struct{}
type tickPreviewMsg struct{}
type gridPreviewsMsg map[string]string

// Picker exit actions.
const (
	ActionNone       = ""
	ActionNew        = "new"
	ActionQuickProto = "quickproto"
)

// Result is returned when the picker exits with a special action.
type Result struct {
	Action string
	Target string
}

// Model is the main Bubble Tea model for the picker TUI.
type Model struct {
	sessions       []models.Session
	filtered       []models.Session
	cursor         int
	viewMode       ViewMode
	sortMode       SortMode
	preview        string
	width          int
	height         int
	statesDir      string
	searchInput    textinput.Model
	showHelp       bool              // F1 help overlay
	showHelpBar    bool              // bottom hint bar
	showThemePick  bool              // Ctrl+t theme picker overlay
	themeNames     []string          // cached sorted theme names
	themeCursor    int               // cursor in theme picker
	themeOriginal  string            // theme before opening picker (for cancel)
	showSaved      bool              // show saved (unloaded) sessions
	gridPreviews   map[string]string // cached grid cell previews keyed by pane target
	registry       *state.Registry
	renameMode     bool
	renameSession  models.Session
	worktreeMode   bool
	worktreeOn     models.Session
	confirmMode    bool
	confirmOn      models.Session
	pending        *pendingWorktree // worktree being created, nil when idle
	spinner        spinner.Model
	statusErr      string              // last failure, shown until the next keypress
	statusNote     string              // neutral message, shown until the next keypress
	workCache      map[string]git.Work // outstanding work per path, refreshed each scan
	result         Result
	promptMode     bool
	promptTarget   models.Session
	promptInput    textinput.Model
	lastQuery      string   // previous search query, to detect query changes in applyFilter
	gridOrder      []string // frozen display order for grid view (session keys); nil = not yet snapshotted
	mouseEnabled   bool     // click-to-select and wheel scrolling (config: picker.mouse)
	animEnabled    bool     // spring-animated overlay entry (config: picker.animations)
	overlayAnim    overlayAnim
	breath         float64                         // phase of the status-dot breath, 0..1
	breathOn       bool                            // whether the breath clock is currently ticking
	slide          selectionSlide                  // highlight crossfading between rows
	arrived        string                          // worktree whose pane just appeared, to flash once it is listed
	history        map[string][]uint8              // recent activity levels per session, for sparklines
	flashes        map[string]flashState           // rows fading after a state change
	prevStatus     map[string]models.SessionStatus // statuses at the last scan, to spot transitions
	testNoScan     bool                            // test hook: disable the live tmux scanner (see export_test.go)
	focus          focusState                      // the agent terminal hosted in nagare (focus.go)
	enterJumps     bool                            // Enter switches to tmux instead of focusing (picker.enter_action)
	pendingFocus   string                          // tmux session to focus once an agent appears in it
	pendingFocusBy time.Time                       // when to stop waiting for pendingFocus
	leaveKey       string                          // returns from focus mode (picker.focus_leave_key)
	newQueue       func() *tmux.Queue              // where focus mode's tmux commands go; faked in tests
	filterGen      int                             // bumped whenever filtered is rebuilt
	compactRows    bool                            // rows rendered for the narrow focus-mode sidebar
	sidebarCache   *sidebarCache                   // focus mode's last sidebar render
}

// New creates a new picker model with default settings.
func New() Model {
	ti := textinput.New()
	ti.Placeholder = "search sessions..."
	ti.Prompt = " > "
	ti.CharLimit = 64

	ti.Focus()

	pi := textinput.New()
	pi.Placeholder = "type prompt to send..."
	pi.CharLimit = 500
	pi.SetWidth(60)

	cfg, _ := config.Load()

	return Model{
		statesDir:    state.DefaultStatesDir(),
		searchInput:  ti,
		showHelpBar:  cfg.Picker.ShowHelpBar,
		mouseEnabled: cfg.Picker.Mouse,
		animEnabled:  cfg.Picker.Animations,
		enterJumps:   cfg.Picker.EnterAction == config.EnterJump,
		overlayAnim:  newOverlayAnim(),
		registry:     state.NewRegistry(state.DefaultRegistryPath()),
		promptInput:  pi,
		workCache:    make(map[string]git.Work),
		flashes:      make(map[string]flashState),
		history:      make(map[string][]uint8),
		prevStatus:   make(map[string]models.SessionStatus),
		spinner:      newSpinner(),
		sidebarCache: &sidebarCache{},
		newQueue:     tmux.NewQueue,
		leaveKey:     cmp.Or(cfg.Picker.FocusLeaveKey, keyFocusLeave),
		// The demo introduces itself; the note clears on the first keypress.
		statusNote: os.Getenv(demo.NoteEnv),
	}
}

// markDead writes a dead state for a session before killing it.
func markDead(s models.Session, statesDir string) {
	state.WriteState(statesDir, models.SessionState{
		State:     string(models.StatusDead),
		SessionID: s.SessionID,
		Cwd:       s.Path,
		Event:     "ManualKill",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

// selectedSession returns the currently selected session, if any.
func (m Model) selectedSession() (models.Session, bool) {
	if len(m.filtered) == 0 || m.cursor >= len(m.filtered) {
		return models.Session{}, false
	}
	return m.filtered[m.cursor], true
}

// activateSelected jumps to the selected session, loading it first when it is a
// saved one. Both Enter and a click on the selected row land here, so the two
// cannot drift apart.
func (m Model) activateSelected() (tea.Model, tea.Cmd) {
	s, ok := m.selectedSession()
	if !ok {
		return m, nil
	}
	if !m.enterJumps {
		return m.enterFocus(s)
	}
	if s.Status == models.StatusSaved {
		agent := string(s.AgentType)
		if agent == "" || agent == "unknown" {
			agent = "claude"
		}
		name, err := session.Load(s.Path, s.Name, agent)
		if err != nil {
			log.Error("load session: %v", err)
			return m, nil
		}
		session.SwitchToSession(name)
		return m, tea.Quit
	}
	session.SwitchToPane(s)
	return m, tea.Quit
}

// approvable reports whether Ctrl+y should send Enter to a session in this
// status. Includes StatusRunning because the hook state lags: Claude fires
// PreToolUse ("working") first, then Notification/permission_prompt
// ("waiting_input") once the dialog is actually rendered — the yellow→red
// transition can take a few seconds. Allowing approval during "working"
// covers that gap. Idle/dead/saved sessions are skipped so a stray Ctrl+y
// doesn't submit an empty prompt or hit a shell.
func approvable(status models.SessionStatus) bool {
	return status == models.StatusWaitingInput || status == models.StatusRunning
}

// sessionKey returns a stable identifier for a session. The cursor tracks this
// key across re-filters so the selection follows the session, not the index.
func sessionKey(s models.Session) string {
	if s.Status == models.StatusSaved {
		return "saved:" + s.Name
	}
	return tmux.PaneTarget(s.SessionName, s.WindowIndex, s.PaneIndex)
}

// snapshotGridOrder captures the current m.filtered order into gridOrder so
// subsequent scans preserve cell positions in grid view.
func (m *Model) snapshotGridOrder() {
	m.gridOrder = make([]string, len(m.filtered))
	for i, s := range m.filtered {
		m.gridOrder[i] = sessionKey(s)
	}
}

// applyGridOrder rebuilds m.filtered honoring the frozen gridOrder: existing
// sessions keep their slot, sessions not in the snapshot (newly appeared) are
// appended, and sessions no longer visible are dropped. gridOrder is updated
// to the new final order.
func (m *Model) applyGridOrder(visible []models.Session) {
	byKey := make(map[string]models.Session, len(visible))
	for _, s := range visible {
		byKey[sessionKey(s)] = s
	}

	result := make([]models.Session, 0, len(visible))
	order := make([]string, 0, len(visible))

	for _, k := range m.gridOrder {
		if s, ok := byKey[k]; ok {
			result = append(result, s)
			order = append(order, k)
			delete(byKey, k)
		}
	}
	for _, s := range visible {
		k := sessionKey(s)
		if _, still := byKey[k]; still {
			result = append(result, s)
			order = append(order, k)
		}
	}

	m.filtered = result
	m.gridOrder = order
}

// workFor returns outstanding git work for a session, computed at most once per
// scan per path. It is only consulted for worktree panes and only for the
// selected session, because the detail pane re-renders every frame for the
// status-dot pulse and git must not be called from there.
// The receiver is a value, but workCache is a map created in New() and replaced
// (never nilled) on each scan, so entries written here persist into the real
// model rather than dying with a copy — which is what keeps git off the render
// path after the first lookup.
func (m Model) workFor(s models.Session) git.Work {
	if s.Path == "" || m.workCache == nil {
		return git.Work{}
	}
	if w, ok := m.workCache[s.Path]; ok {
		return w
	}
	w := git.WorkStatus(s.Path)
	m.workCache[s.Path] = w
	return w
}

// query returns the active search text. Rename and new-worktree modes borrow the
// same input field for typing a name, so the text is not a search query then —
// without this the list filters itself down as you type a worktree name.
func (m Model) query() string {
	if m.renameMode || m.worktreeMode || m.confirmMode {
		return ""
	}
	return m.searchInput.Value()
}

// isStarred returns whether a session is starred in the registry. A Model
// without a registry has no stars rather than panicking, which keeps sorting
// testable in isolation.
func (m Model) isStarred(name string) bool {
	if m.registry == nil {
		return false
	}
	s := m.registry.Find(name)
	return s != nil && s.Starred
}

// Result returns the picker's result (action to take after quitting).
func (m Model) Result() Result {
	return m.result
}

func (m Model) Init() tea.Cmd {
	if m.testNoScan {
		return nil
	}
	return tea.Batch(
		// Windows a crashed run left fitted to its own size go back to tmux.
		func() tea.Msg { tmux.ReleaseStaleWindows(); return nil },
		doScan(m.statesDir),
		doPreviewTick(),
		doBreathTick(),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// A focused agent is resized with nagare, so it keeps drawing for exactly
		// the space it is shown in.
		m.fitFocus(false)
		return m, nil

	case focusTickMsg, focusSnapMsg, attachDoneMsg:
		return m.updateFocus(msg)

	case tea.PasteMsg:
		switch {
		case m.focus.on && !m.overlayOpen():
			return m.pasteFocus(msg.Content)
		case m.promptMode:
			var cmd tea.Cmd
			m.promptInput, cmd = m.promptInput.Update(msg)
			return m, cmd
		}
		var cmd tea.Cmd
		m.searchInput, cmd = m.searchInput.Update(msg)
		if !m.renameMode && !m.worktreeMode {
			m.applyFilter()
		}
		return m, cmd

	case mouseFocusMsg:
		if msg.index < 0 || msg.index >= len(m.filtered) {
			return m, nil
		}
		m.cursor = msg.index
		return m.enterFocus(m.filtered[msg.index])

	case mouseTermScrollMsg:
		if !m.focus.on {
			return m, nil
		}
		return m.scrollFocus(msg.tile, msg.delta)

	case mouseTileMsg:
		if !m.focus.on {
			return m, nil
		}
		return m.activateTile(msg.tile)

	case mouseSelectMsg:
		if msg.index < 0 || msg.index >= len(m.filtered) {
			return m, nil
		}
		m.statusErr = ""
		prev := m.cursor
		m.cursor = msg.index
		cmds := []tea.Cmd{m.doPreview()}
		if m.startSlide(prev, len(m.filtered)) {
			cmds = append(cmds, doAnimTick())
		}
		return m, tea.Batch(cmds...)

	case mouseActivateMsg:
		if msg.index < 0 || msg.index >= len(m.filtered) {
			return m, nil
		}
		m.cursor = msg.index
		return m.activateSelected()

	case mouseDismissMsg:
		// Same semantics as Esc on each overlay: cancelling the theme picker
		// restores the theme it was previewing over.
		if m.showThemePick {
			theme.Set(m.themeOriginal)
			m.showThemePick = false
		}
		m.showHelp = false
		return m, nil

	case mouseScrollMsg:
		if len(m.filtered) == 0 {
			return m, nil
		}
		// A wheel notch moves one visual row, which in grid view is a whole
		// rank of cards.
		step := msg.delta
		if m.viewMode == GridView {
			step *= gridColumns(len(m.filtered))
		}
		prev := m.cursor
		m.cursor = min(max(m.cursor+step, 0), len(m.filtered)-1)
		cmds := []tea.Cmd{m.doPreview()}
		if m.startSlide(prev, len(m.filtered)) {
			cmds = append(cmds, doAnimTick())
		}
		return m, tea.Batch(cmds...)

	case spinner.TickMsg:
		if m.pending == nil {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		// Give up rather than spin forever if the pane never arrives.
		if m.pending.expired(time.Now()) {
			log.Info("worktree %q: agent pane never appeared", m.pending.name)
			m.statusErr = fmt.Sprintf("worktree %q created, but its agent never started", m.pending.name)
			m.pending = nil
			return m, nil
		}
		return m, cmd

	case worktreeCreatedMsg:
		if msg.err != nil {
			log.Info("worktree %q failed: %v", msg.name, msg.err)
			m.statusErr = msg.err.Error()
			m.pending = nil
			return m, nil
		}
		log.Info("created worktree %s", msg.name)
		// Keep waiting: the agent still has to start before the pane exists.
		return m, doScan(m.statesDir)

	case SessionsUpdatedMsg:
		m.sessions = []models.Session(msg)
		if m.pending != nil && m.pending.satisfiedBy(m.sessions) {
			log.Info("worktree %q is live", m.pending.name)
			// Hand the eye off from the spinner to the row that just appeared. The
			// spinner sits above the list and the new pane arrives somewhere inside
			// it, sorted among everything else, so without this the wait ends by the
			// spinner simply vanishing and leaving the user to find what it made.
			m.arrived = m.pending.name
			m.pending = nil
		}
		m.workCache = make(map[string]git.Work) // recompute work against the new scan
		// Spot the transitions worth announcing before the new statuses overwrite
		// the old ones.
		for key, f := range detectFlashes(m.prevStatus, m.sessions) {
			m.flashes[key] = f
		}
		m.prevStatus = statusesOf(m.sessions)
		recordActivity(m.history, m.sessions)
		// A worktree that has just come up flashes like any other arrival worth
		// noticing, reusing the same fade rather than inventing a second one.
		var opened *models.Session
		if m.arrived != "" {
			for _, sess := range m.sessions {
				if sess.Details.Worktree == m.arrived {
					m.flashes[sessionKey(sess)] = flashState{kind: flashDone, level: 1}
					m.arrived = ""
					opened = &sess
					break
				}
			}
		}
		m.mergeSavedSessions()
		log.Debug("scan: %d sessions (%d saved)", len(m.sessions), m.countSaved())
		m.applyFilter()
		// The sidebar's selection is the focused agent, wherever the re-sort put it.
		if m.focus.on {
			m.selectKey(m.focus.cur().key)
		}
		var focusCmd tea.Cmd
		m, focusCmd = m.resolvePendingFocus()
		// A worktree the user just asked for opens like a new session does:
		// they made it to work in it.
		if opened != nil && !m.enterJumps {
			m, focusCmd = m.enterFocus(*opened)
		}
		if m.testNoScan {
			return m, focusCmd
		}
		next := tea.Tick(2*time.Second, func(time.Time) tea.Msg { return tickScanMsg{} })
		// The clock stops itself when nothing is moving, so a scan that turns up
		// activity — or starts a flash — has to start it again.
		if !m.breathOn && (needsBreathing(m.filtered) || len(m.flashes) > 0) {
			m.breathOn = true
			return m, tea.Batch(next, doBreathTick(), focusCmd)
		}
		return m, tea.Batch(next, focusCmd)

	case PreviewUpdatedMsg:
		m.preview = string(msg)
		return m, tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return tickPreviewMsg{} })

	case gridPreviewsMsg:
		m.gridPreviews = map[string]string(msg)
		return m, tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return tickPreviewMsg{} })

	case tickScanMsg:
		return m, doScan(m.statesDir)

	case tickPreviewMsg:
		if m.focus.on {
			// The focus loop is already capturing the one pane on screen; keep
			// the preview clock alive, idle, for when the list comes back.
			return m, doPreviewTick()
		}
		return m, m.doPreview()

	case tickBreathMsg:
		// One clock drives both the breath and any fading rows. It stops when
		// neither has anything to do, so a settled list costs nothing at all, and
		// the scan handler restarts it as soon as something turns up.
		fading := stepFlashes(m.flashes)
		breathing := needsBreathing(m.filtered)
		if !breathing && !fading {
			m.breath, m.breathOn = 0, false
			return m, nil
		}
		m.breathOn = true
		if breathing {
			m.breath = breathStep(m.breath)
		}
		return m, doBreathTick()

	case tickAnimMsg:
		// One transient clock for both; it stops when neither has anything left.
		moving := m.overlayAnim.step()
		if m.slide.step() {
			moving = true
		}
		if !moving {
			return m, nil
		}
		return m, doAnimTick()

	case tea.KeyMsg:
		wasOpen := m.overlayOpen()
		prevCursor, prevLen := m.cursor, len(m.filtered)

		next, cmd := m.handleKey(msg)
		updated, ok := next.(Model)
		if !ok {
			return next, cmd
		}

		cmds := []tea.Cmd{cmd}
		// Catching these transitions here, rather than at each of the places that
		// cause them, means a new overlay or a new navigation key cannot forget to
		// animate.
		if updated.animEnabled && !wasOpen && updated.overlayOpen() {
			updated.overlayAnim.start()
			cmds = append(cmds, doAnimTick())
		}
		if !updated.overlayOpen() {
			updated.overlayAnim.stop()
		}
		if updated.startSlide(prevCursor, prevLen) {
			cmds = append(cmds, doAnimTick())
		}
		return updated, tea.Batch(cmds...)

	case editorDoneMsg:
		defer os.Remove(msg.path)
		if msg.err != nil {
			log.Error("editor prompt failed: %v", msg.err)
			return m, nil
		}
		data, err := os.ReadFile(msg.path)
		if err != nil {
			log.Error("editor prompt read: %v", err)
			return m, nil
		}
		text := strings.TrimSpace(string(data))
		if text != "" {
			sendPromptToPane(m.promptTarget, text)
			log.Info("editor prompt sent to %s", m.promptTarget.Name)
		}
		return m, nil

	case configEditDoneMsg:
		if msg.err != nil {
			log.Error("config editor failed: %v", msg.err)
		}
		// Config may have changed — reload theme
		if cfg, err := config.Load(); err == nil {
			theme.Set(cfg.Appearance.Theme)
		}
		return m, nil
	}

	return m, nil
}

func (m Model) View() tea.View {
	content, hits := m.view()
	content = clampHeight(content, m.height)
	v := tea.NewView(content)
	v.AltScreen = true
	v.Cursor = m.focusCursor()

	if m.mouseEnabled {
		// Cell motion, not all motion: it covers clicks, releases and the wheel
		// while only reporting movement with a button held, and it is the better
		// supported of the two.
		v.MouseMode = tea.MouseModeCellMotion
		// OnMouse exists for exactly this — resolving coordinates against the
		// layout of the frame that was last drawn. The closure captures the hit
		// targets built while rendering it, then emits intent, so the mouse ends
		// up driving the same handlers as the keyboard.
		cursor := m.cursor
		v.OnMouse = func(msg tea.MouseMsg) tea.Cmd {
			intent := hits.resolve(msg, cursor)
			if intent == nil {
				return nil
			}
			return func() tea.Msg { return intent }
		}
	}
	return v
}

// clampHeight drops any rows past the terminal's last line. A frame even one row
// too tall scrolls the alt screen and smears the whole UI.
//
// The width half of this guard used to live here too, as a MaxWidth Render over
// the assembled frame — and it cost 18% of every frame, because Render measures
// each line with full grapheme segmentation. It is not needed: every panel is
// pinned by fitBox, grid rows and the help bar are rendered at an explicit width,
// and placeOverlay clamps itself, so the invariant is established where content is
// built rather than patched up at the end. TestFrameIsExactlyTerminalSized asserts
// it across view modes, session counts and terminal sizes.
//
// Height stays because it is nearly free: dropping trailing lines needs no
// measurement at all.
func clampHeight(content string, height int) string {
	if height <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	if len(lines) <= height {
		return content
	}
	return strings.Join(lines[:height], "\n")
}

func (m Model) view() (string, hitTargets) {
	if m.width == 0 {
		return "Loading...", hitTargets{}
	}

	hits := hitTargets{}

	// Render the help bar first and measure it. It soft-wraps, and how many
	// lines it takes depends on the terminal width — assuming a fixed two
	// pushed the panels off the bottom of a narrow terminal.
	bar := ""
	contentHeight := m.height
	if m.showHelpBar {
		bar = helpBar(m, m.width)
		contentHeight = m.height - lipgloss.Height(bar)
		if contentHeight < 1 {
			contentHeight = 1
		}
	}

	var base string
	if m.focus.on {
		base, hits = m.viewFocus()
	} else if m.viewMode == GridView {
		base, hits.cards = m.viewGrid(m.width, contentHeight)
	} else {
		leftOuter := m.width / 5
		rightOuter := m.width - leftOuter
		left, sessionAt := m.viewLeft(leftOuter, contentHeight)
		right := m.viewRight(rightOuter, contentHeight)
		base = lipgloss.JoinHorizontal(lipgloss.Top, left, right)
		hits.sessionAt = sessionAt
		hits.listWidth = leftOuter
	}

	if m.showHelpBar {
		base = base + "\n" + bar
	}

	// Overlays drawn on top of base content. Whichever is open also becomes the
	// only mouse target: an overlay's bounds are recorded so a click outside it
	// can dismiss it, and the targets underneath are dropped so a click cannot
	// reach through a dialog to the list behind it.
	overlay, dismissable := "", false
	switch {
	case m.showHelp:
		overlay, dismissable = helpOverlayFor(m.width, m.height, keyLabel(m.leaveKeyOrDefault(), false)), true
	case m.showThemePick:
		overlay, dismissable = themePickOverlay(m.themeNames, m.themeCursor, m.width, m.height), true
	case m.promptMode:
		// Not dismissable: a half-typed prompt should not be thrown away by a
		// stray click, and neither should a pending destructive answer.
		overlay = m.renderPromptOverlay()
	case m.confirmMode:
		overlay = m.renderConfirmOverlay()
	}
	if overlay != "" {
		dy := m.overlayAnim.offset()
		// Bounds come from the same offset the frame is drawn with, so a click
		// lands on the dialog where it currently appears, not where it will rest.
		hits.dialog = overlayRect(m.width, m.height, overlay, dy)
		hits.dismissable = dismissable
		return placeOverlay(m.width, m.height, overlay, base, dy), hits
	}

	return base, hits
}

// renderPromptOverlay renders the inline prompt dialog.
func (m Model) renderPromptOverlay() string {
	c := theme.Current().Colors
	title := lipgloss.NewStyle().Foreground(c.Primary).Bold(true).
		Render("Send to: " + m.promptTarget.Name)
	hint := lipgloss.NewStyle().Foreground(c.Muted).
		Render("Enter send  Esc cancel")
	content := title + "\n\n" + m.promptInput.View() + "\n\n" + hint
	return dialogStyle().Padding(1, 2).Render(onPlane(content, c.Overlay))
}

// renderConfirmOverlay renders the worktree removal dialog. It reports the
// worktree's outstanding work, because git refuses to remove a dirty worktree —
// better to say so before the keypress than to fail after it.
func (m Model) renderConfirmOverlay() string {
	c := theme.Current().Colors
	s := m.confirmOn

	title := lipgloss.NewStyle().Foreground(c.Warning).Bold(true).
		Render("Remove worktree")
	name := lipgloss.NewStyle().Foreground(c.Foreground).Bold(true).Render(s.Details.Worktree)
	path := lipgloss.NewStyle().Foreground(c.Muted).Render(s.Path)

	work := m.workFor(s)
	var state string
	if work.Dirty > 0 {
		state = lipgloss.NewStyle().Foreground(c.Warning).
			Render(fmt.Sprintf("%d uncommitted change(s) — removal will be refused", work.Dirty))
	} else {
		state = lipgloss.NewStyle().Foreground(c.Muted).
			Render("clean — the branch is kept, only the directory goes")
	}

	hint := lipgloss.NewStyle().Foreground(c.Muted).Render("y  delete      n / esc  keep")
	content := title + "\n\n" + name + "\n" + path + "\n\n" + state + "\n\n" + hint
	return dialogStyle().Padding(1, 2).Render(onPlane(content, c.Overlay))
}

// --- Key handling ---

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	log.Debug("key: %q", key)

	// Any keystroke acknowledges the last failure or note.
	m.statusErr = ""
	m.statusNote = ""

	// Theme picker intercepts all keys when open
	if m.showThemePick {
		return m.handleThemePickKey(key)
	}

	// Rename mode intercepts keys before normal handling
	if m.renameMode {
		return m.handleRenameKey(msg)
	}

	// Worktree-name entry intercepts keys the same way
	if m.worktreeMode {
		return m.handleWorktreeKey(msg)
	}

	// A pending confirmation swallows everything until answered
	if m.confirmMode {
		return m.handleConfirmKey(msg)
	}

	// Prompt mode intercepts keys
	if m.promptMode {
		return m.handlePromptKey(msg)
	}

	// Focus mode hands the keyboard to the agent, bar a few chords of its own.
	if m.focus.on {
		return m.handleFocusKey(msg)
	}

	switch key {
	case keyHelp:
		m.showHelp = !m.showHelp
		return m, nil
	case keyEscape:
		if m.showHelp {
			m.showHelp = false
			return m, nil
		}
		return m, tea.Quit
	case keyEnter:
		return m.activateSelected()
	case keyUp:
		if m.viewMode == GridView {
			cols := gridColumns(len(m.filtered))
			if m.cursor-cols >= 0 {
				m.cursor -= cols
			}
		} else if m.cursor > 0 {
			m.cursor--
		}
		return m, m.doPreview()
	case keyDown:
		if m.viewMode == GridView {
			cols := gridColumns(len(m.filtered))
			if m.cursor+cols < len(m.filtered) {
				m.cursor += cols
			}
		} else if m.cursor < len(m.filtered)-1 {
			m.cursor++
		}
		return m, m.doPreview()
	case "left":
		if m.viewMode == GridView && m.cursor > 0 {
			m.cursor--
		}
		return m, m.doPreview()
	case "right":
		if m.viewMode == GridView && m.cursor < len(m.filtered)-1 {
			m.cursor++
		}
		return m, m.doPreview()
	case keyToggleView:
		if m.viewMode == ListView {
			m.viewMode = GridView
			// Fresh snapshot: start grid from current sorted order, then freeze.
			m.gridOrder = nil
			m.applyFilter()
			log.Info("switched to grid view")
		} else {
			m.viewMode = ListView
			m.gridOrder = nil
			log.Info("switched to list view")
		}
		return m, nil
	case keyCycleTheme:
		m.showThemePick = true
		m.themeNames = theme.Names()
		m.themeOriginal = theme.Current().Name
		// Set cursor to current theme
		for i, name := range m.themeNames {
			if name == m.themeOriginal {
				m.themeCursor = i
				break
			}
		}
		return m, nil
	case keyApprove:
		if s, ok := m.selectedSession(); ok {
			if approvable(s.Status) {
				tmux.RunTmux("send-keys", "-t", tmux.PaneTarget(s.SessionName, s.WindowIndex, s.PaneIndex), "Enter")
				log.Info("approved %s (status=%s)", s.Name, s.Status)
			} else {
				log.Info("approve ignored: %s is %s", s.Name, s.Status)
			}
		}
		return m, nil
	case keyApproveAlways:
		if s, ok := m.selectedSession(); ok {
			if approvable(s.Status) {
				tmux.RunTmux("send-keys", "-t", tmux.PaneTarget(s.SessionName, s.WindowIndex, s.PaneIndex), "Down", "Enter")
				log.Info("approved always %s (status=%s)", s.Name, s.Status)
			} else {
				log.Info("approve-always ignored: %s is %s", s.Name, s.Status)
			}
		}
		return m, nil
	case keyUnload:
		if s, ok := m.selectedSession(); ok {
			markDead(s, m.statesDir)
			tmux.RunTmux("kill-pane", "-t", tmux.PaneTarget(s.SessionName, s.WindowIndex, s.PaneIndex))
			log.Info("unloaded pane %s", s.Name)
			return m, doScan(m.statesDir)
		}
		return m, nil
	case keyKillSession:
		if s, ok := m.selectedSession(); ok {
			markDead(s, m.statesDir)
			// Kill only this window when the session holds sibling agent panes,
			// or a repo's other worktrees would go down with it.
			if target, isWindow := killTarget(s, m.sessions); isWindow {
				tmux.RunTmux("kill-window", "-t", target)
				log.Info("killed window %s (%s)", target, s.Name)
			} else {
				tmux.RunTmux("kill-session", "-t", target)
				log.Info("killed session %s", s.Name)
			}
			// A worktree outlives its pane, so offer to remove it too.
			if s.Details.Worktree != "" {
				m.confirmMode = true
				m.confirmOn = s
				return m, doScan(m.statesDir)
			}
			return m, doScan(m.statesDir)
		}
		return m, nil
	case keyToggleSaved:
		m.showSaved = !m.showSaved
		m.applyFilter()
		return m, nil
	case keyStar:
		if s, ok := m.selectedSession(); ok {
			// Auto-register if not in registry
			if m.registry.Find(s.Name) == nil {
				m.registry.Register(s.Name, s.Path, string(s.AgentType))
			}
			starred := m.registry.ToggleStar(s.Name)
			if starred {
				log.Info("starred %s", s.Name)
			} else {
				log.Info("unstarred %s", s.Name)
			}
			// Refresh registry after toggle
			m.registry = state.NewRegistry(state.DefaultRegistryPath())
		}
		return m, nil
	case keyCycleSort:
		switch m.sortMode {
		case SortByStatus:
			m.sortMode = SortByName
		case SortByName:
			m.sortMode = SortByAgent
		case SortByAgent:
			m.sortMode = SortByStatus
		}
		// Clear the grid snapshot so the new sort order takes effect, then
		// applyFilter re-snapshots and the grid freezes on that order.
		m.gridOrder = nil
		m.applyFilter()
		log.Info("sort mode: %d", m.sortMode)
		return m, nil
	case keyRename:
		if s, ok := m.selectedSession(); ok && s.Status != models.StatusSaved {
			m.renameMode = true
			m.renameSession = s
			// The project already lives in the group header. Edit only the task
			// name shown on the child row, never the technical "repo/window" ID.
			m.searchInput.SetValue(renameWindowName(s, childLabel(s)))
			m.searchInput.CursorEnd()
		}
		return m, nil
	case keyNewWorktree:
		if s, ok := m.selectedSession(); ok {
			m.worktreeMode = true
			m.worktreeOn = s
			m.searchInput.SetValue("")
		}
		return m, nil
	case keyNewSession:
		m.result = Result{Action: ActionNew}
		return m, tea.Quit
	case keyQuickProto:
		m.result = Result{Action: ActionQuickProto}
		return m, tea.Quit
	case keyInlinePrompt:
		if s, ok := m.selectedSession(); ok {
			m.promptMode = true
			m.promptTarget = s
			m.promptInput.SetValue("")
			m.promptInput.Focus()
		}
		return m, nil
	case keyEditPrompt:
		if s, ok := m.selectedSession(); ok {
			m.promptTarget = s
			return m, m.openEditorPrompt()
		}
		return m, nil
	case keyNextAttention:
		return m.jumpToNextAttention()
	case keyJumpTmux:
		return m.jumpToTmux()
	case keyEditConfig:
		return m, m.openConfigEditor()
	default:
		// All other keys go to search input
		var cmd tea.Cmd
		m.searchInput, cmd = m.searchInput.Update(msg)
		m.applyFilter()
		return m, cmd
	}
}

// handleConfirmKey answers the pending worktree-removal confirmation. Anything
// other than an explicit yes declines, since the action deletes a directory.
func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		// fall through to removal below
	case "n", "N", keyEscape, keyEnter:
		m.confirmMode = false
		log.Info("kept worktree %s", m.confirmOn.Details.Worktree)
		return m, nil
	default:
		// Ignore unrelated keys rather than treating them as an answer: a
		// stray keystroke used to dismiss this silently.
		return m, nil
	}
	m.confirmMode = false
	// git refuses while the worktree holds uncommitted work, so a mistaken yes
	// cannot destroy unsaved changes.
	if err := git.RemoveWorktree(m.confirmOn.Path); err != nil {
		log.Info("could not remove worktree %s: %v", m.confirmOn.Details.Worktree, err)
		return m, nil
	}
	log.Info("removed worktree %s", m.confirmOn.Details.Worktree)
	return m, doScan(m.statesDir)
}

// handleWorktreeKey handles key input while naming a new worktree. The name is
// typed into the search input, the same field rename mode borrows.
func (m Model) handleWorktreeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEscape:
		m.worktreeMode = false
		m.searchInput.SetValue("")
		return m, nil
	case keyEnter:
		name := strings.TrimSpace(m.searchInput.Value())
		m.worktreeMode = false
		m.searchInput.SetValue("")
		if name == "" {
			return m, nil
		}
		// The agent inherits from the session the worktree is spawned off, so
		// a repo full of Claude panes keeps getting Claude panes.
		m.pending = &pendingWorktree{name: name, deadline: time.Now().Add(worktreeWait)}
		m.statusErr = ""
		return m, tea.Batch(
			m.spinner.Tick,
			createWorktreeCmd(m.worktreeOn.Path, name, string(m.worktreeOn.AgentType)),
		)
	default:
		var cmd tea.Cmd
		m.searchInput, cmd = m.searchInput.Update(msg)
		return m, cmd
	}
}

// renameWindowName converts a picker display name into the unqualified name
// tmux stores on the window. It also accepts a legacy or pasted
// "session/window" value so the repository prefix can never become part of the
// window name again.
func renameWindowName(s models.Session, value string) string {
	name := strings.TrimSpace(value)
	return strings.TrimPrefix(name, s.SessionName+"/")
}

// handleRenameKey handles key input during rename mode.
func (m Model) handleRenameKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case keyEscape:
		// Cancel rename
		m.renameMode = false
		m.searchInput.SetValue("")
		return m, nil
	case keyEnter:
		// Finish rename
		newName := renameWindowName(m.renameSession, m.searchInput.Value())
		m.renameMode = false
		m.searchInput.SetValue("")

		if newName == "" || newName == childLabel(m.renameSession) {
			return m, nil
		}

		// A task name belongs to the selected agent window, not to the enclosing
		// tmux session. Keeping the latter stable preserves the repository header,
		// MCP target, saved-session identity, and sibling windows.
		target := fmt.Sprintf("%s:%d", m.renameSession.SessionName, m.renameSession.WindowIndex)
		tmux.RunTmux("rename-window", "-t", target, newName)
		log.Info("named task %s -> %s", target, newName)

		return m, doScan(m.statesDir)
	default:
		// Forward to search input for text editing
		var cmd tea.Cmd
		m.searchInput, cmd = m.searchInput.Update(msg)
		return m, cmd
	}
}

// handlePromptKey handles key input during prompt mode.
func (m Model) handlePromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case keyEscape:
		m.promptMode = false
		return m, nil
	case keyEnter:
		text := strings.TrimSpace(m.promptInput.Value())
		if text != "" {
			sendPromptToPane(m.promptTarget, text)
			log.Info("prompt sent to %s", m.promptTarget.Name)
		}
		m.promptMode = false
		return m, nil
	default:
		var cmd tea.Cmd
		m.promptInput, cmd = m.promptInput.Update(msg)
		return m, cmd
	}
}

// sendPromptToPane sends text to a session's tmux pane.
func sendPromptToPane(s models.Session, text string) {
	target := tmux.PaneTarget(s.SessionName, s.WindowIndex, s.PaneIndex)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i < len(lines)-1 {
			// Intermediate lines: send text then Enter separately
			tmux.RunTmux("send-keys", "-t", target, tmux.Arg(line), "")
			tmux.RunTmux("send-keys", "-t", target, "Enter", "")
		} else {
			// Last line: send with Enter to execute
			tmux.RunTmux("send-keys", "-t", target, tmux.Arg(line), "Enter")
		}
	}
}

// editorDoneMsg is sent when the editor process completes.
type editorDoneMsg struct {
	path string
	err  error
}

// configEditDoneMsg is sent when the config editor process completes.
type configEditDoneMsg struct {
	err error
}

func resolveEditor() string {
	if e := os.Getenv("EDITOR"); e != "" {
		return e
	}
	if e := os.Getenv("VISUAL"); e != "" {
		return e
	}
	return "vi"
}

// openEditorPrompt opens $EDITOR with a temp file for composing a prompt.
func (m Model) openEditorPrompt() tea.Cmd {
	editor := resolveEditor()

	// Create temp file
	tmpFile, err := os.CreateTemp("", "nagare-prompt-*.md")
	if err != nil {
		log.Error("editor prompt: %v", err)
		return nil
	}
	tmpFile.Close()
	tmpPath := tmpFile.Name()

	c := exec.Command(editor, tmpPath)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return editorDoneMsg{path: tmpPath, err: err}
	})
}

// openConfigEditor opens $EDITOR with the config file.
func (m Model) openConfigEditor() tea.Cmd {
	editor := resolveEditor()

	cfgPath := config.DefaultPath()
	// Ensure file exists
	os.MkdirAll(filepath.Dir(cfgPath), 0755)
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		// Write defaults
		cfg := config.Default()
		config.Save(cfg)
	}

	c := exec.Command(editor, cfgPath)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return configEditDoneMsg{err: err}
	})
}

// --- Commands ---

func doScan(statesDir string) tea.Cmd {
	return func() tea.Msg {
		paneStates := state.LoadStatesByPaneID(statesDir)
		cwdStates := state.LoadAllStates(statesDir)
		sessions := tmux.ScanSessions(paneStates, cwdStates)
		return SessionsUpdatedMsg(sessions)
	}
}

func doPreviewTick() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return tickPreviewMsg{} })
}

func (m Model) doPreview() tea.Cmd {
	if len(m.filtered) == 0 {
		return func() tea.Msg { return PreviewUpdatedMsg("") }
	}

	// Grid view: capture all visible sessions in background
	if m.viewMode == GridView {
		sessions := make([]models.Session, len(m.filtered))
		copy(sessions, m.filtered)
		return func() tea.Msg {
			previews := make(map[string]string)
			for _, s := range sessions {
				target := tmux.PaneTarget(s.SessionName, s.WindowIndex, s.PaneIndex)
				previews[target] = CapturePreview(s.SessionName, s.WindowIndex, s.PaneIndex)
			}
			return gridPreviewsMsg(previews)
		}
	}

	// List view: capture only the selected session
	s := m.filtered[m.cursor]
	return func() tea.Msg {
		content := CapturePreview(s.SessionName, s.WindowIndex, s.PaneIndex)
		return PreviewUpdatedMsg(content)
	}
}

// --- Saved session merging ---

// mergeSavedSessions appends registry sessions that aren't currently running.
func (m *Model) mergeSavedSessions() {
	active := make(map[string]bool)
	for _, s := range m.sessions {
		active[s.SessionName] = true
	}
	for _, rs := range m.registry.ListAll() {
		if active[rs.Name] {
			continue
		}
		m.sessions = append(m.sessions, models.Session{
			Name:        rs.Name,
			SessionName: rs.Name,
			Path:        rs.Path,
			Status:      models.StatusSaved,
			AgentType:   models.AgentType(rs.Agent),
		})
	}
}

func (m *Model) countSaved() int {
	n := 0
	for _, s := range m.sessions {
		if s.Status == models.StatusSaved {
			n++
		}
	}
	return n
}

// --- Filtering & sorting ---

func (m *Model) applyFilter() {
	m.filterGen++
	// Remember which session the cursor points at so we can restore the
	// selection after rebuilding the filtered list. Without this, a background
	// scan (every 2s) re-sorts and the cursor index silently slides onto a
	// different session — making Ctrl+y approvals land on (or miss) the
	// wrong target.
	prevKey := ""
	if s, ok := m.selectedSession(); ok {
		prevKey = sessionKey(s)
	}

	// Start with visible sessions (hide saved unless toggled)
	visible := m.sessions
	if !m.showSaved {
		visible = make([]models.Session, 0, len(m.sessions))
		for _, s := range m.sessions {
			if s.Status != models.StatusSaved {
				visible = append(visible, s)
			}
		}
	}

	query := m.query()
	queryChanged := query != m.lastQuery
	m.lastQuery = query

	if query != "" {
		// Build search targets: "name path" for each session
		targets := make([]string, len(visible))
		for i, s := range visible {
			targets[i] = s.Name + " " + s.Path
		}

		matches := fuzzy.Find(query, targets)
		m.filtered = make([]models.Session, len(matches))
		for i, match := range matches {
			m.filtered[i] = visible[match.Index]
		}
	} else if m.viewMode == GridView && len(m.gridOrder) > 0 {
		// Grid view with a live snapshot: preserve cell positions so scans
		// don't shuffle the grid under the user's cursor. A fresh snapshot
		// is taken on Tab-into-grid and on Ctrl+o sort cycles.
		m.applyGridOrder(visible)
	} else {
		m.filtered = make([]models.Session, len(visible))
		copy(m.filtered, visible)
		m.sortFiltered()
		if m.viewMode == GridView {
			m.snapshotGridOrder()
		}
	}

	// Cursor resolution: if the user just changed the query, jump to the top
	// (best fuzzy match). Otherwise, follow the previously-selected session to
	// its new index so background scans don't drift the selection.
	if queryChanged {
		m.cursor = 0
	} else if prevKey != "" {
		for i, s := range m.filtered {
			if sessionKey(s) == prevKey {
				m.cursor = i
				break
			}
		}
	}

	if m.cursor >= len(m.filtered) {
		m.cursor = max(0, len(m.filtered)-1)
	}
}

// sortFiltered orders m.filtered into the order it is displayed in. Sessions
// sharing a tmux session name are kept contiguous so the list view can put a
// single header above them, and each group takes the position of its most
// urgent member — a waiting worktree lifts its whole repo rather than being
// buried inside a quiet group.
//
// A lone session is a group of one and is its own representative, so a list
// with nothing to group sorts exactly as a flat list always did.
func (m *Model) sortFiltered() {
	// Pre-build starred set to avoid per-comparison registry lookups
	starred := make(map[string]bool)
	for _, s := range m.filtered {
		starred[s.Name] = m.isStarred(s.Name)
	}

	// less is the comparator the flat list has always used: stars first, then
	// the active sort mode.
	less := func(a, b models.Session) bool {
		if sa, sb := starred[a.Name], starred[b.Name]; sa != sb {
			return sa
		}
		switch m.sortMode {
		case SortByName:
			return a.Name < b.Name
		case SortByAgent:
			return a.AgentType < b.AgentType
		default: // SortByStatus
			return statusOrder(a.Status) < statusOrder(b.Status)
		}
	}

	// Urgency is independent of the active sort mode: a waiting child has to
	// lift its group even when sorting by name.
	moreUrgent := func(a, b models.Session) bool {
		if sa, sb := starred[a.Name], starred[b.Name]; sa != sb {
			return sa
		}
		return statusOrder(a.Status) < statusOrder(b.Status)
	}

	var keys []string
	groups := make(map[string][]models.Session)
	for _, s := range m.filtered {
		key := groupKeyOf(s)
		if _, seen := groups[key]; !seen {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], s)
	}

	reps := make(map[string]models.Session, len(groups))
	for key, members := range groups {
		sort.SliceStable(members, func(i, j int) bool { return less(members[i], members[j]) })
		groups[key] = members

		rep := members[0]
		for _, s := range members[1:] {
			if moreUrgent(s, rep) {
				rep = s
			}
		}
		reps[key] = rep
	}

	sort.SliceStable(keys, func(i, j int) bool {
		a, b := reps[keys[i]], reps[keys[j]]
		if less(a, b) {
			return true
		}
		if less(b, a) {
			return false
		}
		return keys[i] < keys[j] // stable tie-break so the list never jitters
	})

	ordered := make([]models.Session, 0, len(m.filtered))
	for _, key := range keys {
		ordered = append(ordered, groups[key]...)
	}
	m.filtered = ordered
}

func statusOrder(s models.SessionStatus) int {
	switch s {
	case models.StatusWaitingInput:
		return 0
	case models.StatusRunning:
		return 1
	case models.StatusIdle:
		return 2
	case models.StatusDead:
		return 3
	case models.StatusSaved:
		return 4
	default:
		return 5
	}
}

// --- View rendering ---

// renderStats renders the header line above the search box. Each count is
// tinted with the same color as its status dot, so the eye can read "is
// anything waiting on me?" from color alone without parsing the numbers.
// Zero counts are omitted rather than rendered as "0 waiting" noise.
func (m Model) renderStats(live, waiting, running, saved int) string {
	c := theme.Current().Colors

	shown := live
	if m.showSaved {
		shown += saved
	}

	parts := []string{
		lipgloss.NewStyle().Foreground(c.Foreground).Render(fmt.Sprintf("%d sessions", shown)),
	}
	count := func(n int, label string, status models.SessionStatus) {
		if n == 0 {
			return
		}
		styled := lipgloss.NewStyle().Foreground(lipgloss.Color(models.StatusColor(status)))
		parts = append(parts, styled.Render(fmt.Sprintf("%d %s", n, label)))
	}
	count(waiting, "waiting", models.StatusWaitingInput)
	count(running, "running", models.StatusRunning)
	if saved > 0 && !m.showSaved {
		parts = append(parts, mutedStyle().Render(fmt.Sprintf("%d saved", saved)))
	}

	sep := mutedStyle().Render(" · ")
	return " " + strings.Join(parts, sep)
}

// sidebarStats is renderStats for focus mode's narrow sidebar, where the full
// sentence wraps onto a second row. Counts become dots in the status colours —
// the same dots the rows below carry — so they read without a legend.
func sidebarStats(live, waiting, running, width int) string {
	c := theme.Current().Colors
	noun := "agents"
	if live == 1 {
		noun = "agent"
	}
	left := lipgloss.NewStyle().Foreground(c.Foreground).Render(fmt.Sprintf("%d %s", live, noun))
	var right []string
	if waiting > 0 {
		right = append(right, lipgloss.NewStyle().Foreground(lipgloss.Color(models.StatusColor(models.StatusWaitingInput))).
			Render(fmt.Sprintf("● %d", waiting)))
	}
	if running > 0 {
		right = append(right, lipgloss.NewStyle().Foreground(lipgloss.Color(models.StatusColor(models.StatusRunning))).
			Render(fmt.Sprintf("● %d", running)))
	}
	r := strings.Join(right, "  ")
	gap := width - 1 - lipgloss.Width(left) - lipgloss.Width(r) - rowGutter
	if gap < 1 {
		return " " + left
	}
	return " " + left + strings.Repeat(" ", gap) + r
}

// viewLeft renders the session list panel and reports which frame row each
// session was drawn on, for mouse hit-testing.
func (m Model) viewLeft(outerWidth, outerHeight int) (string, map[int]int) {
	return m.viewList(outerWidth, outerHeight, false)
}

// viewList renders the session list. As focus mode's sidebar it drops the search
// box and takes the quiet border: keystrokes land in the terminal beside it, and
// the gradient border belongs to wherever they land.
func (m Model) viewList(outerWidth, outerHeight int, sidebar bool) (string, map[int]int) {
	innerWidth := outerWidth - 4
	if innerWidth < 10 {
		innerWidth = 10
	}

	// Dashboard stats. Counts describe what the list actually shows: saved
	// sessions are hidden unless toggled, so folding them into "N sessions"
	// made the header disagree with the rows underneath it (34 sessions,
	// three visible). Saved sessions get their own muted count instead.
	live, waiting, running, saved := 0, 0, 0, 0
	for _, s := range m.sessions {
		switch s.Status {
		case models.StatusSaved:
			saved++
		case models.StatusWaitingInput:
			waiting++
		case models.StatusRunning:
			running++
		}
		if s.Status != models.StatusSaved {
			live++
		}
	}

	// Search input (always active)
	if m.renameMode {
		m.searchInput.Prompt = " Name task: "
	} else if m.worktreeMode {
		m.searchInput.Prompt = " New worktree: "
	} else {
		m.searchInput.Prompt = " > "
	}
	// Without a width the input shows only the first character of its
	// placeholder — a lone "s" that read as a query nobody had typed.
	m.searchInput.SetWidth(max(innerWidth-lipgloss.Width(m.searchInput.Prompt)-1, 1))

	// The header is wrapped here rather than left to the panel style to wrap on
	// its own, so its height — and therefore both the list's height and its
	// position on screen — are known exactly.
	//
	// This also fixes a latent bug. listHeight used to assume a four-line
	// header, but the stats line wraps to two or three lines on a narrow panel,
	// which pushed the last rows down through the bottom border.
	wrap := lipgloss.NewStyle().Width(innerWidth)
	stats := m.renderStats(live, waiting, running, saved)
	if sidebar {
		stats = sidebarStats(live, waiting, running, innerWidth)
	}
	header := []string{wrap.Render(stats), ""}
	if !sidebar {
		header = append(header, wrap.Render(m.statusLine()), "")
	} else if msg := m.statusMessage(); msg != "" {
		header = append(header, wrap.Render(msg), "")
	}
	headerHeight := 0
	for _, line := range header {
		headerHeight += lipgloss.Height(line)
	}

	// In lipgloss v2, Width/Height are the TOTAL rendered size — border and
	// padding included. (v1 excluded the border, which is why the old
	// Height(outerHeight-2) calls here left the panel two rows short of the
	// terminal and bled the backdrop through at the bottom.)
	// Content area = outerHeight - border(2) - vertical padding(2).
	listHeight := outerHeight - 4 - headerHeight
	if listHeight < 1 {
		listHeight = 1
	}

	m.compactRows = sidebar
	list, rowAt := m.renderListView(innerWidth, listHeight)

	// Frame row of the list's first line: top border, top padding, then header.
	listTop := 2 + headerHeight
	sessionAt := make(map[int]int, len(rowAt))
	for offset, idx := range rowAt {
		sessionAt[listTop+offset] = idx
	}

	body := strings.Join(header, "\n") + "\n" + list

	// The list is where keystrokes land, so it uses the gradient-bordered
	// primary panel. Other panels (preview, details, empty states) stay
	// on the quieter Border color.
	style := primaryPanelStyle()
	if sidebar {
		style = panelStyle()
	}
	return fitBox(style, outerWidth, outerHeight).
		Render(onPlane(body, surfaceBg())), sessionAt
}

// renderListView renders the visible window of session rows, and reports which
// line of its own output each session landed on. The caller offsets those into
// frame coordinates; keeping the mapping here means the scroll window is
// computed exactly once, by the code that draws it.
func (m Model) renderListView(width, height int) (string, map[int]int) {
	if len(m.filtered) == 0 {
		return mutedStyle().Render("  No sessions found"), nil
	}

	// Rows carry group headers alongside sessions, so scrolling works in row
	// space while the cursor keeps indexing sessions.
	rows := buildRows(m.filtered)
	cursorRow := 0
	for i, r := range rows {
		if r.SessionIdx == m.cursor {
			cursorRow = i
			break
		}
	}

	start := 0
	if cursorRow >= height {
		start = cursorRow - height + 1
	}
	end := start + height
	if end > len(rows) {
		end = len(rows)
	}

	c := theme.Current().Colors

	rowAt := make(map[int]int, end-start)
	var lines []string
	for ri := start; ri < end; ri++ {
		row := rows[ri]
		if row.SessionIdx == spacerRow {
			lines = append(lines, strings.Repeat(" ", width))
			continue
		}
		if row.SessionIdx < 0 {
			lines = append(lines, m.renderGroupHeader(row, width))
			continue
		}
		rowAt[len(lines)] = row.SessionIdx

		i := row.SessionIdx
		s := m.filtered[i]
		// Selection: tint the row background (crush / lazygit / gh-dash
		// convention) — no caret or gutter rune. The tint colour is per-theme
		// (SelBg), so every theme controls exactly how loud its selection reads.
		//
		// Every segment below carries this background itself. That is not a style
		// choice: each segment's own styling ends with a full SGR reset, so a
		// background applied around the finished row would survive only as far as
		// the first reset. Applying it per segment used to be done by wrapping
		// each one in a second Render, which both cost nine extra Render calls a
		// row and still left a gap — the name and branch were rendered without it,
		// so the selection tint broke either side of the name.
		// The selection tint crossfades between the row the cursor left and the one
		// it arrived at, so the highlight reads as travelling rather than jumping.
		// Outside a slide this is the plain full-or-nothing it has always been.
		rowBg := c.Surface
		if t := m.slide.tintFor(i, m.cursor); t > 0 {
			rowBg = theme.Mix(c.Surface, c.SelBg, t)
		}
		// A row that just changed state fades back from a tint, so a glance catches
		// what happened even after the status dot has settled.
		rowBg = flashTint(rowBg, m.flashes[sessionKey(s)], flashRowDepth)
		bg := lipgloss.NewStyle().Background(rowBg)

		dot := statusDotOn(s.Status, m.breath, rowBg)

		star := ""
		if m.isStarred(s.Name) {
			star = "★ "
		}
		starStyled := bg.Foreground(c.Warning).Render(star)

		// Row layout starts with status, tree prefix, and an explicit agent
		// badge. Keeping the badge next to the name makes mixed Claude Code and
		// Codex sessions distinguishable at a glance; their generated pane names
		// are too similar to carry that responsibility by themselves.
		// A grouped child is indented under its header and shows only its own
		// name — the repo is on the header, so the prefix is not repeated.
		prefix := ""
		if row.Glyph != "" {
			prefix = row.Glyph + " "
		}
		agent := listAgentLabel(s.AgentType) + " "
		agentStyled := bg.
			Foreground(lipgloss.Color(models.AgentColor(s.AgentType))).
			Bold(true).
			Render(agent)

		// The branch keeps the right-hand column; the compact agent badge is part
		// of the left cluster so both remain visible on ordinary pane widths.
		branch := branchFor(row.Label, s.Details.Worktree, s.Details.GitBranch)
		// The sidebar is too narrow to afford a branch column: it would truncate
		// the names, which are what the sidebar is for, and the focused agent's
		// branch is already in the terminal's title.
		if m.compactRows {
			branch = ""
		}

		// Columns the row spends on anything that is not text: leading space,
		// the dot, the space after it, the tree prefix, agent badge, star, and the
		// trailing gutter.
		fixed := 1 + lipgloss.Width(dot) + 1 + lipgloss.Width(prefix) +
			lipgloss.Width(agentStyled) + lipgloss.Width(star) + rowGutter
		avail := width - fixed
		if avail < minNameWidth {
			avail = minNameWidth
		}
		nameW, branchW := splitRowWidth(avail, lipgloss.Width(row.Label), lipgloss.Width(branch))
		name := truncate(row.Label, nameW)
		if branchW > 0 {
			branch = truncate(branch, branchW)
		} else {
			branch = ""
		}
		branchStyled := bg.Foreground(c.Muted).Render(branch)

		// Bold the selected row's name for an extra hierarchy cue beyond the tint.
		nameStyle := bg.Foreground(c.Foreground)
		if i == m.cursor {
			nameStyle = nameStyle.Bold(true)
		}
		// Highlight matches against the label actually shown: a query that only
		// hit the repo portion has nothing to mark on the child, and the header
		// above carries that text.
		nameStyled := renderNameWithMatches(name, row.Label, m.query(), nameStyle, c.Accent)
		prefixStyled := bg.Foreground(c.Muted).Render(prefix)

		gap := width - 1 - lipgloss.Width(dot) - 1 - lipgloss.Width(prefixStyled) -
			lipgloss.Width(agentStyled) - lipgloss.Width(nameStyled) -
			lipgloss.Width(starStyled) - lipgloss.Width(branchStyled) - rowGutter
		if gap < 1 {
			gap = 1
		}

		// Every segment already carries rowBg, so the row is assembled by plain
		// concatenation and padded once. onPlane re-establishes the tint after the
		// resets the segments leave behind.
		// With several tiles open, the agents on screen carry a thin accent bar
		// in the gutter, so the sidebar also says which ones are tiled.
		gutter := " "
		if m.focus.on && m.focus.n > 1 && m.focus.onScreen(sessionKey(s)) {
			gutter = bg.Foreground(c.Accent).Render("▎")
		}
		line := gutter + dot + " " + prefixStyled + agentStyled + nameStyled +
			strings.Repeat(" ", gap) + starStyled + branchStyled +
			strings.Repeat(" ", rowGutter)
		lines = append(lines, bg.Width(width).Render(onPlane(line, rowBg)))
	}
	return strings.Join(lines, "\n"), rowAt
}

// listAgentLabel is a one-cell sigil: color carries most of the identity, while
// the letter keeps it readable without color and leaves room for names/branches.
func listAgentLabel(agent models.AgentType) string {
	switch agent {
	case models.AgentClaude:
		return "C"
	case models.AgentCodex:
		return "X"
	case models.AgentOpenCode:
		return "O"
	case models.AgentGemini:
		return "G"
	case models.AgentCrush:
		return "♥"
	case models.AgentPi:
		return "π"
	default:
		return "?"
	}
}

// renderGroupHeader renders the repo line above a group of sessions. It carries
// no status dot and no agent badge — those belong to the sessions underneath —
// just the repo name and how many sessions it holds.
func (m Model) renderGroupHeader(row listRow, width int) string {
	c := theme.Current().Colors

	noun := "sessions"
	if row.Count == 1 {
		noun = "session"
	}
	label := fmt.Sprintf("%d %s", row.Count, noun)
	if m.compactRows {
		label = fmt.Sprint(row.Count)
	}
	count := mutedStyle().Render(label)
	fixed := 1 + 1 + lipgloss.Width(count) + rowGutter
	maxName := width - fixed
	if maxName < minNameWidth {
		maxName = minNameWidth
	}
	name := lipgloss.NewStyle().
		Foreground(c.Secondary).
		Bold(true).
		Render(truncate(row.Group, maxName))

	gap := width - 1 - lipgloss.Width(name) - lipgloss.Width(count) - rowGutter
	if gap < 1 {
		gap = 1
	}
	// Per-segment background, for the same reason as the session rows: an inner
	// reset would drop the tint for everything after it.
	bg := lipgloss.NewStyle().Background(c.Surface)
	var content strings.Builder
	for _, part := range []string{" ", name, strings.Repeat(" ", gap), count, strings.Repeat(" ", rowGutter)} {
		content.WriteString(bg.Render(part))
	}
	return bg.Width(width).Render(content.String())
}

func (m Model) viewRight(outerWidth, outerHeight int) string {
	if len(m.filtered) == 0 {
		return fitBox(panelStyle(), outerWidth, outerHeight).
			Render(onPlane(mutedStyle().Render("No session selected"), surfaceBg()))
	}

	innerWidth := outerWidth - 4
	if innerWidth < 10 {
		innerWidth = 10
	}

	s := m.filtered[m.cursor]
	c := theme.Current().Colors

	// Detail section
	label := lipgloss.NewStyle().Foreground(c.Muted)
	val := lipgloss.NewStyle().Foreground(c.Foreground)
	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(models.StatusColor(s.Status)))
	agentStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(models.AgentColor(s.AgentType))).
		Background(lipgloss.Color(models.AgentBgColor(s.AgentType))).
		Padding(0, 1)

	// Build info column.
	// The path is emitted as an OSC 8 hyperlink, so a terminal that supports
	// them (kitty, WezTerm, Ghostty, iTerm2, foot, recent VTE) makes it
	// clickable and opens the working tree in the desktop file manager. The
	// escape is inert everywhere else — the text renders identically, so there
	// is no capability to detect and no fallback to write.
	var info strings.Builder
	pathStyled := subtleStyle().Hyperlink("file://" + s.Path).Render(s.Path)
	info.WriteString(fmt.Sprintf("  %s  %s\n", label.Render("Path  "), pathStyled))
	info.WriteString(fmt.Sprintf("  %s  %s\n", label.Render("Agent "), agentStyle.Render(models.AgentLabel(s.AgentType))))
	info.WriteString(fmt.Sprintf("  %s  %s\n", label.Render("Status"), statusStyle.Render(models.StatusLabel(s.Status))))

	if s.Details.GitBranch != "" {
		info.WriteString(fmt.Sprintf("  %s  %s\n", label.Render("Branch"), val.Render(s.Details.GitBranch)))
	}
	// Name the repo a worktree belongs to — the display name only carries the
	// worktree, so without this a worktree pane never says what it forked from.
	if s.Details.RepoName != "" && s.Details.Worktree != "" {
		repo := fmt.Sprintf("%s (worktree %s)", s.Details.RepoName, s.Details.Worktree)
		info.WriteString(fmt.Sprintf("  %s  %s\n", label.Render("Repo  "), subtleStyle().Render(repo)))

		w := m.workFor(s)
		state := "clean"
		if w.Dirty > 0 {
			state = fmt.Sprintf("%d uncommitted", w.Dirty)
		}
		if w.HasUpstream && w.Ahead > 0 {
			state += fmt.Sprintf(" · %d ahead", w.Ahead)
		}
		info.WriteString(fmt.Sprintf("  %s  %s\n", label.Render("Work  "), val.Render(state)))
	}
	// Two agents in one working tree will overwrite each other's edits.
	if n := sharedPaths(m.sessions)[s.Path]; n > 1 {
		warn := lipgloss.NewStyle().Foreground(theme.Current().Colors.Warning)
		info.WriteString(fmt.Sprintf("  %s  %s\n", label.Render("Shared"),
			warn.Render(fmt.Sprintf("%d agents in this directory", n))))
	}
	if s.Details.LastActivity != "" {
		info.WriteString(fmt.Sprintf("  %s  %s\n", label.Render("Active"), subtleStyle().Render(formatTimeAgo(s.Details.LastActivity))))
	}
	// Recent activity, as a braille trace: what this agent has been doing, which
	// the status dot cannot say. Only worth drawing once there is more than a
	// single sample to compare against.
	if hist := m.history[sessionKey(s)]; len(hist) > 1 {
		w := sparkWidth(innerWidth - 20)
		if w > 0 {
			info.WriteString(fmt.Sprintf("  %s  %s  %s\n", label.Render("Recent"),
				sparklineOn(hist, w, c.Surface), sparkLegend()))
		}
	}
	if s.LastMessage != "" {
		msg := s.LastMessage
		maxLen := innerWidth - 30
		if maxLen > 0 && len(msg) > maxLen {
			msg = msg[:maxLen] + "..."
		}
		info.WriteString(fmt.Sprintf("  %s  %s\n", label.Render("Last  "), mutedStyle().Render(msg)))
	}

	// Combine art on the left with info on the right
	var detail strings.Builder
	art := renderAgentArt(s.AgentType)
	if art != "" && innerWidth > 40 {
		artWidth := lipgloss.Width(art)
		infoWidth := innerWidth - artWidth - 2 // 2 for gap
		infoBlock := lipgloss.NewStyle().
			Width(infoWidth).
			Background(c.Surface).
			Render(titleStyle().Render(s.Name) + "\n\n" + info.String())
		gap := lipgloss.NewStyle().
			Width(2).
			Background(c.Surface).
			Render("")
		detail.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, art, gap, infoBlock))
	} else {
		detail.WriteString(titleStyle().Render(s.Name))
		detail.WriteString("\n\n")
		detail.WriteString(info.String())
	}

	// Size detail panel to fit its content exactly. lipgloss v2 Height is the
	// total rendered height, so it must cover the content plus this style's
	// vertical padding (2) and border (2).
	//
	// Wrap the content to the panel width *before* measuring it. Counting "\n"
	// in the unwrapped string counts string lines, not rendered rows, and a path
	// too long for a narrow panel occupies two rows while counting as one — which
	// left the panel a row short and let fitBox clip its bottom border away. The
	// wide branch above happened to be immune only because JoinHorizontal renders
	// the info block at a fixed width first, so its wraps were already real
	// newlines by the time they were counted.
	detailContent := lipgloss.NewStyle().Width(innerWidth).Render(detail.String())
	detailLines := lipgloss.Height(detailContent)
	detailOuter := detailLines + 4
	if detailOuter > outerHeight/2 {
		// Cap at half the panel and clamp content to fit.
		detailOuter = outerHeight / 2
		maxContent := detailOuter - 4
		if maxContent < 1 {
			maxContent = 1
		}
		ls := strings.Split(detailContent, "\n")
		if len(ls) > maxContent {
			detailContent = strings.Join(ls[:maxContent], "\n")
		}
	}
	if detailOuter < 6 {
		detailOuter = 6
	}

	detailStr := fitBox(panelStyle(), outerWidth, detailOuter).
		Render(onPlane(detailContent, c.Surface))

	// Preview section: gets the remaining height.
	// inner = previewOuter - border(2), no vertical padding on previewPanelStyle.
	previewOuter := outerHeight - detailOuter
	if previewOuter < 5 {
		previewOuter = 5
	}

	previewContent := m.preview
	if previewContent == "" {
		previewContent = mutedStyle().Render("No preview available")
	} else {
		maxLines := previewOuter - 2
		if maxLines < 1 {
			maxLines = 1
		}
		lines := strings.Split(previewContent, "\n")
		// Trim trailing empty lines
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		// Take the bottom portion
		if len(lines) > maxLines {
			lines = lines[len(lines)-maxLines:]
		}
		for i, line := range lines {
			if ansi.StringWidth(line) > innerWidth {
				lines[i] = ansi.Truncate(line, innerWidth, "")
			}
		}
		previewContent = strings.Join(lines, "\n")
	}

	previewStr := fitBox(previewPanelStyle(), outerWidth, previewOuter).
		Render(onPlane(previewContent, c.Surface))

	return lipgloss.JoinVertical(lipgloss.Left, detailStr, previewStr)
}

// --- Grid view ---

func gridColumns(count int) int {
	if count <= 2 {
		return 1
	}
	if count <= 4 {
		return 2
	}
	return 3
}

// viewGrid renders the full-screen card grid and reports each card's bounds,
// for mouse hit-testing.
func (m Model) viewGrid(totalWidth, totalHeight int) (string, []cardHit) {
	c := theme.Current().Colors

	if len(m.filtered) == 0 {
		return fitBox(panelStyle(), totalWidth, totalHeight).
			Render(onPlane(mutedStyle().Render("No sessions found"), surfaceBg())), nil
	}

	// Search bar at top (1 line + 1 blank line = 2 lines)
	searchBar := m.searchInput.View()

	cols := gridColumns(len(m.filtered))
	cellWidth := totalWidth / cols
	numRows := (len(m.filtered) + cols - 1) / cols

	// Card rows get everything but the search bar line. The previous arithmetic
	// subtracted a per-row gap that does not exist — rows are joined directly —
	// and then clamped the result up to a minimum without reducing how many rows
	// it drew, so on a short terminal the grid was taller than the frame and the
	// bottom cards were silently cut off by the frame clamp in View.
	avail := totalHeight - 1
	if avail < minCellHeight {
		avail = minCellHeight
	}
	cellHeight := avail / numRows
	if cellHeight < minCellHeight {
		cellHeight = minCellHeight
	}

	// When every row cannot fit at the minimum card height, show fewer rows and
	// scroll rather than drawing cards that get clipped. Same principle as the
	// list view: a partly drawn row is worse than an absent one.
	visibleRows := max(min(avail/cellHeight, numRows), 1)
	startRow := 0
	if cursorRow := m.cursor / cols; cursorRow >= visibleRows {
		startRow = cursorRow - visibleRows + 1
	}

	// Build rows of cells
	var rows []string
	var cards []cardHit
	for i := startRow * cols; i < len(m.filtered) && len(rows) < visibleRows; i += cols {
		var cells []string
		for j := 0; j < cols && i+j < len(m.filtered); j++ {
			idx := i + j
			s := m.filtered[idx]

			// Content width inside the card's border (2) and its Padding(1), which
			// is one cell per side and so two in total — not the four the previous
			// arithmetic subtracted, which left the separator two cells short of
			// the card and the preview needlessly truncated.
			innerWidth := cellWidth - 4
			if innerWidth < 10 {
				innerWidth = 10
			}

			// The agent art sits to the right of the header block, so the header's
			// real budget is the text column beside it — not the full card width.
			// Sizing the header against innerWidth and then rendering it into the
			// narrower column is what made it wrap.
			art := renderAgentArtSmall(s.AgentType)
			artWidth := lipgloss.Width(art)
			textWidth := innerWidth
			if art != "" && innerWidth > 30 {
				textWidth = innerWidth - artWidth - 1
			}

			// Header: status dot + name + agent badge
			dot := statusDotOn(s.Status, m.breath, c.Surface)
			statusLabel := lipgloss.NewStyle().Foreground(lipgloss.Color(models.StatusColor(s.Status))).Render(models.StatusLabel(s.Status))
			agentBadge := lipgloss.NewStyle().
				Foreground(lipgloss.Color(models.AgentColor(s.AgentType))).
				Background(lipgloss.Color(models.AgentBgColor(s.AgentType))).
				Padding(0, 1).
				Render(models.AgentLabel(s.AgentType))

			// The header stays on one row: the name is truncated to whatever the
			// badge and status leave, exactly as list rows do. Letting it wrap made
			// a narrow card taller than its cell, and a name broken across two rows
			// is unreadable anyway.
			// An activity trace pinned to the right of the header. Grid view is where
			// it earns the most: a wall of cards is exactly the situation where
			// knowing which agent has been busy matters, and the status dot alone
			// cannot say.
			// 5 = leading space, two separating spaces, and the double space
			// before the status label.
			chrome := lipgloss.Width(dot) + lipgloss.Width(agentBadge) +
				lipgloss.Width(statusLabel) + 5

			// The trace yields to the name. A card narrow enough that both cannot fit
			// gets no trace: forcing one in left the name at its minimum and the
			// header still over budget, which wrapped it and cost the card a row.
			spark := ""
			const sparkGap = 2
			if hist := m.history[sessionKey(s)]; len(hist) > 1 {
				room := textWidth - chrome - minNameWidth - sparkGap
				if w := sparkWidth(min(room, textWidth/4)); w > 0 {
					spark = sparklineOn(hist, w, c.Surface)
				}
			}
			sparkRoom := 0
			if spark != "" {
				sparkRoom = lipgloss.Width(spark) + sparkGap
			}

			header := fmt.Sprintf(" %s %s %s  %s", dot,
				truncate(s.Name, max(textWidth-chrome-sparkRoom, minNameWidth)),
				agentBadge, statusLabel)
			if spark != "" {
				pad := max(textWidth-lipgloss.Width(header)-lipgloss.Width(spark), 1)
				header += strings.Repeat(" ", pad) + spark
			}

			// Meta line: path + git branch. Allowed to wrap — seeing the whole path
			// is worth a row — with the card's preview budget derived from however
			// tall it actually ends up.
			meta := mutedStyle().Render(fmt.Sprintf("   %s", s.Path))
			if s.Details.GitBranch != "" {
				meta += mutedStyle().Render(fmt.Sprintf("  (%s)", s.Details.GitBranch))
			}

			topBlock := header + "\n" + meta
			if art != "" && innerWidth > 30 {
				textCol := lipgloss.NewStyle().Width(textWidth).Background(c.Surface).Render(topBlock)
				gap := lipgloss.NewStyle().Width(1).Background(c.Surface).Render("")
				topBlock = lipgloss.JoinHorizontal(lipgloss.Top, textCol, gap, art)
			}

			separator := fadingRule(innerWidth, c.Border, c.Surface)

			// Measure the header block instead of assuming it is two rows.
			//
			// It is two rows only when the meta line fits: a long path plus a long
			// branch wraps it to three, which made the card one row taller than
			// cellHeight — and fitBox then clipped that row off, taking the card's
			// bottom border with it. An open-bottomed card is what the bug looked
			// like from the outside.
			// Wrap the header block to the card width first, so what follows counts
			// rendered rows rather than string lines — one line of meta can occupy
			// two rows, and clamping the string would not have shortened it.
			topBlock = lipgloss.NewStyle().Width(innerWidth).Render(topBlock)
			topLines := strings.Split(topBlock, "\n")

			// Card budget: border (2), padding (2), separator (1).
			const cardChrome = 2 + 2 + 1
			// Leave at least one row of preview, clamping the header if even that
			// does not fit — a truncated header beats a card with no bottom edge.
			if maxTop := cellHeight - cardChrome - 1; maxTop > 0 && len(topLines) > maxTop {
				topLines = topLines[:maxTop]
				topBlock = strings.Join(topLines, "\n")
			}
			topHeight := len(topLines)

			previewHeight := cellHeight - cardChrome - topHeight
			if previewHeight < 1 {
				previewHeight = 1
			}

			preview := m.getGridPreview(s, innerWidth, previewHeight)

			content := topBlock + "\n" + separator + "\n" + preview

			// The selected card takes the same gradient sweep as the focused
			// panel in list view, so "this is where you are" looks identical in
			// both modes. Unselected cards stay on quiet chrome.
			//
			// Nothing about a card animates. A trail on the card being left behind
			// and a flash on a card whose agent changed state were both tried and
			// both rejected: a card is a large object, and moving or lighting one
			// pulls the eye to the card rather than to what is written on it. The
			// status dot inside it still breathes, and in list view the row tint
			// still crossfades and flashes — a row is a thin enough thing that motion
			// reads as a hint rather than as an event.
			cellStyle := lipgloss.NewStyle().
				Background(c.Surface).
				Foreground(c.Foreground).
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(c.Border).
				BorderBackground(c.Surface).
				Padding(1)
			if idx == m.cursor {
				cellStyle = cellStyle.BorderForegroundBlend(c.GradientFrom, c.GradientTo)
			}
			cell := fitBox(cellStyle, cellWidth, cellHeight).Render(onPlane(content, c.Surface))

			// The card's bounds in frame coordinates: one row down for the search
			// bar, then whole cells from there, relative to the first visible row.
			x0, y0 := j*cellWidth, 1+(i/cols-startRow)*cellHeight
			cards = append(cards, cardHit{
				index: idx,
				area:  image.Rect(x0, y0, x0+cellWidth, y0+cellHeight),
			})

			cells = append(cells, cell)
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, cells...))
	}

	// Unlike list view, grid view composes straight onto the canvas — the cards
	// are the panels and everything around them is bare ground. So the search
	// bar and the bottom padding have to carry the canvas plane themselves: a
	// fg-only search input and a bare "\n" pad each leave a whole row on the
	// terminal's own background, which is a visible seam now that the canvas is
	// a plane rather than whatever the terminal happened to be.
	canvasLine := lipgloss.NewStyle().Background(canvasBg()).Width(totalWidth)

	out := []string{canvasLine.Render(onPlane(" "+searchBar, canvasBg()))}
	// Card rows already carry their own plane, so they are only padded out to
	// the full width — for when cols does not divide totalWidth evenly. Running
	// onPlane over them would inject the canvas *inside* the cards and undo
	// their surface.
	for _, row := range rows {
		out = append(out, strings.Split(canvasLine.Render(row), "\n")...)
	}
	// Pad to totalHeight so the help bar (appended by View) lands at the bottom.
	for len(out) < totalHeight {
		out = append(out, canvasLine.Render(""))
	}
	return strings.Join(out, "\n"), cards
}

func (m Model) getGridPreview(s models.Session, width, height int) string {
	target := tmux.PaneTarget(s.SessionName, s.WindowIndex, s.PaneIndex)
	content := m.gridPreviews[target]
	if content == "" {
		return mutedStyle().Render("Loading...")
	}

	lines := strings.Split(content, "\n")
	// Trim trailing blank lines so the last real content lands at the bottom.
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	// Take the bottom portion so the last line of the stream is visible.
	if len(lines) > height {
		lines = lines[len(lines)-height:]
	}
	for i, line := range lines {
		if ansi.StringWidth(line) > width {
			lines[i] = ansi.Truncate(line, width, "")
		}
	}
	return strings.Join(lines, "\n")
}

// formatTimeAgo converts an ISO 8601 timestamp to a human-readable relative time.
func formatTimeAgo(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm ago", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
