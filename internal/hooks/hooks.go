package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/nemke/nagare-go/internal/config"
	"github.com/nemke/nagare-go/internal/models"
	"github.com/nemke/nagare-go/internal/notifications"
	"github.com/nemke/nagare-go/internal/state"
	"github.com/nemke/nagare-go/internal/tmux"
)

// HookEvent is the JSON structure received on stdin from an agent hook,
// plugin, or extension. Fields absent for a given agent stay empty.
type HookEvent struct {
	HookEventName        string `json:"hook_event_name"`
	SessionID            string `json:"session_id"`
	Cwd                  string `json:"cwd"`
	LastAssistantMessage string `json:"last_assistant_message"`
	NotificationType     string `json:"notification_type"`
}

var needsInputTypes = map[string]bool{
	"permission_prompt":  true,
	"elicitation_dialog": true,
}

// EventToState maps a hook event name to a state string. Event names come from
// every supported agent: Claude Code, Codex, and Gemini CLI (MixedCase), pi and
// OhMyPi extensions (snake_case), and OpenCode plugins (dotted.lowercase).
func EventToState(event, notificationType string) string {
	switch event {
	// Claude Code
	case "UserPromptSubmit", "PreToolUse", "PostToolUse",
		"PreCompact", "PostCompact", "ElicitationResult":
		return "working"
	case "PermissionRequest", "Elicitation":
		return "waiting_input"
	case "Stop", "StopFailure":
		return "idle"
	case "Notification":
		if needsInputTypes[notificationType] {
			return "waiting_input"
		}
		return "idle"
	case "SessionEnd":
		return "dead"
	case "SessionStart":
		return "idle"

	// Gemini CLI
	case "BeforeAgent", "BeforeTool", "AfterTool":
		return "working"
	case "AfterAgent":
		return "idle"

	// pi and OhMyPi extensions. Shared lifecycle names map identically. pi
	// settles with agent_settled; OhMyPi settles with agent_end and also exposes
	// approval events.
	case "before_agent_start", "agent_start", "turn_start",
		"auto_compaction_start", "auto_retry_start", "tool_approval_resolved":
		return "working"
	case "tool_approval_requested":
		return "waiting_input"
	case "agent_settled", "agent_end", "session_start":
		return "idle"
	case "session_shutdown":
		return "dead"

	// OpenCode plugin
	case "session.status", "tool.execute.before", "permission.replied":
		return "working"
	case "permission.asked":
		return "waiting_input"
	case "session.idle", "session.error", "session.created":
		return "idle"

	default:
		return "unknown"
	}
}

// ShouldNotify determines if a notification should fire.
// Returns (eventType, workingSeconds). eventType is "" if no notification.
// minWorkingSeconds is the threshold from config (typically 30).
func ShouldNotify(newState, prevState string, workingSeconds, minWorkingSeconds int) (string, int) {
	// Only on the transition into waiting_input: a single approval prompt fires
	// both PermissionRequest and Notification/permission_prompt.
	if newState == "waiting_input" {
		if prevState == "waiting_input" {
			return "", 0
		}
		return "needs_input", 0
	}

	if newState == "idle" && prevState == "working" && workingSeconds >= minWorkingSeconds {
		return "task_complete", workingSeconds
	}

	return "", 0
}

// NvimPaneEnv names the variable the Neovim plugin sets on every agent it
// starts. Its value ("nvim:<pid>:<n>") stands in for TMUX_PANE, which inside
// Neovim would be the editor's own pane and shared by every agent in it.
const NvimPaneEnv = "NAGARE_PANE"

// PaneID returns the identifier state files are keyed by: the Neovim agent id
// when the agent runs inside the plugin, else the tmux pane.
func PaneID() string {
	if id := os.Getenv(NvimPaneEnv); id != "" {
		return id
	}
	return os.Getenv("TMUX_PANE")
}

// Handle reads a hook event from stdin and processes it.
// Exits with code 1 on fatal errors so hook failures are visible.
func Handle() {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nagare-go hook-state: failed to read stdin: %v\n", err)
		os.Exit(1)
	}

	var event HookEvent
	if err := json.Unmarshal(data, &event); err != nil {
		fmt.Fprintf(os.Stderr, "nagare-go hook-state: invalid JSON: %v\n", err)
		os.Exit(1)
	}

	newState := EventToState(event.HookEventName, event.NotificationType)
	now := time.Now().UTC().Format(time.RFC3339)
	statesDir := state.DefaultStatesDir()

	// Load previous state for this session only
	prevState, hasPrev := state.LoadStateByID(statesDir, event.SessionID)

	// Write new state
	newSessionState := models.SessionState{
		State:            newState,
		SessionID:        event.SessionID,
		Cwd:              event.Cwd,
		PaneID:           PaneID(),
		Event:            event.HookEventName,
		NotificationType: event.NotificationType,
		LastMessage:      event.LastAssistantMessage,
		Timestamp:        now,
	}
	state.WriteState(statesDir, newSessionState)

	// Determine working duration
	var workingSeconds int
	if hasPrev && prevState.State == "working" {
		prevTime, err := time.Parse(time.RFC3339, prevState.Timestamp)
		if err == nil {
			workingSeconds = int(time.Since(prevTime).Seconds())
		}
	}

	// Load config
	cfg, _ := config.Load()
	if !cfg.Notifications.Enabled {
		return
	}

	// Check if notification needed
	prevStateStr := ""
	if hasPrev {
		prevStateStr = prevState.State
	}

	minSecs := cfg.Notifications.TaskComplete.MinWorkingSeconds
	eventType, _ := ShouldNotify(newState, prevStateStr, workingSeconds, minSecs)
	if eventType == "" {
		return
	}

	var eventCfg config.NotificationEventConfig
	switch eventType {
	case "needs_input":
		eventCfg = cfg.Notifications.NeedsInput
	case "task_complete":
		eventCfg = cfg.Notifications.TaskComplete
	}

	// Resolve session name and build message once
	sessionName := resolveSessionName(event.Cwd)
	message := notifications.BuildToastMessage(sessionName, eventType, event.NotificationType)

	// An agent inside Neovim is announced by the plugin in the editor it runs
	// in; a tmux toast or popup would land in whatever tmux pane has focus.
	inNvim := os.Getenv(NvimPaneEnv) != ""
	toast, popup := eventCfg.Toast && !inNvim, eventCfg.Popup && !inNvim

	notifications.Deliver(message, toast, eventCfg.Bell && !inNvim, eventCfg.OsNotify, cfg.NotificationDuration)

	// Send popup if enabled
	if popup {
		notifications.SendPopup(sessionName, eventType, message, workingSeconds, eventCfg.PopupTimeout)
	}

	// Store notification
	store := notifications.NewStore(notifications.DefaultStorePath())
	store.Add(sessionName, message)
}

// resolveSessionName finds the tmux session name for a working directory.
func resolveSessionName(cwd string) string {
	raw := tmux.RunTmux("list-sessions", "-F", "#{session_name}:#{session_path}")
	if raw == "" {
		return fallbackName(cwd)
	}
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && parts[1] == cwd {
			return parts[0]
		}
	}
	return fallbackName(cwd)
}

func fallbackName(cwd string) string {
	if idx := strings.LastIndex(cwd, "/"); idx >= 0 {
		return cwd[idx+1:]
	}
	return cwd
}
