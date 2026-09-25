package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nemke/nagare-go/internal/hooks"
	"github.com/nemke/nagare-go/internal/mcp"
)

func TestInstallClaudeHooks_NewFile(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	os.MkdirAll(claudeDir, 0755)

	if err := installClaudeHooks(home, "nagare-go-test"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}

	hooks, ok := settings["hooks"].(map[string]interface{})
	if !ok {
		t.Fatal("hooks key missing")
	}

	// Check standard events exist
	for _, event := range hookEvents {
		arr, ok := hooks[event].([]interface{})
		if !ok || len(arr) == 0 {
			t.Errorf("event %q missing or empty", event)
		}
	}

	// Check Notification event has matcher
	notifArr, ok := hooks["Notification"].([]interface{})
	if !ok || len(notifArr) == 0 {
		t.Fatal("Notification event missing")
	}
	notifEntry, ok := notifArr[0].(map[string]interface{})
	if !ok {
		t.Fatal("Notification entry is not a map")
	}
	if notifEntry["matcher"] != notificationMatcher {
		t.Errorf("matcher = %q, want %q", notifEntry["matcher"], notificationMatcher)
	}
}

func TestInstallClaudeHooks_PreservesExisting(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	os.MkdirAll(claudeDir, 0755)

	// Write existing settings with a custom hook
	existing := map[string]interface{}{
		"hooks": map[string]interface{}{
			"Stop": []interface{}{
				map[string]interface{}{
					"type":    "command",
					"command": "my-custom-hook",
				},
			},
		},
		"other_setting": true,
	}
	data, _ := json.Marshal(existing)
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0644)

	if err := installClaudeHooks(home, "nagare-go-test"); err != nil {
		t.Fatal(err)
	}

	result, err := loadJSON(filepath.Join(claudeDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}

	// other_setting preserved
	if result["other_setting"] != true {
		t.Error("other_setting should be preserved")
	}

	// Custom hook preserved
	hooks := result["hooks"].(map[string]interface{})
	stopArr := hooks["Stop"].([]interface{})
	if len(stopArr) < 2 {
		t.Fatalf("Stop should have custom + nagare hooks, got %d", len(stopArr))
	}

	// First should be the custom hook
	first := stopArr[0].(map[string]interface{})
	if first["command"] != "my-custom-hook" {
		t.Errorf("custom hook should be preserved, got %q", first["command"])
	}
}

func TestInstallClaudeHooks_RemovesStaleHooks(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	os.MkdirAll(claudeDir, 0755)

	// Write settings with old nagare hooks
	existing := map[string]interface{}{
		"hooks": map[string]interface{}{
			"Stop": []interface{}{
				map[string]interface{}{
					"type":    "command",
					"command": "/old/path/nagare-go hook-state",
					"timeout": 5,
				},
				map[string]interface{}{
					"type":    "command",
					"command": "my-custom-hook",
				},
			},
		},
	}
	data, _ := json.Marshal(existing)
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0644)

	if err := installClaudeHooks(home, "nagare-go-test"); err != nil {
		t.Fatal(err)
	}

	result, _ := loadJSON(filepath.Join(claudeDir, "settings.json"))
	hooks := result["hooks"].(map[string]interface{})
	stopArr := hooks["Stop"].([]interface{})

	// Should have custom hook + new nagare hook (stale one removed)
	if len(stopArr) != 2 {
		t.Fatalf("expected 2 hooks (custom + fresh nagare), got %d", len(stopArr))
	}

	// First should be custom, second should be fresh nagare
	first := stopArr[0].(map[string]interface{})
	if first["command"] != "my-custom-hook" {
		t.Errorf("first hook should be custom, got %q", first["command"])
	}
}

func TestInstallPiExtension(t *testing.T) {
	home := t.TempDir()
	if err := installPiExtension(home, "/opt/bin/nagare-go"); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(home, ".pi", "agent", "extensions", "nagare.ts")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// The binary path must be embedded as a quoted JS string.
	if !strings.Contains(content, `const NAGARE = "/opt/bin/nagare-go"`) {
		t.Error("nagare binary path not embedded")
	}
	// No unexpanded Go format verbs may survive into the generated file.
	if strings.Contains(content, "%q") || strings.Contains(content, "%%") {
		t.Error("generated extension contains unexpanded format verbs")
	}
	// Every messaging tool must be registered, or pi loses parity with MCP agents.
	for _, tool := range mcp.ToolNames() {
		if !strings.Contains(content, `name: "`+tool+`"`) {
			t.Errorf("tool %q not registered in pi extension", tool)
		}
	}
	// agent_settled, not agent_end: pi may keep working after agent_end.
	if !strings.Contains(content, "agent_settled") {
		t.Error("extension does not subscribe to agent_settled")
	}
	// agent_end is read for the run's final text, but must never report
	// status: it fires before pi has settled.
	start := strings.Index(content, "const statusEvents = [")
	end := strings.Index(content[start:], "] as const") + start
	if start < 0 || end < start {
		t.Fatal("status event list not found")
	}
	if strings.Contains(content[start:end], `"agent_end"`) {
		t.Error("extension reports status on agent_end, which fires before pi has settled")
	}
	// The final text rides on agent_settled, which is what sends it back as
	// an automatic reply.
	for _, want := range []string{"last_assistant_message", "lastAssistantText(e?.messages)", "extra.prompt = e.prompt"} {
		if !strings.Contains(content, want) {
			t.Errorf("extension missing automatic-reply piece %q", want)
		}
	}
}

func TestInstallPiExtensionIsIdempotent(t *testing.T) {
	home := t.TempDir()
	if err := installPiExtension(home, "/a/nagare-go"); err != nil {
		t.Fatal(err)
	}
	if err := installPiExtension(home, "/b/nagare-go"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(home, ".pi", "agent", "extensions", "nagare.ts"))
	if strings.Contains(string(data), "/a/nagare-go") {
		t.Error("re-running setup should replace the old binary path")
	}
}

func TestInstallOhMyPiExtension(t *testing.T) {
	home := t.TempDir()
	if err := installOhMyPiExtension(home, "/opt/bin/nagare-go"); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(home, ".omp", "agent", "extensions", "nagare.ts")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, `const NAGARE = "/opt/bin/nagare-go"`) {
		t.Error("nagare binary path not embedded")
	}
	if !strings.Contains(content, `from "@oh-my-pi/pi-coding-agent"`) {
		t.Error("extension does not import OhMyPi's extension API")
	}

	events := []string{
		"session_start",
		"before_agent_start",
		"agent_start",
		"turn_start",
		"auto_compaction_start",
		"auto_retry_start",
		"tool_approval_requested",
		"tool_approval_resolved",
		"agent_end",
		"session_shutdown",
	}
	for _, event := range events {
		if !strings.Contains(content, `"`+event+`"`) {
			t.Errorf("extension does not forward %q", event)
		}
		if state := hooks.EventToState(event, ""); state == "unknown" {
			t.Errorf("forwarded event %q is not mapped by EventToState", event)
		}
	}
	if strings.Contains(content, "agent_settled") {
		t.Error("OhMyPi extension uses pi's unavailable agent_settled event")
	}
	// The pi treatment: a message listener and automatic replies, from the
	// code shared with pi, and no retry mistaken for the end of a run.
	for _, want := range []string{`listen(pi, () => ctxNow, "omp")`, `deliverAs: "steer"`, "function takePushes",
		"last_assistant_message", "e?.willContinue", "extra.prompt = e.prompt"} {
		if !strings.Contains(content, want) {
			t.Errorf("extension missing %q", want)
		}
	}
	if strings.Contains(content, "__SHARED__") || strings.Contains(content, "%%") {
		t.Error("template placeholders or format escapes left in the extension")
	}
}

func TestRegisterMCPOhMyPiPath(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".omp", "agent", "mcp.json")
	if err := registerMCPStandard(path, "/opt/bin/nagare-go"); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	servers, ok := cfg["mcpServers"].(map[string]interface{})
	if !ok {
		t.Fatal("mcpServers key missing")
	}
	entry, ok := servers["nagare"].(map[string]interface{})
	if !ok {
		t.Fatal("nagare server missing")
	}
	if entry["command"] != "/opt/bin/nagare-go" {
		t.Errorf("command = %v, want /opt/bin/nagare-go", entry["command"])
	}
	args, ok := entry["args"].([]interface{})
	if !ok || len(args) != 1 || args[0] != "mcp" {
		t.Errorf("args = %v, want [mcp]", entry["args"])
	}
}

func TestInstallCommandsIncludesOhMyPi(t *testing.T) {
	home := t.TempDir()
	installCommands(home)

	dir := filepath.Join(home, ".omp", "agent", "commands")
	for name := range commands {
		path := filepath.Join(dir, name+".md")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("command %q not installed for OhMyPi: %v", name, err)
			continue
		}
		if !strings.HasPrefix(string(data), "---\ndescription: ") {
			t.Errorf("command %q has no OhMyPi frontmatter", name)
		}
	}
}

func TestInstallOpenCodePlugin(t *testing.T) {
	home := t.TempDir()
	if err := installOpenCodePlugin(home, "/opt/bin/nagare-go"); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(home, ".config", "opencode", "plugins", "nagare.js")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, `const NAGARE = "/opt/bin/nagare-go"`) {
		t.Error("nagare binary path not embedded")
	}
	if strings.Contains(content, "%q") || strings.Contains(content, "%%") {
		t.Error("generated plugin contains unexpanded format verbs")
	}
	// The events the plugin forwards must be ones nagare maps to a real state.
	for _, event := range []string{"session.idle", "permission.asked", "session.status"} {
		if !strings.Contains(content, `"`+event+`"`) {
			t.Errorf("plugin does not forward %q", event)
		}
		if state := hooks.EventToState(event, ""); state == "unknown" {
			t.Errorf("forwarded event %q is not mapped by EventToState", event)
		}
	}
	// The plugin is the pane's message listener.
	for _, want := range []string{"promptAsync", `join(DATA, "push", PANE)`, "renameSync", `agent: "opencode"`,
		"client.session.messages", "last_assistant_message", `"chat.message"`} {
		if !strings.Contains(content, want) {
			t.Errorf("plugin missing message listener piece %q", want)
		}
	}
}

func TestPiExtensionListensForMessages(t *testing.T) {
	home := t.TempDir()
	if err := installPiExtension(home, "/opt/bin/nagare-go"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "extensions", "nagare.ts"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	// Busy pi must get messages as steering, or they wait for the turn to end.
	for _, want := range []string{"sendUserMessage", `deliverAs: "steer"`, "isIdle()", `join(DATA, "push", PANE)`, `listen(pi, () => ctxNow, "pi")`} {
		if !strings.Contains(content, want) {
			t.Errorf("extension missing %q", want)
		}
	}
	if strings.Contains(content, "__DESC_") {
		t.Error("tool description placeholder left unfilled")
	}
	// pi tools must carry the MCP server's wording, not a stale copy.
	if !strings.Contains(content, strconv.Quote(mcp.ToolDescription("send_message"))) {
		t.Error("send_message description differs from the MCP server's")
	}
}

func TestHooksDeliverMessagesMidTurn(t *testing.T) {
	for name, events := range map[string][]string{"Claude Code": hookEvents, "Codex": codexHookEvents} {
		found := false
		for _, e := range events {
			if e == "PostToolUse" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s hooks lack PostToolUse, so messages wait for the turn to end", name)
		}
	}
}

// OpenCode reads ~/.config/opencode/opencode.json; config.json is the old name
// that current versions ignore.
func TestRegisterMCPLocalOpenCodePath(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".config", "opencode", "opencode.json")
	if err := registerMCPLocal(path, "/opt/bin/nagare-go"); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	mcpMap, ok := cfg["mcp"].(map[string]interface{})
	if !ok {
		t.Fatal("mcp key missing")
	}
	entry, ok := mcpMap["nagare"].(map[string]interface{})
	if !ok {
		t.Fatal("nagare server missing")
	}
	if entry["type"] != "local" {
		t.Errorf("type = %v, want local", entry["type"])
	}
	if entry["enabled"] != true {
		t.Errorf("enabled = %v, want true", entry["enabled"])
	}
}

func TestInstallCodexHooksPreservesExisting(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	existing := map[string]interface{}{
		"description": "my hooks",
		"hooks": map[string]interface{}{
			"Stop": []interface{}{map[string]interface{}{
				"hooks": []interface{}{map[string]interface{}{
					"type": "command", "command": "my-custom-hook",
				}},
			}},
		},
	}
	data, _ := json.Marshal(existing)
	if err := os.WriteFile(filepath.Join(dir, "hooks.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if err := installCodexHooks(home, "/opt/bin/nagare-go"); err != nil {
			t.Fatal(err)
		}
	}
	result, err := loadJSON(filepath.Join(dir, "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if result["description"] != "my hooks" {
		t.Error("top-level Codex hook metadata was not preserved")
	}
	hooks := result["hooks"].(map[string]interface{})
	for _, event := range codexHookEvents {
		if entries, ok := hooks[event].([]interface{}); !ok || len(entries) == 0 {
			t.Errorf("Codex event %q missing", event)
		}
	}
	if got := len(hooks["Stop"].([]interface{})); got != 2 {
		t.Errorf("Stop hooks = %d, want custom + Nagare", got)
	}
}

func TestRegisterMCPCodexPreservesConfigAndReplacesEntry(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	existing := `# keep this comment
model = "test-model"

[mcp_servers.other]
command = "other-mcp"

[mcp_servers.nagare]
command = "/old/nagare-go"
args = ["mcp"]
`
	if err := os.WriteFile(path, []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}
	if err := registerMCPCodex(path, "/opt/bin/nagare-go"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{"# keep this comment", `model = "test-model"`, "[mcp_servers.other]", `command = "/opt/bin/nagare-go"`,
		// Codex filters MCP server environments; without the pane id nagare
		// cannot tell which agent is sending.
		`env_vars = ["TMUX_PANE", "TMUX"]`,
		// The 60s default would cut send_message_and_wait short.
		"tool_timeout_sec = 900"} {
		if !strings.Contains(content, want) {
			t.Errorf("Codex config missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "/old/nagare-go") {
		t.Error("stale Nagare MCP entry survived")
	}
}

func TestInstallCodexSkill(t *testing.T) {
	home := t.TempDir()
	installCodexSkill(home)
	data, err := os.ReadFile(filepath.Join(home, ".codex", "skills", "nagare", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "name: nagare") {
		t.Error("Codex skill frontmatter missing")
	}
	for _, tool := range mcp.ToolNames() {
		if !strings.Contains(content, tool+"(") {
			t.Errorf("Codex skill does not mention %q", tool)
		}
	}
}
