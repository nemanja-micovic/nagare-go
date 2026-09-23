package hooks

import (
	"encoding/json"
	"testing"
)

func TestEventToState(t *testing.T) {
	tests := []struct {
		event string
		ntype string
		want  string
	}{
		{"UserPromptSubmit", "", "working"},
		{"PreToolUse", "", "working"},
		{"BeforeAgent", "", "working"},
		{"BeforeTool", "", "working"},
		{"AfterTool", "", "working"},
		{"Stop", "", "idle"},
		{"AfterAgent", "", "idle"},
		{"SessionEnd", "", "dead"},
		{"SessionStart", "", "idle"},
		{"Notification", "permission_prompt", "waiting_input"},
		{"Notification", "elicitation_dialog", "waiting_input"},
		{"Notification", "other", "idle"},
		{"UnknownEvent", "", "unknown"},

		// Claude Code events added after the original five
		{"PermissionRequest", "", "waiting_input"},
		{"Elicitation", "", "waiting_input"},
		{"ElicitationResult", "", "working"},
		{"PostToolUse", "", "working"},
		{"PreCompact", "", "working"},
		{"PostCompact", "", "working"},
		{"StopFailure", "", "idle"},

		// pi extension events
		{"before_agent_start", "", "working"},
		{"agent_start", "", "working"},
		{"turn_start", "", "working"},
		{"agent_settled", "", "idle"},
		{"session_start", "", "idle"},
		{"session_shutdown", "", "dead"},

		// OhMyPi extension events
		{"auto_compaction_start", "", "working"},
		{"auto_retry_start", "", "working"},
		{"tool_approval_requested", "", "waiting_input"},
		{"tool_approval_resolved", "", "working"},
		{"agent_end", "", "idle"},

		// OpenCode plugin events
		{"session.status", "", "working"},
		{"tool.execute.before", "", "working"},
		{"permission.replied", "", "working"},
		{"permission.asked", "", "waiting_input"},
		{"session.idle", "", "idle"},
		{"session.error", "", "idle"},
		{"session.created", "", "idle"},
	}
	for _, tt := range tests {
		got := EventToState(tt.event, tt.ntype)
		if got != tt.want {
			t.Errorf("EventToState(%q, %q) = %q, want %q", tt.event, tt.ntype, got, tt.want)
		}
	}
}

func TestShouldNotify_NeedsInput(t *testing.T) {
	eventType, _ := ShouldNotify("waiting_input", "", 0, 30)
	if eventType != "needs_input" {
		t.Errorf("expected needs_input, got %q", eventType)
	}
}

func TestShouldNotify_TaskComplete(t *testing.T) {
	eventType, _ := ShouldNotify("idle", "working", 45, 30)
	if eventType != "task_complete" {
		t.Errorf("expected task_complete, got %q", eventType)
	}
}

func TestShouldNotify_TaskCompleteTooShort(t *testing.T) {
	eventType, _ := ShouldNotify("idle", "working", 5, 30)
	if eventType != "" {
		t.Errorf("expected empty (too short), got %q", eventType)
	}
}

func TestShouldNotify_NoNotification(t *testing.T) {
	eventType, _ := ShouldNotify("working", "idle", 0, 30)
	if eventType != "" {
		t.Errorf("expected empty, got %q", eventType)
	}
}

// Claude Code fires both PermissionRequest and Notification/permission_prompt for a
// single approval prompt. The second one must not produce a second notification.
func TestShouldNotify_NeedsInputNotRepeated(t *testing.T) {
	eventType, _ := ShouldNotify("waiting_input", "waiting_input", 0, 30)
	if eventType != "" {
		t.Errorf("expected empty on repeated waiting_input, got %q", eventType)
	}
}

func TestDeliveryOutputShapes(t *testing.T) {
	var stop map[string]string
	if err := json.Unmarshal(DeliveryOutput("Stop", "hello"), &stop); err != nil {
		t.Fatal(err)
	}
	// Claude Code and Codex both continue a blocked Stop with the reason as
	// the next prompt; a blank reason is rejected by Codex.
	if stop["decision"] != "block" || stop["reason"] != "hello" {
		t.Errorf("Stop output = %v", stop)
	}

	for _, event := range []string{"PostToolUse", "UserPromptSubmit", "SessionStart"} {
		var out struct {
			HookSpecificOutput map[string]string `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal(DeliveryOutput(event, "hello"), &out); err != nil {
			t.Fatal(err)
		}
		// hookEventName must match the event, or Codex rejects the output.
		if out.HookSpecificOutput["hookEventName"] != event || out.HookSpecificOutput["additionalContext"] != "hello" {
			t.Errorf("%s output = %v", event, out)
		}
	}
}

func TestOnlyContextCarryingEventsDeliver(t *testing.T) {
	// PreToolUse cannot carry context; draining there would lose messages.
	for _, event := range []string{"PreToolUse", "PermissionRequest", "SessionEnd", "agent_settled", "session.idle"} {
		if DeliversMessages(event) {
			t.Errorf("%s must not drain messages", event)
		}
	}
}
