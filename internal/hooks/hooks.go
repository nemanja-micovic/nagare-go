package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/nemke/nagare-go/internal/config"
	"github.com/nemke/nagare-go/internal/mcp"
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
	Prompt               string `json:"prompt"`          // the prompt that started a turn, where reported
	PermissionMode       string `json:"permission_mode"` // Claude Code's current permission mode
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
	case "session.status", "tool.execute.before", "permission.replied", "chat.message":
		return "working"
	case "permission.asked":
		return "waiting_input"
	case "session.idle", "session.error", "session.created":
		return "idle"

	default:
		return "unknown"
	}
}

// DeliversMessages reports whether an event's hook response can carry text
// into the agent's context. Claude Code and Codex share these event names and
// the response shapes in DeliveryOutput.
func DeliversMessages(event string) bool {
	switch event {
	case "Stop", "PostToolUse", "UserPromptSubmit", "SessionStart":
		return true
	}
	return false
}

// DeliveryOutput is the hook response that hands text to the agent. Stop is
// blocked with the messages as the reason, which both Claude Code and Codex
// feed back to the model as the prompt to continue with; the other events
// attach them as additional context.
func DeliveryOutput(event, text string) []byte {
	var out any
	if event == "Stop" {
		out = map[string]string{"decision": "block", "reason": text}
	} else {
		out = map[string]any{"hookSpecificOutput": map[string]string{
			"hookEventName":     event,
			"additionalContext": text,
		}}
	}
	data, _ := json.Marshal(out)
	return data
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

	paneID := os.Getenv("TMUX_PANE")

	// A prompt the user typed makes the next answer the user's, so any reply
	// this pane owed another agent is no longer its final message's to give.
	if event.Prompt != "" && !mcp.IsNagarePrompt(event.Prompt) {
		mcp.ForgetOwed(paneID)
	}
	// A finished turn's final message answers whatever the pane was asked.
	// Settled before new messages are drained, since those start a new debt.
	if newState == "idle" && event.LastAssistantMessage != "" {
		mcp.SettleOwed(paneID, event.LastAssistantMessage)
	}

	// Messages queued for this pane ride out on the hook's own response. A
	// Stop that delivers keeps the agent working, so it must not read as idle
	// — or notify as a finished task.
	var delivery []byte
	if DeliversMessages(event.HookEventName) && paneID != "" {
		if pushes := mcp.Drain(paneID); len(pushes) > 0 {
			mcp.Owe(paneID, mcp.OwedIDs(pushes)...)
			mcp.MarkRead(pushes)
			delivery = DeliveryOutput(event.HookEventName, mcp.JoinPushes(pushes))
			if event.HookEventName == "Stop" {
				newState = "working"
			}
		}
	}
	if delivery != nil {
		defer os.Stdout.Write(delivery)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	statesDir := state.DefaultStatesDir()

	// Load previous state for this session only
	prevState, hasPrev := state.LoadStateByID(statesDir, event.SessionID)

	// Write new state
	newSessionState := models.SessionState{
		State:            newState,
		SessionID:        event.SessionID,
		Cwd:              event.Cwd,
		PaneID:           paneID,
		Event:            event.HookEventName,
		NotificationType: event.NotificationType,
		LastMessage:      event.LastAssistantMessage,
		Timestamp:        now,
		// Claude Code exports its inbox socket only to hooks and Bash, so the
		// hook is where nagare learns it — and the permission mode decides
		// whether a message sent there would be delivered or held.
		MessagingSocket: os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET"),
		PermissionMode:  event.PermissionMode,
	}
	// Not every event carries these; one that omits them must not erase them.
	if hasPrev {
		if newSessionState.MessagingSocket == "" {
			newSessionState.MessagingSocket = prevState.MessagingSocket
		}
		if newSessionState.PermissionMode == "" {
			newSessionState.PermissionMode = prevState.PermissionMode
		}
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

	notifications.Deliver(message, eventCfg.Toast, eventCfg.Bell, eventCfg.OsNotify, cfg.NotificationDuration)

	// Send popup if enabled
	if eventCfg.Popup {
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
