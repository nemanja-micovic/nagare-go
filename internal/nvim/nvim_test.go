package nvim

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nemke/nagare-go/internal/models"
)

func TestEntriesResolvesRootOncePerPath(t *testing.T) {
	sessions := []models.Session{
		{Name: "api", SessionName: "api", Path: "/src/api", WindowIndex: 1, PaneIndex: 0, PaneID: "%1",
			Status: models.StatusWaitingInput, AgentType: models.AgentClaude},
		{Name: "api/fix", SessionName: "api", Path: "/src/api/.worktrees/fix", WindowIndex: 2, PaneID: "%2",
			Status: models.StatusRunning, AgentType: models.AgentCodex,
			Details: models.SessionDetails{Worktree: "fix", GitBranch: "fix"}},
		{Name: "api_02", SessionName: "api", Path: "/src/api", WindowIndex: 3, PaneID: "%3",
			Status: models.StatusIdle, AgentType: models.AgentClaude},
		{Name: "scratch", SessionName: "scratch", Path: "/tmp/scratch", PaneID: "%4",
			Status: models.StatusIdle, AgentType: models.AgentPi},
	}
	calls := map[string]int{}
	root := func(p string) string {
		calls[p]++
		if strings.HasPrefix(p, "/src/api") {
			return "/src/api"
		}
		return "" // not a repository
	}

	got := Entries(sessions, root)
	if len(got) != 4 {
		t.Fatalf("got %d entries, want 4", len(got))
	}
	if calls["/src/api"] != 1 {
		t.Errorf("root called %d times for a repeated path, want 1", calls["/src/api"])
	}
	if got[1].Root != "/src/api" || got[1].Worktree != "fix" {
		t.Errorf("worktree entry = %+v, want it grouped under /src/api", got[1])
	}
	if got[3].Root != "/tmp/scratch" {
		t.Errorf("non-repo root = %q, want the path itself", got[3].Root)
	}
	if got[0].Target != "api:1.0" || got[0].Status != "waiting_input" || got[0].Agent != "claude" {
		t.Errorf("entry 0 = %+v", got[0])
	}
}

func TestEntryJSONContract(t *testing.T) {
	// lua/nagare/tmux.lua reads these keys; renaming one breaks the plugin
	// silently, so pin them.
	data, err := json.Marshal(Entry{Name: "n", Session: "s", Target: "s:0.0", PaneID: "%0",
		Path: "/p", Root: "/p", Agent: "claude", Status: "idle"})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"name"`, `"session"`, `"target"`, `"pane_id"`, `"path"`, `"root"`, `"agent"`, `"status"`} {
		if !strings.Contains(string(data), key) {
			t.Errorf("JSON %s missing key %s", data, key)
		}
	}
}

func TestSocketPath(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	t.Setenv("XDG_RUNTIME_DIR", "")
	if got := SocketPath(""); got != "/home/u/.local/share/nagare/nvim.sock" {
		t.Errorf("default = %q", got)
	}
	if got := SocketPath("work"); got != "/home/u/.local/share/nagare/nvim-work.sock" {
		t.Errorf("named = %q", got)
	}
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	if got := SocketPath(""); got != "/run/user/1000/nagare/nvim.sock" {
		t.Errorf("runtime dir = %q", got)
	}
}
