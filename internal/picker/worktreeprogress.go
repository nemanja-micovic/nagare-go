package picker

import (
	"fmt"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/nemke/nagare-go/internal/models"
	"github.com/nemke/nagare-go/internal/session"
	"github.com/nemke/nagare-go/internal/theme"
)

// worktreeCreatedMsg reports the result of the tmux and git work. It says
// nothing about the agent, which starts up afterwards on its own schedule.
type worktreeCreatedMsg struct {
	name string
	err  error
}

// pendingWorktree tracks a worktree from the keystroke that asked for it until
// its agent pane shows up in a scan.
//
// Waiting for the pane rather than for CreateWorktree to return is the whole
// point: nagare's own work finishes in a couple of hundred milliseconds, while
// `claude -w` takes seconds more to build the worktree and start. Returning
// early would clear the spinner while the screen still showed nothing.
type pendingWorktree struct {
	name     string
	deadline time.Time
}

// satisfiedBy reports whether the awaited pane has appeared.
func (p pendingWorktree) satisfiedBy(sessions []models.Session) bool {
	for _, s := range sessions {
		if s.Details.Worktree == p.name {
			return true
		}
	}
	return false
}

// expired reports whether the pane failed to appear in time.
func (p pendingWorktree) expired(now time.Time) bool {
	return now.After(p.deadline)
}

// worktreeWait is how long a pane gets to appear before nagare stops waiting.
// Generous, because it covers an agent's whole startup, but bounded so a failed
// launch cannot spin forever.
const worktreeWait = 60 * time.Second

// createWorktreeCmd does the git and tmux work off the update loop. Running it
// inline froze the entire TUI for seconds — long enough that a spinner could not
// have animated even if one had existed.
func createWorktreeCmd(repoPath, name, agent string) tea.Cmd {
	return func() tea.Msg {
		_, err := session.CreateWorktree(repoPath, name, agent)
		return worktreeCreatedMsg{name: name, err: err}
	}
}

// newSpinner returns the spinner used while a worktree is being created.
func newSpinner() spinner.Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	return sp
}

// statusLine returns whatever belongs above the session list: a spinner while a
// worktree is being created, an error when one failed, otherwise the search
// input.
//
// Errors are shown rather than only logged. A failure like "worktree already
// exists" used to be indistinguishable from nothing happening at all.
func (m Model) statusLine() string {
	if msg := m.statusMessage(); msg != "" {
		return msg
	}
	return m.searchInput.View()
}

// statusMessage is the transient line — progress, a note, an error — or "" when
// there is nothing to say. Focus mode shows it alone, since its sidebar has no
// search box for it to replace.
func (m Model) statusMessage() string {
	c := theme.Current().Colors

	if m.pending != nil {
		return " " + m.spinner.View() + lipgloss.NewStyle().Foreground(c.Warning).
			Render(fmt.Sprintf(" Creating worktree %q — waiting for the agent to start…", m.pending.name))
	}
	if m.statusNote != "" {
		return " " + lipgloss.NewStyle().Foreground(c.Subtle).Render("· "+m.statusNote)
	}
	if m.statusErr != "" {
		// A curly underline was tried here and rejected: lipgloss v2 emits a
		// separate SGR run per grapheme once a styled underline is set, which
		// turned a 23-character message into 958 bytes. The glyph and the Error
		// color already carry the signal.
		return " " + lipgloss.NewStyle().Foreground(c.Error).Bold(true).Render("✗ "+m.statusErr)
	}
	return ""
}
