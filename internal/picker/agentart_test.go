package picker

import (
	"strings"
	"testing"

	"github.com/muesli/ansi"
	"github.com/nemanja-micovic/nagare-go/internal/models"
)

// Logos are joined horizontally with session details, so every logo must have
// the same number of rows or the surrounding layout shifts.
func TestAgentArtRowCount(t *testing.T) {
	for agent, art := range agentArt {
		if got := len(strings.Split(art, "\n")); got != 5 {
			t.Errorf("agentArt[%q] has %d rows, want 5", agent, got)
		}
	}
}

// renderAgentArt returns "" unless the agent has both art and a gradient, so a
// half-registered agent silently renders as blank.
func TestAgentArtAndGradientsAgree(t *testing.T) {
	for agent := range agentArt {
		if _, ok := agentGradients[agent]; !ok {
			t.Errorf("agent %q has art but no gradient", agent)
		}
	}
	for agent := range agentGradients {
		if _, ok := agentArt[agent]; !ok {
			t.Errorf("agent %q has a gradient but no art", agent)
		}
	}
}

func TestAgentArtPi(t *testing.T) {
	art, ok := agentArt[models.AgentPi]
	if !ok {
		t.Fatal("pi has no logo")
	}
	for i, line := range strings.Split(art, "\n") {
		if w := ansi.PrintableRuneWidth(line); w != 8 {
			t.Errorf("pi logo row %d is %d cells wide, want 8", i, w)
		}
	}
	if renderAgentArt(models.AgentPi) == "" {
		t.Error("renderAgentArt(pi) returned empty")
	}
	if renderAgentArtSmall(models.AgentPi) == "" {
		t.Error("renderAgentArtSmall(pi) returned empty")
	}
}

func TestAgentArtOhMyPi(t *testing.T) {
	art, ok := agentArt[models.AgentOhMyPi]
	if !ok {
		t.Fatal("OhMyPi has no logo")
	}
	for i, line := range strings.Split(art, "\n") {
		if w := ansi.PrintableRuneWidth(line); w != 8 {
			t.Errorf("OhMyPi logo row %d is %d cells wide, want 8", i, w)
		}
	}
	if renderAgentArt(models.AgentOhMyPi) == "" {
		t.Error("renderAgentArt(OhMyPi) returned empty")
	}
	if renderAgentArtSmall(models.AgentOhMyPi) == "" {
		t.Error("renderAgentArtSmall(OhMyPi) returned empty")
	}
}

func TestAgentArtCodex(t *testing.T) {
	art, ok := agentArt[models.AgentCodex]
	if !ok || art == "" {
		t.Fatal("Codex has no logo")
	}
	for i, line := range strings.Split(art, "\n") {
		if w := ansi.PrintableRuneWidth(line); w != 8 {
			t.Errorf("Codex logo row %d is %d cells wide, want 8", i, w)
		}
	}
}
