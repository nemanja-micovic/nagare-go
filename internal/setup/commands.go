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
		prompt: `Check your message inbox using the nagare MCP server.

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
		prompt: `Send a message to another agent session using the nagare MCP server (fire-and-forget, does not wait for response). Use check_messages() later to see if they responded.

First call list_agents() to find available sessions, then call send_message() with the target and message.

The user's argument is the message to send in the format: "TARGET_SESSION MESSAGE"

For example: "cosmiclab-backend Please review the API changes"

If no target is specified, call list_agents() and ask which session to message.

$ARGUMENTS`,
	},
	"nagare-send-wait": {
		description: "Send a message to another agent and wait for their response",
		prompt: `Send a message to another agent session using the nagare MCP server and WAIT for their response. This blocks until the other agent replies or times out.

First call list_agents() to find available sessions and verify the target is IDLE, then call send_message_and_wait() with the target, message, and a reasonable timeout (default 120s).

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
description: Communicate with other AI coding-agent sessions through Nagare, and share what you learn through its memory. Use when the user asks to list agents, send or await a message, check the inbox, or reply to another agent; recall memories before non-trivial work; remember lessons learned the hard way.
---

# Nagare inter-agent messaging

Use the Nagare MCP tools directly:

- list_agents() lists sessions with their status and project path.
- send_message(target, message) sends without waiting.
- send_message_and_wait(target, message, timeout) waits for a reply.
- check_messages() reads pending messages and late responses.
- reply(message_id, content) answers a pending message.

Before sending, call list_agents() to resolve the target. Prefer an idle target
before waiting for a response. Reply to every pending message that calls for an
answer.

# Nagare memory

Memories are shared by every agent on this repository and survive sessions:

- recall(query) searches them; use it before non-trivial work and after a
  surprising error. get_memory(ids) reads them in full.
- remember(text, kind) saves a lesson learned the hard way: a build or test
  quirk, a gotcha and its root cause, a decision and why, a user preference.
  Not task progress, not what the code already says, never secrets.
- update_memory(id, ...) corrects, pins or archives one that is wrong.
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
- **send_message_and_wait(target, message, timeout)** — Send a message and block until the other agent replies (default timeout: 120s)
- **check_messages()** — Check your inbox for pending messages and late responses
- **reply(message_id, content)** — Reply to a pending message

## Workflows

### List sessions
Call list_agents() to see all available sessions.

### Send a message (fire-and-forget)
1. Call list_agents() to find the target
2. Call send_message(target, message)
3. Later, call check_messages() to see if they responded

### Send and wait for reply
1. Call list_agents() and verify target is IDLE
2. Call send_message_and_wait(target, message, timeout)

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
