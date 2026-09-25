package hooks

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nemke/nagare-go/internal/models"
	"github.com/nemke/nagare-go/internal/verify"
)

func TestDescribeTool(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"Bash", `{"command":"go test ./...","description":"run tests"}`, "Bash: go test ./..."},
		{"Edit", `{"file_path":"/src/app.go","old_string":"a"}`, "Edit: /src/app.go"},
		{"WebFetch", `{"url":"https://x.dev","prompt":"read"}`, "WebFetch: https://x.dev"},
		{"mcp__nagare__recall", `{}`, "mcp__nagare__recall"},
		{"Bash", `{"command":"echo  a\n  b"}`, "Bash: echo a b"},
		{"", `{"command":"x"}`, ""},
	}
	for _, tt := range tests {
		if got := DescribeTool(tt.name, json.RawMessage(tt.input)); got != tt.want {
			t.Errorf("DescribeTool(%s, %s) = %q, want %q", tt.name, tt.input, got, tt.want)
		}
	}
	long := `{"command":"` + strings.Repeat("x", 400) + `"}`
	if got := DescribeTool("Bash", json.RawMessage(long)); len([]rune(got)) > 170 {
		t.Errorf("long command not truncated: %d runes", len([]rune(got)))
	}
}

func TestDecideCarriesTheLastToolIntoTheWait(t *testing.T) {
	pre, _ := Decide(HookEvent{HookEventName: "PreToolUse", SessionID: "s", ToolName: "Bash",
		ToolInput: json.RawMessage(`{"command":"rm -rf build"}`)}, models.SessionState{}, false, Verifier{}, "t1")
	if pre.LastTool != "Bash: rm -rf build" || pre.State != "working" {
		t.Fatalf("PreToolUse = %+v", pre)
	}
	wait, _ := Decide(HookEvent{HookEventName: "Notification", SessionID: "s", NotificationType: "permission_prompt"},
		pre, true, Verifier{}, "t2")
	if wait.State != "waiting_input" || wait.LastTool != "Bash: rm -rf build" {
		t.Errorf("the wait should name the tool: %+v", wait)
	}
	done, _ := Decide(HookEvent{HookEventName: "Stop", SessionID: "s"}, wait, true, Verifier{}, "t3")
	if done.LastTool != "" {
		t.Errorf("Stop should clear the tool, got %q", done.LastTool)
	}
	other, _ := Decide(HookEvent{HookEventName: "Notification", SessionID: "other"}, pre, true, Verifier{}, "t4")
	if other.LastTool != "" {
		t.Errorf("another session's tool leaked: %q", other.LastTool)
	}
}

func fakeVerifier(cmd string, results ...bool) (Verifier, *int) {
	calls := 0
	return Verifier{
		Find: func(string) string { return cmd },
		Run: func(cwd, c string) verify.Result {
			ok := results[calls]
			calls++
			return verify.Result{OK: ok, Output: "FAIL TestLogin"}
		},
	}, &calls
}

func stop(active bool) HookEvent {
	return HookEvent{HookEventName: "Stop", SessionID: "s", Cwd: "/r", StopHookActive: &active}
}

func TestVerifyGateBlocksAFailingStop(t *testing.T) {
	v, calls := fakeVerifier("go test ./...", false)
	st, out := Decide(stop(false), models.SessionState{SessionID: "s", State: "working"}, true, v, "t")
	if *calls != 1 {
		t.Fatalf("verify ran %d times", *calls)
	}
	if st.State != "working" || st.Verify != "fail" || st.VerifyFailures != 1 {
		t.Errorf("blocked stop should keep working: %+v", st)
	}
	var decision map[string]string
	if err := json.Unmarshal([]byte(out), &decision); err != nil {
		t.Fatalf("stdout is not JSON: %q", out)
	}
	if decision["decision"] != "block" || !strings.Contains(decision["reason"], "FAIL TestLogin") ||
		!strings.Contains(decision["reason"], "attempt 1/3") {
		t.Errorf("decision = %v", decision)
	}
}

func TestVerifyGatePassesAndGivesUp(t *testing.T) {
	v, _ := fakeVerifier("make check", true)
	st, out := Decide(stop(true), models.SessionState{SessionID: "s", VerifyFailures: 2, Verify: "fail"}, true, v, "t")
	if out != "" || st.State != "idle" || st.Verify != "pass" || st.VerifyFailures != 0 {
		t.Errorf("passing stop: %+v, out %q", st, out)
	}

	v, calls := fakeVerifier("make check", false)
	st, out = Decide(stop(true), models.SessionState{SessionID: "s", VerifyFailures: verify.MaxAttempts}, true, v, "t")
	if out != "" || st.State != "idle" || st.Verify != "fail" || *calls != 0 {
		t.Errorf("out of attempts should stop, flagged, without running: %+v out %q calls %d", st, out, *calls)
	}
}

func TestVerifyGateOnlyForClaudeStopsInProjectsThatOptIn(t *testing.T) {
	v, calls := fakeVerifier("", false)
	if _, out := Decide(stop(false), models.SessionState{}, false, v, "t"); out != "" || *calls != 0 {
		t.Error("no .nagare/verify: nothing runs")
	}
	v, calls = fakeVerifier("go test", false)
	codex := HookEvent{HookEventName: "Stop", SessionID: "s"} // no stop_hook_active
	if _, out := Decide(codex, models.SessionState{}, false, v, "t"); out != "" || *calls != 0 {
		t.Error("an envelope that cannot honour block must not be blocked")
	}
}

func TestUserPromptResetsVerifyFailures(t *testing.T) {
	st, _ := Decide(HookEvent{HookEventName: "UserPromptSubmit", SessionID: "s"},
		models.SessionState{SessionID: "s", VerifyFailures: 3, Verify: "fail"}, true, Verifier{}, "t")
	if st.VerifyFailures != 0 || st.Verify != "" {
		t.Errorf("new prompt should reset: %+v", st)
	}
}
