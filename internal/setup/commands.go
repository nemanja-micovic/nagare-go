package setup

import (
	"fmt"
	"os"
	"path/filepath"
)

// command templates keyed by name (without extension).
var commands = map[string]struct {
	description string
	prompt      string
}{
	"nagare-inbox": {
		description: "Check your message inbox using the nagare MCP server",
		prompt: `Check your message inbox using the nagare MCP server. Messages normally arrive in your conversation by themselves; this is the fallback.

Call check_messages() to see:
- Pending messages sent to you (respond with reply())
- Late responses to messages you sent (in case send_message_and_wait timed out)

If there are pending messages, read them carefully and use reply() to respond to each one.`,
	},
	"nagare-ls": {
		description: "List all available agent sessions using the nagare MCP server",
		prompt: `List all available agent sessions using the nagare MCP server.

Call list_agents() to show all sessions with their name, agent type, status (idle/working/waiting_input/dead), and project path.`,
	},
	"nagare-send": {
		description: "Send a message to another agent session (fire-and-forget)",
		prompt: `Send a message to another agent session with nagare's send_message tool. Call it right away — do not call list_agents first. If the message asks something, set expects_reply=true: the answer comes back into this conversation automatically when they finish. The target can be a session name, a repo or worktree name, or an agent type such as "codex" when that is unique; if it does not match, the error lists the agents.

The message is delivered straight into their conversation, even if they are busy. Their reply arrives in your conversation automatically; do not poll or call check_messages.

The user's argument is the message to send in the format: "TARGET_SESSION MESSAGE"

For example: "cosmiclab-backend Please review the API changes"

If no target is specified, call list_agents() and ask which session to message.

$ARGUMENTS`,
	},
	"nagare-send-wait": {
		description: "Send a message to another agent and wait for their response",
		prompt: `Send a message to another agent session with nagare's send_message_and_wait tool and WAIT for their response. This blocks until the other agent replies or times out (default 300s). Call it right away — do not call list_agents first. The target can be a session name, a repo or worktree name, or an agent type such as "codex" when that is unique; if it does not match, the error lists the agents. The target may be busy; the message is delivered at its next tool call.

The user's argument is the message to send in the format: "TARGET_SESSION MESSAGE"

For example: "cosmiclab-backend Can you give me the latest API docs?"

If no target is specified, call list_agents() and ask which session to message.

$ARGUMENTS`,
	},
}

// commandTarget defines where and how to write commands for an agent CLI.
type commandTarget struct {
	label  string
	dir    string
	ext    string
	format func(name, description, prompt string) string
}

// installCommands installs slash commands for all supported agent CLIs.
func installCommands(home string) {
	targets := []commandTarget{
		{
			label: "Claude Code",
			dir:   filepath.Join(home, ".claude", "commands"),
			ext:   ".md",
			format: func(_, _, prompt string) string {
				return prompt + "\n"
			},
		},
		{
			label: "Gemini CLI",
			dir:   filepath.Join(home, ".gemini", "commands"),
			ext:   ".toml",
			format: func(_, description, prompt string) string {
				return fmt.Sprintf("description = %q\nprompt = %q\n", description, prompt)
			},
		},
		{
			label: "OpenCode",
			dir:   filepath.Join(home, ".config", "opencode", "commands"),
			ext:   ".md",
			format: func(_, description, prompt string) string {
				return fmt.Sprintf("---\ndescription: %s\n---\n\n%s\n", description, prompt)
			},
		},
		{
			label: "pi",
			dir:   filepath.Join(home, ".pi", "agent", "prompts"),
			ext:   ".md",
			format: func(_, description, prompt string) string {
				return piPromptTemplate(description, prompt)
			},
		},
		{
			label: "OhMyPi",
			dir:   filepath.Join(home, ".omp", "agent", "commands"),
			ext:   ".md",
			format: func(_, description, prompt string) string {
				return fmt.Sprintf("---\ndescription: %s\n---\n\n%s\n", description, prompt)
			},
		},
	}
	for _, t := range targets {
		if err := writeCommandFiles(t); err != nil {
			fmt.Printf("  Commands: %s — skipped (%v)\n", t.label, err)
			continue
		}
		fmt.Printf("  Commands: %s — %s\n", t.label, t.dir)
	}

	// Crush uses Agent Skills instead of slash commands
	installCrushSkill(home)
	// Codex uses Agent Skills for reusable messaging workflows.
	installCodexSkill(home)
}

// installCodexSkill writes one discoverable skill covering all Nagare MCP
// messaging workflows. Codex can invoke it automatically or via $nagare.
func installCodexSkill(home string) {
	dir := filepath.Join(home, ".codex", "skills", "nagare")
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Printf("  Skill: Codex — skipped (%v)\n", err)
		return
	}
	skill := `---
name: nagare
description: Communicate with other AI coding-agent sessions through Nagare. Use when the user asks to list agents, send or await a message, check the inbox, or reply to another agent.
---

# Nagare inter-agent messaging

Use the Nagare MCP tools directly:

- send_message(target, message, expects_reply) delivers straight into the other
  agent's conversation, even while it is busy. With expects_reply=true their
  answer comes back into yours by itself when they finish.
- send_message_and_wait(target, message, timeout) blocks until the reply comes.
- reply(message_id, content) answers mid-turn. Usually unnecessary: when a
  message says your final message is sent back, just answer in it.
- list_agents() lists sessions with their status and project path.
- check_messages() reads the inbox; a fallback, rarely needed.

Call send_message right away; do not call list_agents() first. The target can
be a session name, repo, worktree, or agent type ("claude", "pi") when unique,
several separated by commas, or "all"; an unknown name returns the list of
agents. Messages from other agents arrive in your conversation starting with
"[nagare]". When one asks for an answer, just answer: the final message of your
turn is sent back. Do not poll for replies.
`
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(skill), 0644); err != nil {
		fmt.Printf("  Skill: Codex — skipped (%v)\n", err)
		return
	}
	fmt.Printf("  Skill: Codex — %s\n", dir)
}

// installCrushSkill writes a nagare Agent Skill to ~/.config/crush/skills/nagare/SKILL.md.
func installCrushSkill(home string) {
	dir := filepath.Join(home, ".config", "crush", "skills", "nagare")
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Printf("  Skill: Crush — skipped (%v)\n", err)
		return
	}
	skill := `# Nagare — Inter-Agent Messaging

You have access to the nagare MCP server for communicating with other AI agent sessions.

## Available Tools

- **list_agents()** — List all active agent sessions with name, type, status, and path
- **send_message(target, message)** — Send a fire-and-forget message to another agent
- **send_message_and_wait(target, message, timeout)** — Send a message and block until the other agent replies (default timeout: 300s)
- **check_messages()** — Check your inbox for pending messages and late responses
- **reply(message_id, content)** — Reply to a pending message

## Workflows

### List sessions
Call list_agents() to see all available sessions.

### Send a message
Call send_message(target, message) directly — no list_agents() first. The target
can be a session, repo, worktree, or agent type when unique. The reply arrives in
your conversation by itself, starting with "[nagare]".

### Send and wait for reply
Call send_message_and_wait(target, message, timeout). The target may be busy.

### Answer a message
Messages to you arrive starting with "[nagare]". When one asks for an answer, just
answer: your final message is sent back. reply(message_id, content) answers mid-turn.

### Check inbox
Call check_messages() — reply to pending messages with reply(message_id, content).
`
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(skill), 0644); err != nil {
		fmt.Printf("  Skill: Crush — skipped (%v)\n", err)
		return
	}
	fmt.Printf("  Skill: Crush — %s\n", dir)
}

func writeCommandFiles(t commandTarget) error {
	if err := os.MkdirAll(t.dir, 0755); err != nil {
		return err
	}
	for name, cmd := range commands {
		content := t.format(name, cmd.description, cmd.prompt)
		path := filepath.Join(t.dir, name+t.ext)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return err
		}
	}
	return nil
}
