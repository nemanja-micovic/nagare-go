package models

// SessionStatus represents the current state of an agent session.
type SessionStatus string

const (
	// StatusWaitingInput indicates the agent is waiting for user input.
	StatusWaitingInput SessionStatus = "waiting_input"
	// StatusRunning indicates the agent is actively working.
	StatusRunning SessionStatus = "running"
	// StatusIdle indicates the agent is idle.
	StatusIdle SessionStatus = "idle"
	// StatusDead indicates the agent process has exited.
	StatusDead SessionStatus = "dead"
	// StatusSaved indicates a saved session not currently running.
	StatusSaved SessionStatus = "saved"
)

// AgentType represents which AI coding agent is running.
type AgentType string

const (
	// AgentClaude represents the Claude Code agent.
	AgentClaude AgentType = "claude"
	// AgentOpenCode represents the OpenCode agent.
	AgentOpenCode AgentType = "opencode"
	// AgentGemini represents the Gemini agent.
	AgentGemini AgentType = "gemini"
	// AgentCrush represents the Crush agent.
	AgentCrush AgentType = "crush"
	// AgentPi represents the pi coding agent.
	AgentPi AgentType = "pi"
	// AgentOhMyPi represents the Oh My Pi coding agent.
	AgentOhMyPi AgentType = "omp"
	// AgentCodex represents the OpenAI Codex coding agent.
	AgentCodex AgentType = "codex"
	// AgentUnknown represents an unrecognized agent.
	AgentUnknown AgentType = "unknown"
)

// SessionDetails holds metadata about a session.
type SessionDetails struct {
	GitBranch    string
	Worktree     string // worktree directory basename, empty for a main checkout
	RepoName     string // repository name, shared by a repo's worktrees
	Model        string
	ContextUsage string
	LastActivity string // ISO 8601 timestamp of last hook event
	LastEvent    string // last hook event name (e.g. "Stop", "UserPromptSubmit")
}

// Session represents a discovered agent pane in tmux.
type Session struct {
	Name        string // display name (window name if multi-agent, else session name)
	SessionID   string // tmux session ID ($0, $1, etc.)
	SessionName string // actual tmux session name (for switch-client)
	Path        string
	WindowIndex int
	PaneIndex   int
	PaneID      string // tmux pane id, e.g. "%23"
	Status      SessionStatus
	AgentType   AgentType
	Details     SessionDetails
	LastMessage string
}

// SessionState is the JSON-serializable hook state written to disk.
type SessionState struct {
	State            string `json:"state"`
	SessionID        string `json:"session_id"`
	Cwd              string `json:"cwd"`
	PaneID           string `json:"pane_id,omitempty"`
	Event            string `json:"event"`
	NotificationType string `json:"notification_type,omitempty"`
	LastMessage      string `json:"last_message,omitempty"`
	Timestamp        string `json:"timestamp"`
	MessagingSocket  string `json:"messaging_socket,omitempty"` // Claude Code's cross-session inbox
	PermissionMode   string `json:"permission_mode,omitempty"`
}

// StatusColor returns the hex color for a status (tokyonight palette).
func StatusColor(s SessionStatus) string {
	switch s {
	case StatusWaitingInput:
		return "#db4b4b"
	case StatusRunning:
		return "#e0af68"
	case StatusIdle:
		return "#00D26A"
	case StatusDead:
		return "#565f89"
	case StatusSaved:
		return "#3b4261"
	default:
		return "#565f89"
	}
}

// StatusLabel returns the human-readable label for a status.
func StatusLabel(s SessionStatus) string {
	switch s {
	case StatusWaitingInput:
		return "Waiting for input"
	case StatusRunning:
		return "Working"
	case StatusIdle:
		return "Idle"
	case StatusDead:
		return "Exited"
	case StatusSaved:
		return "Not loaded"
	default:
		return "Unknown"
	}
}

// AgentLabel returns the human-readable label for an agent type.
func AgentLabel(a AgentType) string {
	switch a {
	case AgentClaude:
		return "Claude Code"
	case AgentOpenCode:
		return "OpenCode"
	case AgentGemini:
		return "Gemini"
	case AgentCrush:
		return "Crush"
	case AgentPi:
		return "Pi"
	case AgentOhMyPi:
		return "OhMyPi"
	case AgentCodex:
		return "Codex"
	default:
		return "Unknown"
	}
}

// AgentColor returns the foreground hex color for an agent type.
func AgentColor(a AgentType) string {
	switch a {
	case AgentClaude:
		return "#da7756"
	case AgentOpenCode:
		return "#00e5ff"
	case AgentGemini:
		return "#4285f4"
	case AgentCrush:
		return "#ff5fd7"
	case AgentPi:
		return "#a78bfa"
	case AgentOhMyPi:
		return "#f5c542"
	case AgentCodex:
		return "#10a37f"
	default:
		return "#565f89"
	}
}

// AgentBgColor returns the background hex color for an agent type.
func AgentBgColor(a AgentType) string {
	switch a {
	case AgentClaude:
		return "#3b2820"
	case AgentOpenCode:
		return "#002b33"
	case AgentGemini:
		return "#1a2744"
	case AgentCrush:
		return "#3b1a33"
	case AgentPi:
		return "#241b3b"
	case AgentOhMyPi:
		return "#332b12"
	case AgentCodex:
		return "#12352d"
	default:
		return ""
	}
}
