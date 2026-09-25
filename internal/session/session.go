package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nemanja-micovic/nagare-go/internal/config"
	"github.com/nemanja-micovic/nagare-go/internal/log"
	"github.com/nemanja-micovic/nagare-go/internal/models"
	"github.com/nemanja-micovic/nagare-go/internal/state"
	"github.com/nemanja-micovic/nagare-go/internal/tmux"
)

// ResolvePath resolves a path. If it contains no / and no ~, prepend QuickProjectPath.
func ResolvePath(path string) string {
	if !strings.Contains(path, "/") && !strings.Contains(path, "~") {
		cfg, _ := config.Load()
		return filepath.Join(cfg.Picker.QuickProjectPath, path)
	}
	return path
}

// ExpandPath expands ~ to home directory and returns absolute path.
func ExpandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, path[2:])
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

// UniqueName ensures a session name is unique by appending -N if needed.
func UniqueName(name string) string {
	existing := make(map[string]bool)
	raw := tmux.RunTmux("list-sessions", "-F", "#{session_name}")
	for _, line := range strings.Split(raw, "\n") {
		existing[strings.TrimSpace(line)] = true
	}
	if !existing[name] {
		return name
	}
	for i := 2; i < 100; i++ {
		candidate := fmt.Sprintf("%s-%d", name, i)
		if !existing[candidate] {
			return candidate
		}
	}
	return fmt.Sprintf("%s-%d", name, os.Getpid())
}

// Create creates a new tmux session with the specified agent.
func Create(path, name, agent string, continueSession bool) (string, error) {
	// If a name is given, always treat path as the parent directory.
	// ~/Projects + kenshi → ~/Projects/kenshi
	// ~/Projects/ + kenshi → ~/Projects/kenshi
	if name != "" {
		path = filepath.Join(path, name)
	}
	path = ExpandPath(ResolvePath(path))

	if err := os.MkdirAll(path, 0755); err != nil {
		return "", fmt.Errorf("cannot create directory: %w", err)
	}

	if name == "" {
		name = filepath.Base(path)
	}
	name = UniqueName(name)

	// Create tmux session
	tmux.RunTmux("new-session", "-d", "-s", name, "-c", path)

	// Launch agent
	cmd := agentCommand(agent, path, continueSession)
	tmux.RunTmux("send-keys", "-t", name, cmd, "Enter")

	// Register
	reg := state.NewRegistry(state.DefaultRegistryPath())
	reg.Register(name, path, agent)

	log.Info("created session %s (%s) at %s", name, agent, path)
	return name, nil
}

// Load restarts a previously saved session (already in the registry).
// It reuses the existing name and path, continuing the agent if possible.
func Load(path, name, agent string) (string, error) {
	path = ExpandPath(path)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("path does not exist: %s", path)
	}

	// Check if tmux session already exists
	existing := tmux.RunTmux("list-sessions", "-F", "#{session_name}")
	for _, line := range strings.Split(existing, "\n") {
		if strings.TrimSpace(line) == name {
			// Session exists but agent is dead — launch agent in it
			cmd := agentCommand(agent, path, true)
			tmux.RunTmux("send-keys", "-t", name, cmd, "Enter")
			log.Info("loaded agent in existing session %s (%s)", name, agent)
			return name, nil
		}
	}

	// Create new tmux session
	tmux.RunTmux("new-session", "-d", "-s", name, "-c", path)
	cmd := agentCommand(agent, path, true)
	tmux.RunTmux("send-keys", "-t", name, cmd, "Enter")

	reg := state.NewRegistry(state.DefaultRegistryPath())
	reg.Register(name, path, agent)

	log.Info("loaded session %s (%s) at %s", name, agent, path)
	return name, nil
}

// claudeSessionExists reports whether a previous Claude conversation exists
// for the given project directory. Claude stores conversations as .jsonl files
// under ~/.claude/projects/<path-with-slashes-as-dashes>/.
func claudeSessionExists(projectPath string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	encoded := strings.ReplaceAll(projectPath, "/", "-")
	dir := filepath.Join(home, ".claude", "projects", encoded)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			return true
		}
	}
	return false
}

// agentCommand returns the command to launch an agent.
func agentCommand(agent, projectPath string, continueSession bool) string {
	switch agent {
	case "opencode":
		if continueSession {
			return "opencode -c"
		}
		return "opencode"
	case "gemini":
		return "gemini"
	case "crush":
		return "crush"
	case "pi":
		if continueSession {
			return "pi -c"
		}
		return "pi"
	case "omp":
		if continueSession {
			return "omp -c"
		}
		return "omp"
	case "codex":
		if continueSession {
			return "codex resume --last"
		}
		return "codex"
	default: // claude
		if continueSession && claudeSessionExists(projectPath) {
			return "claude -c"
		}
		return "claude"
	}
}

// ListDirectories returns directory suggestions for path autocomplete.
func ListDirectories(partial string, maxResults int) []string {
	partial = ExpandPath(partial)

	dir := partial
	prefix := ""
	if !strings.HasSuffix(partial, "/") {
		dir = filepath.Dir(partial)
		prefix = strings.ToLower(filepath.Base(partial))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var results []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if prefix != "" && !strings.HasPrefix(strings.ToLower(e.Name()), prefix) {
			continue
		}
		results = append(results, filepath.Join(dir, e.Name()))
		if len(results) >= maxResults {
			break
		}
	}
	return results
}

// SwitchToSession switches to or attaches to a tmux session.
func SwitchToSession(name string) {
	if os.Getenv("TMUX") != "" {
		tmux.RunTmux("switch-client", "-t", name)
	} else {
		tmux.RunTmux("attach-session", "-t", name)
	}
}

// SwitchToPane switches to a specific tmux session:window.pane.
func SwitchToPane(s models.Session) {
	target := tmux.PaneTarget(s.SessionName, s.WindowIndex, s.PaneIndex)
	if os.Getenv("TMUX") != "" {
		tmux.RunTmux("select-window", "-t", fmt.Sprintf("%s:%d", s.SessionName, s.WindowIndex))
		tmux.RunTmux("select-pane", "-t", target)
		tmux.RunTmux("switch-client", "-t", s.SessionName)
	} else {
		tmux.RunTmux("attach-session", "-t", target)
	}
}
