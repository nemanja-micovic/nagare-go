package mcp

import (
	"strings"
	"testing"

	"github.com/nemke/nagare-go/internal/models"
)

func TestResolveSessionExact(t *testing.T) {
	sessions := []models.Session{
		{Name: "cosmo-ai"},
		{Name: "cosmo-ai/claude_01"},
	}
	got, err := resolveSession("cosmo-ai", sessions)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "cosmo-ai" {
		t.Errorf("got %q", got.Name)
	}
}

func TestResolveSessionPrefix(t *testing.T) {
	sessions := []models.Session{{Name: "cosmo-ai/claude_01"}}
	got, err := resolveSession("cosmo-ai", sessions)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "cosmo-ai/claude_01" {
		t.Errorf("got %q", got.Name)
	}
}

func TestResolveSessionAmbiguous(t *testing.T) {
	sessions := []models.Session{
		{Name: "cosmo-ai/claude_01"},
		{Name: "cosmo-ai/claude_02"},
	}
	_, err := resolveSession("cosmo-ai", sessions)
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error = %v", err)
	}
	if !strings.Contains(err.Error(), "cosmo-ai/claude_01") || !strings.Contains(err.Error(), "cosmo-ai/claude_02") {
		t.Errorf("error should list candidates: %v", err)
	}
}

func TestResolveSessionNotFound(t *testing.T) {
	_, err := resolveSession("nope", []models.Session{{Name: "other"}})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found error, got %v", err)
	}
}

func TestResolveSessionLooseNames(t *testing.T) {
	sessions := []models.Session{
		{Name: "cosmo-ai", AgentType: models.AgentClaude, Details: models.SessionDetails{RepoName: "cosmo"}},
		{Name: "billing", AgentType: models.AgentCodex, Path: "/src/billing-service"},
		{Name: "frontend/fix-nav", AgentType: models.AgentPi, Details: models.SessionDetails{Worktree: "fix-nav"}},
	}
	for query, want := range map[string]string{
		"Cosmo-AI":        "cosmo-ai",
		"codex":           "billing",
		"pi":              "frontend/fix-nav",
		"cosmo":           "cosmo-ai",
		"billing-service": "billing",
		"fix-nav":         "frontend/fix-nav",
		"front":           "frontend/fix-nav",
	} {
		got, err := resolveSession(query, sessions)
		if err != nil {
			t.Errorf("resolveSession(%q): %v", query, err)
			continue
		}
		if got.Name != want {
			t.Errorf("resolveSession(%q) = %q, want %q", query, got.Name, want)
		}
	}
}

func TestResolveSessionAmbiguousAgentType(t *testing.T) {
	sessions := []models.Session{
		{Name: "a", AgentType: models.AgentClaude},
		{Name: "b", AgentType: models.AgentClaude},
	}
	if _, err := resolveSession("claude", sessions); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("two Claude sessions must be ambiguous, got %v", err)
	}
}

// TestPaneTargetForUsesSessionName locks in the invariant that pane targets
// are built from SessionName (the real tmux session), not Name (the display
// name which can contain "/" for multi-pane disambiguation).
func TestPaneTargetForUsesSessionName(t *testing.T) {
	s := models.Session{
		Name:        "cosmo-ai/claude_02",
		SessionName: "cosmo-ai",
		WindowIndex: 1,
		PaneIndex:   0,
	}
	got := paneTargetFor(s)
	if got != "cosmo-ai:1.0" {
		t.Errorf("paneTargetFor = %q, want cosmo-ai:1.0", got)
	}
	if strings.Contains(got, "/") {
		t.Errorf("pane target leaked display-name slash: %q", got)
	}
}
