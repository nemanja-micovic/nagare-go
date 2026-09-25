package tmux

import (
	"regexp"
	"strings"

	"github.com/nemanja-micovic/nagare-go/internal/models"
)

var (
	// Bare prompt on its own line.
	waitingPromptRe = regexp.MustCompile(`(?m)^❯\s*$`)

	// Choice/confirmation prompts.
	waitingChoicePatterns = []*regexp.Regexp{
		regexp.MustCompile(`❯\s+\d+\.\s+(Yes|No)`),
		regexp.MustCompile(`Do you want to`),
		regexp.MustCompile(`Esc to cancel`),
	}

	// Running indicators.
	runningPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\(running\)`),
		regexp.MustCompile(`[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏⠐⠂]`),
	}

	// Status bar: (git:branch) | Model | ctx:NN%
	statusBarRe = regexp.MustCompile(`\(git:(?P<branch>[^)]+)\)\s*\|\s*(?P<model>[^|]+?)\s*\|\s*ctx:(?P<ctx>\d+%)`)
)

// tail returns the last n lines of s.
func tail(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// DetectStatus determines session status by scraping pane content.
func DetectStatus(paneContent string) models.SessionStatus {
	if strings.TrimSpace(paneContent) == "" {
		return models.StatusDead
	}

	last15 := tail(paneContent, 15)

	// Choice/confirmation prompts -> waiting_input
	for _, pat := range waitingChoicePatterns {
		if pat.MatchString(last15) {
			return models.StatusWaitingInput
		}
	}

	// Running indicators
	for _, pat := range runningPatterns {
		if pat.MatchString(last15) {
			return models.StatusRunning
		}
	}

	// Bare prompt -> idle
	if waitingPromptRe.MatchString(last15) {
		return models.StatusIdle
	}

	// Fast-forward status bar
	if strings.Contains(last15, "⏵⏵") {
		return models.StatusRunning
	}

	return models.StatusIdle
}

// ParseDetails extracts git branch, model, context usage from pane status bar.
func ParseDetails(paneContent string) models.SessionDetails {
	if paneContent == "" {
		return models.SessionDetails{}
	}

	last5 := tail(paneContent, 5)
	match := statusBarRe.FindStringSubmatch(last5)
	if match == nil {
		return models.SessionDetails{}
	}

	result := models.SessionDetails{}
	for i, name := range statusBarRe.SubexpNames() {
		if i == 0 {
			continue
		}
		switch name {
		case "branch":
			result.GitBranch = strings.TrimSpace(match[i])
		case "model":
			result.Model = strings.TrimSpace(match[i])
		case "ctx":
			result.ContextUsage = strings.TrimSpace(match[i])
		}
	}
	return result
}
