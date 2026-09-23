package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/nemke/nagare-go/internal/bin"
)

// Claude Code hook events nagare subscribes to, with matcher="" (all).
// PostToolUse adds a process spawn per tool call; it earns it as the point
// where messages from other agents are delivered mid-turn, as additional
// context, instead of waiting for the turn to end.
var hookEvents = []string{
	"UserPromptSubmit",
	"PreToolUse",
	"PostToolUse",
	"PermissionRequest", // exact "needs approval" signal
	"Elicitation",       // MCP server asking the user for input
	"ElicitationResult", // input supplied, turn resumes
	"Stop",
	"StopFailure", // turn died on an API error — otherwise stuck "working"
	"SessionStart",
	"SessionEnd",
}

// Codex exposes the same lifecycle event names and stdin JSON envelope as
// Claude Code. These are the events needed for Nagare's four visible states,
// plus PostToolUse, which delivers messages from other agents mid-turn.
var codexHookEvents = []string{
	"UserPromptSubmit",
	"PermissionRequest",
	"PostToolUse",
	"Stop",
	"SessionStart",
	"SessionEnd",
}

// notificationEvent has a matcher, handled separately.
const notificationMatcher = "idle_prompt|permission_prompt|elicitation_dialog"

// Run installs nagare-go status reporting, the MCP server, and slash commands
// into every supported agent CLI.
func Run() error {
	// Ensure data directory exists
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}

	dataDir := filepath.Join(home, ".local", "share", "nagare")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("cannot create data directory: %w", err)
	}
	fmt.Printf("  Data directory: %s\n", dataDir)

	// Ensure states directory exists
	statesDir := filepath.Join(dataDir, "states")
	if err := os.MkdirAll(statesDir, 0755); err != nil {
		return fmt.Errorf("cannot create states directory: %w", err)
	}

	// Find our binary once for all registrations
	nagareBin := bin.FindSelf()

	// Install hooks
	if err := installClaudeHooks(home, nagareBin); err != nil {
		return fmt.Errorf("failed to install Claude hooks: %w", err)
	}
	if err := installGeminiHooks(home, nagareBin); err != nil {
		fmt.Printf("  Hooks: Gemini CLI — skipped (%v)\n", err)
	}
	if err := installCodexHooks(home, nagareBin); err != nil {
		fmt.Printf("  Hooks: Codex — skipped (%v)\n", err)
	}

	// OpenCode, pi, and OhMyPi report status through a plugin or extension
	// rather than a settings-file hook.
	if err := installOpenCodePlugin(home, nagareBin); err != nil {
		fmt.Printf("  Plugin: OpenCode — skipped (%v)\n", err)
	}
	if err := installPiExtension(home, nagareBin); err != nil {
		fmt.Printf("  Extension: pi — skipped (%v)\n", err)
	}
	if err := installOhMyPiExtension(home, nagareBin); err != nil {
		fmt.Printf("  Extension: OhMyPi — skipped (%v)\n", err)
	}

	// Register MCP server in all supported agents

	// Claude Code, Gemini CLI, and OhMyPi use standard mcpServers format.
	for _, mc := range []struct{ name, path string }{
		{"Claude Code", filepath.Join(home, ".claude.json")},
		{"Gemini CLI", filepath.Join(home, ".gemini", "settings.json")},
		{"OhMyPi", filepath.Join(home, ".omp", "agent", "mcp.json")},
	} {
		if err := registerMCPStandard(mc.path, nagareBin); err != nil {
			fmt.Printf("  MCP server: %s — skipped (%v)\n", mc.name, err)
		} else {
			fmt.Printf("  MCP server: %s — %s\n", mc.name, mc.path)
		}
	}

	// Codex uses TOML tables for stdio MCP servers.
	codexConfig := filepath.Join(home, ".codex", "config.toml")
	if err := registerMCPCodex(codexConfig, nagareBin); err != nil {
		fmt.Printf("  MCP server: Codex — skipped (%v)\n", err)
	} else {
		fmt.Printf("  MCP server: Codex — %s\n", codexConfig)
	}

	// OpenCode and Crush use "mcp" key with type/command format.
	// OpenCode's global config is opencode.json — config.json was the old name
	// and current versions ignore it.
	for _, mc := range []struct{ name, path string }{
		{"OpenCode", filepath.Join(home, ".config", "opencode", "opencode.json")},
		{"Crush", filepath.Join(home, ".config", "crush", "crush.json")},
	} {
		if err := registerMCPLocal(mc.path, nagareBin); err != nil {
			fmt.Printf("  MCP server: %s — skipped (%v)\n", mc.name, err)
		} else {
			fmt.Printf("  MCP server: %s — %s\n", mc.name, mc.path)
		}
	}

	// Install slash commands for all supported agent CLIs
	installCommands(home)

	fmt.Println("\nSetup complete!")
	return nil
}

// registerMCPCodex adds Nagare's stdio server to Codex's TOML config while
// preserving comments, ordering, and every unrelated user setting.
func registerMCPCodex(configPath, nagareBin string) error {
	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot read %s: %w", configPath, err)
	}
	if len(data) > 0 {
		var cfg map[string]interface{}
		if _, err := toml.Decode(string(data), &cfg); err != nil {
			return fmt.Errorf("invalid TOML in %s: %w", configPath, err)
		}
	}

	const section = "mcp_servers.nagare"
	var kept []string
	skipping := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			name := strings.TrimSpace(strings.Trim(trimmed, "[]"))
			skipping = name == section || strings.HasPrefix(name, section+".")
		}
		if !skipping {
			kept = append(kept, line)
		}
	}

	base := strings.TrimSpace(strings.Join(kept, "\n"))
	// Codex hands stdio servers a filtered environment, so the pane id — how
	// nagare knows which agent is calling — must be forwarded explicitly. Its
	// default 60s tool timeout would also cut send_message_and_wait short.
	entry := fmt.Sprintf("[mcp_servers.nagare]\ncommand = %q\nargs = [\"mcp\"]\nenv_vars = [\"TMUX_PANE\", \"TMUX\"]\ntool_timeout_sec = 900", nagareBin)
	out := entry + "\n"
	if base != "" {
		out = base + "\n\n" + out
	}
	var cfg map[string]interface{}
	if _, err := toml.Decode(out, &cfg); err != nil {
		return fmt.Errorf("cannot encode Codex MCP config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(configPath, []byte(out), 0644)
}

// installCodexHooks installs user-level lifecycle hooks. Codex merges hook
// sources, so unrelated hooks in this file and hooks from other layers remain
// active. Codex asks the user to review new command hooks in /hooks.
func installCodexHooks(home, nagareBin string) error {
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	hookCmd := nagareBin + " hook-state"

	settings, err := loadJSON(hooksPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot read %s: %w", hooksPath, err)
	}
	if settings == nil {
		settings = make(map[string]interface{})
	}
	hooksMap, _ := settings["hooks"].(map[string]interface{})
	if hooksMap == nil {
		hooksMap = make(map[string]interface{})
	}
	for event := range hooksMap {
		hooksMap[event] = removeNagareHooks(hooksMap[event], "nagare-go hook-state")
		hooksMap[event] = removeNagareHooks(hooksMap[event], "nagare hook-state")
	}
	for _, event := range codexHookEvents {
		timeout := 5
		if event == "SessionEnd" {
			timeout = 3
		}
		hooksMap[event] = appendHookEntry(hooksMap[event], map[string]interface{}{
			"hooks": []interface{}{map[string]interface{}{
				"type":    "command",
				"command": hookCmd,
				"timeout": timeout,
			}},
		})
	}
	settings["hooks"] = hooksMap

	if err := os.MkdirAll(filepath.Dir(hooksPath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(hooksPath, data, 0644); err != nil {
		return err
	}
	fmt.Printf("  Hooks installed: Codex — %s (review once with /hooks)\n", hooksPath)
	return nil
}

// registerMCPStandard adds nagare to the standard mcpServers format
// used by Claude Code, Gemini CLI, Cursor, etc.
func registerMCPStandard(configPath, nagareBin string) error {
	cfg, err := loadJSON(configPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot read %s: %w", configPath, err)
	}
	if cfg == nil {
		cfg = make(map[string]interface{})
	}

	servers, _ := cfg["mcpServers"].(map[string]interface{})
	if servers == nil {
		servers = make(map[string]interface{})
	}

	servers["nagare"] = map[string]interface{}{
		"command": nagareBin,
		"args":    []string{"mcp"},
	}
	cfg["mcpServers"] = servers

	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return err
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, out, 0644)
}

// registerMCPLocal adds nagare to the "mcp" key config format
// used by OpenCode and Crush.
func registerMCPLocal(configPath, nagareBin string) error {
	cfg, err := loadJSON(configPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot read %s: %w", configPath, err)
	}
	if cfg == nil {
		cfg = make(map[string]interface{})
	}

	mcpMap, _ := cfg["mcp"].(map[string]interface{})
	if mcpMap == nil {
		mcpMap = make(map[string]interface{})
	}

	mcpMap["nagare"] = map[string]interface{}{
		"type":    "local",
		"command": []string{nagareBin, "mcp"},
		"enabled": true,
	}
	cfg["mcp"] = mcpMap

	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return err
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, out, 0644)
}

func installClaudeHooks(home, nagareBin string) error {
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	hookCmd := nagareBin + " hook-state"

	// Load existing settings
	settings, err := loadJSON(settingsPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot read %s: %w", settingsPath, err)
	}
	if settings == nil {
		settings = make(map[string]interface{})
	}

	// Get or create hooks map
	hooksMap, _ := settings["hooks"].(map[string]interface{})
	if hooksMap == nil {
		hooksMap = make(map[string]interface{})
	}

	// Remove stale nagare hooks from all events
	for event := range hooksMap {
		hooksMap[event] = removeNagareHooks(hooksMap[event], "nagare-go hook-state")
		// Also remove old Python nagare hooks
		hooksMap[event] = removeNagareHooks(hooksMap[event], "nagare hook-state")
	}

	// Hook command entry
	hookEntry := map[string]interface{}{
		"type":    "command",
		"command": hookCmd,
		"timeout": 5,
	}

	// Standard events: matcher="" matches all
	for _, event := range hookEvents {
		hooksMap[event] = appendHookEntry(hooksMap[event], map[string]interface{}{
			"matcher": "",
			"hooks":   []interface{}{hookEntry},
		})
	}

	// Notification event has a specific matcher
	hooksMap["Notification"] = appendHookEntry(
		removeNagareHooks(hooksMap["Notification"], "nagare-go hook-state"),
		map[string]interface{}{
			"matcher": notificationMatcher,
			"hooks":   []interface{}{hookEntry},
		},
	)

	settings["hooks"] = hooksMap

	// Write back
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(settingsPath, data, 0644); err != nil {
		return err
	}

	fmt.Printf("  Hooks installed: %s\n", settingsPath)
	fmt.Printf("  Command: %s\n", hookCmd)
	fmt.Printf("  Events: %s, Notification\n", strings.Join(hookEvents, ", "))
	return nil
}

// Gemini CLI hook events that map to nagare state changes.
var geminiHookEvents = []string{
	"BeforeTool",
	"AfterAgent",
	"SessionEnd",
}

func installGeminiHooks(home, nagareBin string) error {
	settingsPath := filepath.Join(home, ".gemini", "settings.json")
	hookCmd := nagareBin + " hook-state"

	settings, err := loadJSON(settingsPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot read %s: %w", settingsPath, err)
	}
	if settings == nil {
		settings = make(map[string]interface{})
	}

	hooksMap, _ := settings["hooks"].(map[string]interface{})
	if hooksMap == nil {
		hooksMap = make(map[string]interface{})
	}

	// Remove stale nagare hooks
	for event := range hooksMap {
		hooksMap[event] = removeNagareHooks(hooksMap[event], "nagare-go hook-state")
		hooksMap[event] = removeNagareHooks(hooksMap[event], "nagare hook-state")
	}

	hookEntry := map[string]interface{}{
		"name":    "nagare",
		"type":    "command",
		"command": hookCmd,
	}

	for _, event := range geminiHookEvents {
		hooksMap[event] = appendHookEntry(hooksMap[event], hookEntry)
	}

	// Notification event for permission prompts
	hooksMap["Notification"] = appendHookEntry(
		removeNagareHooks(hooksMap["Notification"], "nagare-go hook-state"),
		hookEntry,
	)

	settings["hooks"] = hooksMap

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(settingsPath, data, 0644); err != nil {
		return err
	}

	fmt.Printf("  Hooks installed: Gemini CLI — %s\n", settingsPath)
	return nil
}

// loadJSON reads a JSON file into a map.
func loadJSON(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("invalid JSON in %s: %w", path, err)
	}
	return result, nil
}

// removeNagareHooks filters out matching command handlers while preserving
// unrelated handlers that happen to share the same matcher group.
func removeNagareHooks(eventVal interface{}, cmdSubstr string) interface{} {
	arr, ok := eventVal.([]interface{})
	if !ok {
		return eventVal
	}
	var kept []interface{}
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			kept = append(kept, item)
			continue
		}
		if cmd, ok := m["command"].(string); ok && strings.Contains(cmd, cmdSubstr) {
			continue
		}
		if nested, ok := m["hooks"].([]interface{}); ok {
			filtered, _ := removeNagareHooks(nested, cmdSubstr).([]interface{})
			if len(filtered) == 0 {
				continue
			}
			m["hooks"] = filtered
		}
		kept = append(kept, item)
	}
	return kept
}

// appendHookEntry appends a hook entry to an event's array.
func appendHookEntry(eventVal interface{}, entry map[string]interface{}) []interface{} {
	arr, _ := eventVal.([]interface{})
	return append(arr, entry)
}
