package tmux

import (
	"testing"

	"github.com/nemanja-micovic/nagare-go/internal/models"
)

func TestDetectStatus_Empty(t *testing.T) {
	if got := DetectStatus(""); got != models.StatusDead {
		t.Errorf("empty content: got %q, want %q", got, models.StatusDead)
	}
}

func TestDetectStatus_BarePrompt(t *testing.T) {
	content := "some output\n❯\n"
	if got := DetectStatus(content); got != models.StatusIdle {
		t.Errorf("bare prompt: got %q, want %q", got, models.StatusIdle)
	}
}

func TestDetectStatus_ChoicePrompt(t *testing.T) {
	content := "some output\n❯ 1. Yes\n❯ 2. No\n"
	if got := DetectStatus(content); got != models.StatusWaitingInput {
		t.Errorf("choice prompt: got %q, want %q", got, models.StatusWaitingInput)
	}
}

func TestDetectStatus_DoYouWant(t *testing.T) {
	content := "Do you want to proceed?\n"
	if got := DetectStatus(content); got != models.StatusWaitingInput {
		t.Errorf("do you want: got %q, want %q", got, models.StatusWaitingInput)
	}
}

func TestDetectStatus_EscToCancel(t *testing.T) {
	content := "Choose an option\nEsc to cancel\n"
	if got := DetectStatus(content); got != models.StatusWaitingInput {
		t.Errorf("esc to cancel: got %q, want %q", got, models.StatusWaitingInput)
	}
}

func TestDetectStatus_Spinner(t *testing.T) {
	content := "Processing ⠙ loading...\n"
	if got := DetectStatus(content); got != models.StatusRunning {
		t.Errorf("spinner: got %q, want %q", got, models.StatusRunning)
	}
}

func TestDetectStatus_RunningTag(t *testing.T) {
	content := "something (running) here\n"
	if got := DetectStatus(content); got != models.StatusRunning {
		t.Errorf("running tag: got %q, want %q", got, models.StatusRunning)
	}
}

func TestDetectStatus_FastForward(t *testing.T) {
	content := "status bar ⏵⏵ active\n"
	if got := DetectStatus(content); got != models.StatusRunning {
		t.Errorf("fast-forward: got %q, want %q", got, models.StatusRunning)
	}
}

func TestParseDetails_WithStatusBar(t *testing.T) {
	content := "line1\nline2\nuser@host:/path (git:main) | Opus 4.6 | ctx:42%\n"
	details := ParseDetails(content)
	if details.GitBranch != "main" {
		t.Errorf("git branch = %q, want %q", details.GitBranch, "main")
	}
	if details.Model != "Opus 4.6" {
		t.Errorf("model = %q, want %q", details.Model, "Opus 4.6")
	}
	if details.ContextUsage != "42%" {
		t.Errorf("context = %q, want %q", details.ContextUsage, "42%")
	}
}

func TestParseDetails_NoStatusBar(t *testing.T) {
	content := "just some normal output\n"
	details := ParseDetails(content)
	if details.GitBranch != "" {
		t.Errorf("git branch should be empty, got %q", details.GitBranch)
	}
}
