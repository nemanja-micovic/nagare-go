package picker

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/nemanja-micovic/nagare-go/internal/models"
)

// benchSessions builds a realistic mixed set: grouped worktrees, varied statuses
// and agents, long paths and branches.
func benchSessions(n int) []models.Session {
	out := make([]models.Session, 0, n)
	statuses := []models.SessionStatus{
		models.StatusIdle, models.StatusRunning, models.StatusWaitingInput,
	}
	agents := []models.AgentType{
		models.AgentClaude, models.AgentOpenCode, models.AgentGemini, models.AgentPi,
	}
	for i := 0; i < n; i++ {
		repo := []string{"cosmic-platform-backend", "cosmic-platform-frontend", "nagare-go"}[i%3]
		out = append(out, models.Session{
			Name:        repo + "/claude_0" + string(rune('0'+i%10)),
			SessionName: repo,
			Path:        "/home/nemke/Projects/" + repo,
			Status:      statuses[i%len(statuses)],
			AgentType:   agents[i%len(agents)],
			// No LastActivity: the detail pane renders it as a relative time, which
			// would make a rendered frame differ run to run.
			Details: models.SessionDetails{
				RepoName: repo, Worktree: "feat/procosmic",
				GitBranch: "picker-depth-and-mouse",
			},
			LastMessage: "a reasonably long last assistant message from the agent",
		})
	}
	return out
}

func benchModel(n, w, h int) Model {
	m := NewForTest()
	for _, msg := range []tea.Msg{
		tea.WindowSizeMsg{Width: w, Height: h},
		SessionsUpdatedMsg(benchSessions(n)),
		PreviewUpdatedMsg(strings.Repeat("a line of captured pane output\n", 60)),
	} {
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	return m
}

// A frame's cost decides whether an animation clock can run continuously: at 30fps
// a 4ms frame is 12% of a core. See the Animation notes in CLAUDE.md.
func BenchmarkViewList30(b *testing.B) {
	m := benchModel(30, 200, 50)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}

func BenchmarkViewList8(b *testing.B) {
	m := benchModel(8, 120, 30)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}

func BenchmarkViewGrid(b *testing.B) {
	m := benchModel(9, 200, 50)
	m.viewMode = GridView
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}

// Searching turns on per-rune match highlighting, which is a hot path of its own.
func BenchmarkViewListFiltered(b *testing.B) {
	m := benchModel(30, 200, 50)
	m.searchInput.SetValue("cosmic")
	m.applyFilter()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}

func BenchmarkHelpOverlay(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = helpOverlay(200, 50)
	}
}
