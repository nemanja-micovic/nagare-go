package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/nemke/nagare-go/internal/config"
	"github.com/nemke/nagare-go/internal/memory"
	"github.com/nemke/nagare-go/internal/models"
	"github.com/nemke/nagare-go/internal/notifications"
	"github.com/nemke/nagare-go/internal/policy"
	"github.com/nemke/nagare-go/internal/state"
	"github.com/nemke/nagare-go/internal/tmux"
	"github.com/nemke/nagare-go/internal/trust"
	"github.com/nemke/nagare-go/internal/verify"
)

// HookEvent is the JSON structure received on stdin from an agent hook,
// plugin, or extension. Fields absent for a given agent stay empty.
type HookEvent struct {
	HookEventName        string          `json:"hook_event_name"`
	SessionID            string          `json:"session_id"`
	Cwd                  string          `json:"cwd"`
	LastAssistantMessage string          `json:"last_assistant_message"`
	NotificationType     string          `json:"notification_type"`
	ToolName             string          `json:"tool_name"`
	TranscriptPath       string          `json:"transcript_path"`
	ToolInput            json.RawMessage `json:"tool_input"`
	// StopHookActive is present only in Claude Code's Stop envelope, which is
	// also the one that honours a "block" decision; true when Claude is
	// already continuing because a stop hook blocked it.
	StopHookActive *bool `json:"stop_hook_active"`
}

// toolFields are the tool_input keys that say what a call is about, in the
// order to try them.
var toolFields = []string{"command", "file_path", "notebook_path", "url", "pattern", "query", "description", "prompt"}

// DescribeTool renders a tool call as one short line — "Bash: go test ./..."
// — so a "needs you" notification can say what it needs you for.
func DescribeTool(name string, input json.RawMessage) string {
	if name == "" {
		return ""
	}
	detail := ToolDetail(input)
	if r := []rune(detail); len(r) > 160 {
		detail = string(r[:159]) + "…"
	}
	if detail == "" {
		return name
	}
	return name + ": " + detail
}

// injectsContext lists agents whose SessionStart hook adds plain stdout to
// the model's context. Others ignore it or want a different envelope, so they
// get memory through the tools only.
var injectsContext = map[string]bool{"claude": true, "codex": true}

// ToolDetail is what a tool call is about — the command, file path or URL —
// whitespace-collapsed but complete, for matching policy rules.
func ToolDetail(input json.RawMessage) string {
	var fields map[string]any
	if len(input) > 0 {
		_ = json.Unmarshal(input, &fields)
	}
	for _, k := range toolFields {
		if v, ok := fields[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.Join(strings.Fields(v), " ")
		}
	}
	return ""
}

// ApplyPolicy answers a Claude PreToolUse from the project's policy: stdout
// carries the decision, and the state counts what was approved without
// asking. A policy with no opinion leaves the agent's own flow in charge.
func ApplyPolicy(ev HookEvent, st models.SessionState, p *policy.Policy) (models.SessionState, string) {
	if p == nil || ev.HookEventName != "PreToolUse" || ev.ToolName == "" {
		return st, ""
	}
	d := p.Decide(policy.Call{Tool: ev.ToolName, Detail: ToolDetail(ev.ToolInput), Cwd: ev.Cwd})
	if d.Verdict == "" {
		return st, ""
	}
	if d.Verdict == "allow" {
		st.AutoApproved++
	}
	out, _ := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]string{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       d.Verdict,
			"permissionDecisionReason": d.Reason,
		},
	})
	return st, string(out)
}

// projectPolicy loads the approved policy for cwd, or nil.
func projectPolicy(cwd string) *policy.Policy {
	file := verify.FindFile(cwd, policy.FileName)
	if file == "" || !trust.Open().Trusted(file) {
		return nil
	}
	p, err := policy.Load(file)
	if err != nil {
		return nil
	}
	return &p
}

// resetsTool marks events after which the last tool call is history.
var resetsTool = map[string]bool{"UserPromptSubmit": true, "Stop": true, "SessionStart": true, "AfterAgent": true}

// Verifier finds and runs a project's checks; tests swap it out. Find
// returns the command and whether the user approved the file it came from.
type Verifier struct {
	Find func(cwd string) (cmd string, trusted bool)
	Run  func(cwd, cmd string) verify.Result
}

// DefaultVerifier runs `.nagare/verify` for real — only once its contents
// are approved (`nagare-go trust`), since the file comes with the repository.
var DefaultVerifier = Verifier{
	Find: func(cwd string) (string, bool) {
		cmd, file := verify.Find(cwd)
		return cmd, cmd != "" && trust.Open().Trusted(file)
	},
	Run: func(cwd, cmd string) verify.Result {
		return verify.Run(cwd, cmd, verify.Timeout)
	},
}

// Decide works out the state to record for an event, and anything to print
// on stdout for the agent. It is pure apart from the verifier, so every
// branch is testable without a real agent.
func Decide(ev HookEvent, prev models.SessionState, hasPrev bool, v Verifier, now string) (models.SessionState, string) {
	st := models.SessionState{
		State:            EventToState(ev.HookEventName, ev.NotificationType),
		SessionID:        ev.SessionID,
		Cwd:              ev.Cwd,
		PaneID:           PaneID(),
		Event:            ev.HookEventName,
		NotificationType: ev.NotificationType,
		LastMessage:      ev.LastAssistantMessage,
		TranscriptPath:   ev.TranscriptPath,
		Timestamp:        now,
	}
	same := hasPrev && prev.SessionID == ev.SessionID
	if st.TranscriptPath == "" && same {
		st.TranscriptPath = prev.TranscriptPath
	}
	if same {
		st.Verify, st.VerifyFailures = prev.Verify, prev.VerifyFailures
		st.AutoApproved = prev.AutoApproved
	}

	switch tool := DescribeTool(ev.ToolName, ev.ToolInput); {
	case tool != "":
		st.LastTool = tool
	case resetsTool[ev.HookEventName]:
	case same:
		st.LastTool = prev.LastTool
	}

	if ev.HookEventName == "UserPromptSubmit" {
		// A new request: earlier verify failures were about earlier work.
		st.Verify, st.VerifyFailures = "", 0
	}

	// The verify gate: Claude wants to stop; the task is done only if the
	// project's own checks agree.
	if ev.HookEventName == "Stop" && ev.StopHookActive != nil && v.Find != nil {
		cmd, trusted := v.Find(ev.Cwd)
		if cmd != "" && !trusted {
			// A verify file nobody approved does nothing but say so.
			st.Verify = "untrusted"
			return st, ""
		}
		if cmd != "" {
			if st.VerifyFailures >= verify.MaxAttempts {
				// Out of attempts: let it stop, flagged, for a human.
				st.Verify = "fail"
				return st, ""
			}
			r := v.Run(ev.Cwd, cmd)
			if r.OK {
				st.Verify, st.VerifyFailures = "pass", 0
				return st, ""
			}
			st.VerifyFailures++
			st.Verify = "fail"
			st.State = "working"
			out, _ := json.Marshal(map[string]string{
				"decision": "block",
				"reason":   verify.Reason(cmd, r, st.VerifyFailures),
			})
			return st, string(out)
		}
	}
	return st, ""
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
func Handle(agent string) {
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

	now := time.Now().UTC().Format(time.RFC3339)
	statesDir := state.DefaultStatesDir()

	// Load previous state for this session only
	prevState, hasPrev := state.LoadStateByID(statesDir, event.SessionID)

	newSessionState, stdout := Decide(event, prevState, hasPrev, DefaultVerifier, now)
	if newSessionState.State == "idle" && event.LastAssistantMessage != "" && stdout == "" {
		// A turn ended with what reads like a lesson: propose it for the
		// shared memory. Pending until the human approves; never blocks.
		memory.Open().Propose(event.Cwd, event.LastAssistantMessage, memory.Who{Author: PaneID(), Session: event.SessionID})
	}
	if agent == "claude" && event.HookEventName == "PreToolUse" {
		var out string
		newSessionState, out = ApplyPolicy(event, newSessionState, projectPolicy(event.Cwd))
		if out != "" {
			stdout = out
		}
	}
	if event.HookEventName == "SessionStart" && injectsContext[agent] {
		// What agents learned on this repository, so this session starts
		// where the last ones left off. Covers startup, resume, /clear and
		// compaction, since Claude fires SessionStart for each.
		stdout = memory.Open().Context(event.Cwd, 6000)
	}
	newState := newSessionState.State
	state.WriteState(statesDir, newSessionState)
	if stdout != "" {
		fmt.Println(stdout)
	}

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
