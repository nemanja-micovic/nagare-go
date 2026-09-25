package picker

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nemke/nagare-go/internal/models"
)

// lipglossHeight is a thin alias so the intent reads clearly in assertions.
func lipglossHeight(s string) int { return lipgloss.Height(s) }

func footerText(m Model, width int) string {
	return ansi.Strip(helpBar(m, width))
}

func footerKeys(m Model) []string {
	keys := make([]string, 0, 8)
	for _, h := range hintsFor(m) {
		keys = append(keys, h.key)
	}
	return keys
}

func hasKey(m Model, key string) bool {
	for _, k := range footerKeys(m) {
		if k == key {
			return true
		}
	}
	return false
}

func footerModel(t *testing.T, sessions []models.Session) Model {
	t.Helper()
	return driveModel(t, NewForTest(),
		tea.WindowSizeMsg{Width: 120, Height: 30},
		SessionsUpdatedMsg(sessions),
	)
}

// TestFooterIsAlwaysOneLine is the reason the bar was rewritten: the old one
// wrapped to two lines and stole a row from the session list. Narrow terminals
// are the case that used to break.
func TestFooterIsAlwaysOneLine(t *testing.T) {
	m := footerModel(t, mouseSessions())

	for _, width := range []int{20, 40, 60, 80, 120, 200} {
		bar := helpBar(m, width)
		if got := lipglossHeight(bar); got != 1 {
			t.Errorf("width %d: footer is %d lines, want 1\n%s", width, got, ansi.Strip(bar))
		}
		if got := ansi.StringWidth(strings.Split(bar, "\n")[0]); got != width {
			t.Errorf("width %d: footer renders %d cells wide", width, got)
		}
	}
}

// TestFooterAlwaysOffersAWayOutAndIn — however little room there is, the user
// must be able to see how to reach the rest of the keymap and how to quit.
func TestFooterAlwaysOffersAWayOutAndIn(t *testing.T) {
	m := footerModel(t, mouseSessions())

	for _, width := range []int{20, 30, 50, 120} {
		text := footerText(m, width)
		if !strings.Contains(text, "F1") {
			t.Errorf("width %d: footer does not mention F1\n%s", width, text)
		}
		if !strings.Contains(text, "Esc") {
			t.Errorf("width %d: footer does not mention Esc\n%s", width, text)
		}
	}
}

// TestFooterHidesKeysThatWouldDoNothing — advertising ^y on an idle session is
// what made the old bar untrustworthy.
func TestFooterHidesKeysThatWouldDoNothing(t *testing.T) {
	idle := footerModel(t, []models.Session{
		{Name: "idle", SessionName: "idle", Path: "/tmp/idle",
			Status: models.StatusIdle, AgentType: models.AgentClaude},
	})
	if hasKey(idle, "^y") {
		t.Error("idle session offers ^y Allow, which would do nothing")
	}

	waiting := footerModel(t, []models.Session{
		{Name: "waiting", SessionName: "waiting", Path: "/tmp/waiting",
			Status: models.StatusWaitingInput, AgentType: models.AgentClaude},
	})
	if !hasKey(waiting, "^y") {
		t.Error("a session waiting for input should offer ^y Allow")
	}
	if !hasKey(waiting, "^a") {
		t.Error("a session waiting for input should offer ^a Always")
	}
}

// TestFooterAdaptsToSavedSessions — a saved session is loaded, not jumped to,
// and cannot be unloaded or killed.
func TestFooterAdaptsToSavedSessions(t *testing.T) {
	// Saved sessions are filtered out until ^s reveals them, so the toggle has
	// to be on before one can be the selection at all.
	m := NewForTest()
	m.showSaved = true
	m = driveModel(t, m,
		tea.WindowSizeMsg{Width: 120, Height: 30},
		SessionsUpdatedMsg([]models.Session{
			{Name: "saved", Path: "/tmp/saved", Status: models.StatusSaved, AgentType: models.AgentClaude},
		}),
	)
	if _, ok := m.selectedSession(); !ok {
		t.Fatal("no saved session selected; the filter did not include it")
	}

	var enterLabel string
	for _, h := range hintsFor(m) {
		if h.key == "Enter" {
			enterLabel = h.label
		}
	}
	if enterLabel != "Load" {
		t.Errorf("Enter on a saved session is labelled %q, want \"Load\"", enterLabel)
	}
	if hasKey(m, "^w") {
		t.Error("a saved session offers ^w Unload, but there is no pane to unload")
	}
	if hasKey(m, "^x") {
		t.Error("a saved session offers ^x Kill, but there is nothing to kill")
	}
}

// TestFooterNamesWorktreeRemoval — Ctrl+x behaves differently on a worktree
// pane, so it should say so instead of springing an extra prompt.
func TestFooterNamesWorktreeRemoval(t *testing.T) {
	plain := footerModel(t, []models.Session{
		{Name: "plain", SessionName: "plain", Path: "/tmp/plain",
			Status: models.StatusIdle, AgentType: models.AgentClaude},
	})
	worktree := footerModel(t, []models.Session{
		{Name: "repo/wt", SessionName: "repo", Path: "/tmp/repo/wt",
			Status: models.StatusIdle, AgentType: models.AgentClaude,
			Details: models.SessionDetails{RepoName: "repo", Worktree: "wt"}},
	})

	labelFor := func(m Model, key string) string {
		for _, h := range hintsFor(m) {
			if h.key == key {
				return h.label
			}
		}
		return ""
	}

	if got := labelFor(plain, "^x"); got != "Kill" {
		t.Errorf("^x on a plain pane is labelled %q, want \"Kill\"", got)
	}
	if got := labelFor(worktree, "^x"); !strings.Contains(got, "remove") {
		t.Errorf("^x on a worktree pane is labelled %q, want it to mention removal", got)
	}
}

// TestFooterFollowsTheActiveMode — whichever overlay or input mode owns the
// keyboard owns the footer, and its keys replace the normal set entirely rather
// than being appended to it.
func TestFooterFollowsTheActiveMode(t *testing.T) {
	base := footerModel(t, mouseSessions())

	cases := []struct {
		name  string
		setup func(Model) Model
		want  []string
		gone  string
	}{
		{"help", func(m Model) Model { m.showHelp = true; return m }, []string{"Close"}, "Navigate"},
		{"theme picker", func(m Model) Model { m.showThemePick = true; return m }, []string{"Preview", "Cancel"}, "Navigate"},
		{"confirm", func(m Model) Model { m.confirmMode = true; return m }, []string{"Remove worktree", "Keep it"}, "Navigate"},
		{"prompt", func(m Model) Model { m.promptMode = true; return m }, []string{"Send", "Cancel"}, "Navigate"},
		{"rename", func(m Model) Model { m.renameMode = true; return m }, []string{"Save name", "Cancel"}, "Navigate"},
		{"new worktree", func(m Model) Model { m.worktreeMode = true; return m }, []string{"Create worktree"}, "Navigate"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := footerText(tc.setup(base), 120)
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("footer lacks %q\ngot: %s", want, text)
				}
			}
			if strings.Contains(text, tc.gone) {
				t.Errorf("footer still shows the normal-mode hint %q\ngot: %s", tc.gone, text)
			}
		})
	}
}

// TestFooterModesDoNotClaimF1OrEsc — inside a mode those keys mean something
// specific, so the generic "F1 More / Esc Quit" tail must not be appended and
// contradict the mode's own labels.
func TestFooterModesDoNotClaimF1OrEsc(t *testing.T) {
	m := footerModel(t, mouseSessions())
	m.confirmMode = true

	if text := footerText(m, 120); strings.Contains(text, "More") || strings.Contains(text, "Quit") {
		t.Errorf("confirm-mode footer advertises More/Quit\ngot: %s", text)
	}
}

// TestFooterHandlesAnEmptyList — with nothing selected there is no session to
// act on, and the footer must not offer session actions or panic.
func TestFooterHandlesAnEmptyList(t *testing.T) {
	m := footerModel(t, nil)

	for _, key := range []string{"Enter", "^y", "^w", "^x", "F2", "F3"} {
		if hasKey(m, key) {
			t.Errorf("empty list offers %s with nothing selected", key)
		}
	}
	if got := lipglossHeight(helpBar(m, 120)); got != 1 {
		t.Errorf("footer with an empty list is %d lines, want 1", got)
	}
}

// TestHelpOverlayFitsTheFrame guards the bug the animation work uncovered: the
// single-column screen ran to ~44 rows and was silently clipped on any shorter
// terminal. It also pinned the dialog's centered position to the top of the frame,
// which defeated the entry animation.
func TestHelpOverlayFitsTheFrame(t *testing.T) {
	for _, sz := range [][2]int{{200, 50}, {160, 40}, {120, 36}, {120, 30}, {100, 24}, {80, 22}} {
		w, h := sz[0], sz[1]
		o := helpOverlay(w, h)
		if got := lipgloss.Height(o); got > h {
			t.Errorf("%dx%d: help overlay is %d rows, taller than the frame", w, h, got)
		}
		if got := lipgloss.Width(o); got > w {
			t.Errorf("%dx%d: help overlay is %d cells wide, wider than the frame", w, h, got)
		}
	}
}

// TestHelpOverlayIsCenteredNotPinned — an overlay taller than the frame clamps to
// y=0, so this is what proves the fit is real rather than incidental.
func TestHelpOverlayIsCenteredNotPinned(t *testing.T) {
	const w, h = 160, 40
	area := overlayRect(w, h, helpOverlay(w, h), 0)
	if area.Min.Y <= 0 {
		t.Errorf("help overlay rests at y=%d; it is not being centered", area.Min.Y)
	}
}

// TestHelpOverlayCollapsesToOneColumn — a narrow dialog cannot hold two columns
// of key/description pairs without shredding the descriptions.
func TestHelpOverlayCollapsesToOneColumn(t *testing.T) {
	wide := ansi.Strip(helpOverlay(200, 60))
	narrow := ansi.Strip(helpOverlay(80, 60))

	// In two columns, a left-hand and a right-hand section title share a row.
	if !strings.Contains(wide, "Navigation") || !strings.Contains(wide, "Agent") {
		t.Fatal("wide help overlay is missing expected sections")
	}
	twoOnALine := false
	for _, line := range strings.Split(wide, "\n") {
		if strings.Contains(line, "Navigation") && strings.Contains(line, "Agent") {
			twoOnALine = true
		}
	}
	if !twoOnALine {
		t.Error("wide help overlay did not lay out two columns")
	}

	for _, line := range strings.Split(narrow, "\n") {
		if strings.Contains(line, "Navigation") && strings.Contains(line, "Agent") {
			t.Error("narrow help overlay still laid out two columns")
		}
	}
}

// TestHelpOverlayCoversEveryBinding — the screen is the full reference the footer
// defers to, so a binding that exists in keys.go and is not listed here is
// undiscoverable.
func TestHelpOverlayCoversEveryBinding(t *testing.T) {
	text := ansi.Strip(helpOverlay(200, 60))

	// Rendered spelling for each binding constant.
	want := map[string]string{
		keyApprove:       "Ctrl+y",
		keyApproveAlways: "Ctrl+a",
		keyToggleView:    "Tab",
		keyCycleTheme:    "Ctrl+t",
		keyHelp:          "F1",
		keyUnload:        "Ctrl+w",
		keyKillSession:   "Ctrl+x",
		keyStar:          "Ctrl+f",
		keyCycleSort:     "Ctrl+o",
		keyRename:        "F2",
		keyNewWorktree:   "F3",
		keyNewSession:    "Ctrl+n",
		keyQuickProto:    "Ctrl+r",
		keyInlinePrompt:  "Ctrl+l",
		keyEditPrompt:    "Ctrl+g",
		keyEditConfig:    "Ctrl+e",
		keyToggleSaved:   "Ctrl+s",
		keyNextAttention: "F4",
		keyMailbox:       "F5",
		keyMailCleanup:   "Ctrl+d",
	}
	for binding, shown := range want {
		if !strings.Contains(text, shown) {
			t.Errorf("help screen does not document %s (expected to see %q)", binding, shown)
		}
	}
}
