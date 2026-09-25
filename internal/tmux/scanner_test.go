package tmux

import (
	"testing"

	"github.com/nemanja-micovic/nagare-go/internal/models"
)

func TestParseSessions(t *testing.T) {
	raw := "my-project:$0:/home/user/project\nother:$1:/tmp/other\n"
	sessions := ParseSessions(raw)
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	if sessions[0].Name != "my-project" {
		t.Errorf("name = %q, want %q", sessions[0].Name, "my-project")
	}
	if sessions[0].SessionID != "$0" {
		t.Errorf("id = %q, want %q", sessions[0].SessionID, "$0")
	}
	if sessions[0].Path != "/home/user/project" {
		t.Errorf("path = %q, want %q", sessions[0].Path, "/home/user/project")
	}
}

func TestParseSessionsEmpty(t *testing.T) {
	sessions := ParseSessions("")
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestParseAllPanes(t *testing.T) {
	raw := "my-project:0:0:claude:12345\nmy-project:0:1:zsh:12346\nother:0:0:opencode:12347\n"
	panes := ParseAllPanes(raw)

	myPanes, ok := panes["my-project"]
	if !ok {
		t.Fatal("expected my-project panes")
	}
	if len(myPanes) != 1 {
		t.Fatalf("expected 1 agent pane, got %d", len(myPanes))
	}
	if myPanes[0].AgentType != models.AgentClaude {
		t.Errorf("agent = %q, want %q", myPanes[0].AgentType, models.AgentClaude)
	}

	otherPanes := panes["other"]
	if len(otherPanes) != 1 {
		t.Fatalf("expected 1 pane, got %d", len(otherPanes))
	}
	if otherPanes[0].AgentType != models.AgentOpenCode {
		t.Errorf("agent = %q, want %q", otherPanes[0].AgentType, models.AgentOpenCode)
	}
}

func TestParseAllPanes_IgnoresNonAgent(t *testing.T) {
	raw := "sess:0:0:zsh:12345\nsess:0:1:vim:12346\n"
	panes := ParseAllPanes(raw)
	if len(panes) != 0 {
		t.Errorf("expected 0 agent sessions, got %d", len(panes))
	}
}

func TestParseAllPanesCapturesPaneID(t *testing.T) {
	raw := "work:0:0:claude:123:? claude:%7\n"
	got := ParseAllPanes(raw)
	panes := got["work"]
	if len(panes) != 1 {
		t.Fatalf("expected 1 pane, got %d", len(panes))
	}
	if panes[0].PaneID != "%7" {
		t.Errorf("PaneID = %q, want %q", panes[0].PaneID, "%7")
	}
}

func TestComputeDisplayNames(t *testing.T) {
	sess := "cosmo-ai"
	panes := []PaneInfo{
		{WindowIndex: 1, PaneIndex: 0, AgentType: models.AgentClaude, WindowName: "terminal", PaneID: "%2"},
		{WindowIndex: 0, PaneIndex: 0, AgentType: models.AgentClaude, WindowName: "? claude", PaneID: "%1"},
	}
	got := ComputeDisplayNames(sess, panes, nil)
	if got["%1"] != "cosmo-ai/claude_01" {
		t.Errorf("pane %%1 = %q, want cosmo-ai/claude_01", got["%1"])
	}
	if got["%2"] != "cosmo-ai/claude_02" {
		t.Errorf("pane %%2 = %q, want cosmo-ai/claude_02", got["%2"])
	}
}

func TestComputeDisplayNamesSinglePane(t *testing.T) {
	got := ComputeDisplayNames("work", []PaneInfo{
		{WindowIndex: 0, PaneIndex: 0, AgentType: models.AgentClaude, WindowName: "zsh", PaneID: "%3"},
	}, nil)
	if got["%3"] != "work" {
		t.Errorf("single pane name = %q, want work", got["%3"])
	}
}

func TestComputeDisplayNamesSinglePaneWithTaskName(t *testing.T) {
	got := ComputeDisplayNames("work", []PaneInfo{
		{WindowIndex: 0, PaneIndex: 0, AgentType: models.AgentClaude, WindowName: "fix login redirect", PaneID: "%3"},
	}, nil)
	if got["%3"] != "work/fix login redirect" {
		t.Errorf("single named pane = %q, want work/fix login redirect", got["%3"])
	}
}

func TestComputeDisplayNamesRepairsPrefixedTaskName(t *testing.T) {
	got := ComputeDisplayNames("cosmic-platform-backend", []PaneInfo{
		{
			WindowIndex: 0,
			PaneIndex:   0,
			AgentType:   models.AgentClaude,
			WindowName:  "cosmic-platform-backend/cosmic-platform-backend/tracking-financials",
			PaneID:      "%3",
		},
	}, nil)
	want := "cosmic-platform-backend/tracking-financials"
	if got["%3"] != want {
		t.Errorf("repaired pane name = %q, want %q", got["%3"], want)
	}
}

func TestComputeDisplayNamesCustomWindowName(t *testing.T) {
	panes := []PaneInfo{
		{WindowIndex: 0, PaneIndex: 0, AgentType: models.AgentClaude, WindowName: "? claude", PaneID: "%1"},
		{WindowIndex: 1, PaneIndex: 0, AgentType: models.AgentClaude, WindowName: "planning", PaneID: "%2"},
	}
	got := ComputeDisplayNames("cosmo-ai", panes, nil)
	if got["%1"] != "cosmo-ai/claude_01" {
		t.Errorf("pane %%1 = %q", got["%1"])
	}
	if got["%2"] != "cosmo-ai/planning" {
		t.Errorf("pane %%2 = %q", got["%2"])
	}
}

func TestComputeDisplayNamesMixedAgents(t *testing.T) {
	panes := []PaneInfo{
		{WindowIndex: 0, PaneIndex: 0, AgentType: models.AgentClaude, WindowName: "zsh", PaneID: "%1"},
		{WindowIndex: 1, PaneIndex: 0, AgentType: models.AgentGemini, WindowName: "zsh", PaneID: "%2"},
	}
	got := ComputeDisplayNames("proj", panes, nil)
	if got["%1"] != "proj/claude_01" {
		t.Errorf("pane %%1 = %q", got["%1"])
	}
	if got["%2"] != "proj/gemini_01" {
		t.Errorf("pane %%2 = %q", got["%2"])
	}
}

// pi is detectable as pane_current_command (standalone binary install).
func TestParseAllPanesDetectsPi(t *testing.T) {
	raw := "proj:0:0:pi:12345:zsh:%1\n"
	panes := ParseAllPanes(raw)

	p, ok := panes["proj"]
	if !ok || len(p) != 1 {
		t.Fatalf("expected 1 pi pane, got %v", panes)
	}
	if p[0].AgentType != models.AgentPi {
		t.Errorf("agent = %q, want %q", p[0].AgentType, models.AgentPi)
	}
}

// OhMyPi is detectable by its omp executable name.
func TestParseAllPanesDetectsOhMyPi(t *testing.T) {
	raw := "proj:0:0:omp:12345:zsh:%1\n"
	panes := ParseAllPanes(raw)

	p, ok := panes["proj"]
	if !ok || len(p) != 1 {
		t.Fatalf("expected 1 OhMyPi pane, got %v", panes)
	}
	if p[0].AgentType != models.AgentOhMyPi {
		t.Errorf("agent = %q, want %q", p[0].AgentType, models.AgentOhMyPi)
	}
}

func TestParseAllPanesDetectsCodex(t *testing.T) {
	raw := "proj:0:0:codex:12345:zsh:%1\n"
	panes := ParseAllPanes(raw)
	p, ok := panes["proj"]
	if !ok || len(p) != 1 {
		t.Fatalf("expected 1 Codex pane, got %v", panes)
	}
	if p[0].AgentType != models.AgentCodex {
		t.Errorf("agent = %q, want %q", p[0].AgentType, models.AgentCodex)
	}
}

func TestParseAllPanesCapturesPanePath(t *testing.T) {
	raw := "proj:0:0:claude:111:? claude:%2:/home/u/proj/.claude/worktrees/the-site\n"
	panes := ParseAllPanes(raw)

	p, ok := panes["proj"]
	if !ok || len(p) != 1 {
		t.Fatalf("expected 1 pane, got %v", panes)
	}
	if p[0].Path != "/home/u/proj/.claude/worktrees/the-site" {
		t.Errorf("Path = %q, want the worktree path", p[0].Path)
	}
}

// Older field counts must keep parsing, as they already do for pane_id.
func TestParseAllPanesWithoutPanePath(t *testing.T) {
	raw := "proj:0:0:claude:111:? claude:%2\n"
	panes := ParseAllPanes(raw)
	if len(panes["proj"]) != 1 {
		t.Fatalf("expected 1 pane, got %v", panes)
	}
	if got := panes["proj"][0].Path; got != "" {
		t.Errorf("Path = %q, want empty", got)
	}
}

func TestComputeDisplayNamesWorktrees(t *testing.T) {
	panes := []PaneInfo{
		{WindowIndex: 0, PaneIndex: 0, AgentType: models.AgentClaude, WindowName: "? claude", PaneID: "%2"},
		{WindowIndex: 1, PaneIndex: 0, AgentType: models.AgentClaude, WindowName: "terminal", PaneID: "%12"},
	}
	worktreeOf := map[string]string{"%2": "fluttering-watching-gadget", "%12": "the-site"}

	got := ComputeDisplayNames("cosmic-platform-frontend", panes, worktreeOf)

	if got["%2"] != "cosmic-platform-frontend/fluttering-watching-gadget" {
		t.Errorf("%%2 = %q", got["%2"])
	}
	if got["%12"] != "cosmic-platform-frontend/the-site" {
		t.Errorf("%%12 = %q", got["%12"])
	}
}

// A lone pane in a worktree is still worth naming after the worktree.
func TestComputeDisplayNamesSinglePaneInWorktree(t *testing.T) {
	panes := []PaneInfo{{AgentType: models.AgentClaude, PaneID: "%5"}}
	got := ComputeDisplayNames("app", panes, map[string]string{"%5": "the-site"})
	if got["%5"] != "app/the-site" {
		t.Errorf("got %q, want %q", got["%5"], "app/the-site")
	}
}

// An explicitly named window is the user's own labelling and still wins.
func TestComputeDisplayNamesWindowNameBeatsWorktree(t *testing.T) {
	panes := []PaneInfo{
		{WindowIndex: 0, AgentType: models.AgentClaude, WindowName: "review", PaneID: "%1"},
		{WindowIndex: 1, AgentType: models.AgentClaude, WindowName: "terminal", PaneID: "%2"},
	}
	got := ComputeDisplayNames("app", panes, map[string]string{"%1": "wt-a", "%2": "wt-b"})
	if got["%1"] != "app/review" {
		t.Errorf("%%1 = %q, want app/review", got["%1"])
	}
	if got["%2"] != "app/wt-b" {
		t.Errorf("%%2 = %q, want app/wt-b", got["%2"])
	}
}

// Two panes in one worktree must stay distinguishable.
func TestComputeDisplayNamesSameWorktreeTwice(t *testing.T) {
	panes := []PaneInfo{
		{WindowIndex: 0, AgentType: models.AgentClaude, WindowName: "terminal", PaneID: "%1"},
		{WindowIndex: 1, AgentType: models.AgentClaude, WindowName: "terminal", PaneID: "%2"},
	}
	got := ComputeDisplayNames("app", panes, map[string]string{"%1": "the-site", "%2": "the-site"})
	if got["%1"] == got["%2"] {
		t.Errorf("both panes got the same name %q", got["%1"])
	}
}
