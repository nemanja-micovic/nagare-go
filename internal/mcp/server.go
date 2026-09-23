package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func textResult(s string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: s}},
	}, nil, nil
}

// Tool descriptions, shared with the pi extension so every agent is steered
// the same way. They lean on what makes messaging fast: no list_agents before
// a send, and no check_messages to receive.
const (
	descListAgents = "List the other AI agent sessions with their status. Rarely needed: send_message accepts a repo, worktree, or agent type as the target and lists the agents itself when a name does not match."
	descSend       = "Message another agent session. It is delivered straight into their conversation, even while they are busy, and their reply arrives in yours automatically — do not poll. Call this directly; you do not need list_agents first."
	descSendWait   = "Message another agent session and block until they reply, returning the reply. Use only when you cannot continue without the answer; otherwise prefer send_message, whose reply also arrives automatically."
	descCheck      = "Read your nagare inbox. Rarely needed: messages from other agents are delivered into your conversation automatically, starting with \"[nagare]\"."
	descReply      = "Answer a message another agent sent you, using the message_id shown in it. The answer is delivered straight into their conversation."
)

// TargetDescription documents the target argument of the send tools.
const TargetDescription = "who to message: a session name, or a repo, worktree, or agent type (claude, codex, pi, opencode...) if that is unique"

// ToolDescription returns the description of a nagare tool, for agents that
// register the tools themselves rather than over MCP.
func ToolDescription(name string) string {
	return map[string]string{
		"list_agents":           descListAgents,
		"send_message":          descSend,
		"send_message_and_wait": descSendWait,
		"check_messages":        descCheck,
		"reply":                 descReply,
	}[name]
}

// instructions is the MCP server's guidance, which clients such as Claude Code
// place in the system prompt. The roster is a snapshot from startup; it saves
// a list_agents call for the common case, and a stale name only costs one
// failed send, whose error lists the live agents.
func instructions(self, agents string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "nagare connects you with the other AI coding agents running in this tmux server. You are %q.\n", self)
	b.WriteString("- To message an agent, call send_message with its name. A repo, worktree, or agent type (\"codex\", \"claude\") works when unique. Do not call list_agents first; an unknown name returns the list of agents.\n")
	b.WriteString("- Messages reach the other agent at once, even while it is busy. Their reply arrives in your conversation by itself, as text starting with \"[nagare]\" — do not poll or call check_messages.\n")
	b.WriteString("- Messages to you arrive the same way. Answer with reply(message_id, content).\n")
	b.WriteString("- Use send_message_and_wait only when you cannot continue without the answer.\n")
	if agents != "" && agents != noAgents {
		fmt.Fprintf(&b, "\nAgents running when this session started:\n%s\n", agents)
	}
	return b.String()
}

// RunServer starts the MCP server on stdio transport.
func RunServer() error {
	sessions := scan()
	self := resolveMySessionFrom(sessions)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "nagare",
		Version: "2.0.0",
	}, &mcp.ServerOptions{Instructions: instructions(self, roster(sessions, self))})

	// The caller is resolved per call, not once: an agent's display name
	// changes when its window is renamed or a sibling pane appears.
	mcp.AddTool(server, &mcp.Tool{Name: "list_agents", Description: descListAgents},
		func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
			return textResult(ListAgentsHandler(resolveMySession()))
		})

	mcp.AddTool(server, &mcp.Tool{Name: "send_message", Description: descSend},
		func(ctx context.Context, req *mcp.CallToolRequest, input SendMessageInput) (*mcp.CallToolResult, any, error) {
			return textResult(SendMessageHandler(resolveMySession(), input))
		})

	mcp.AddTool(server, &mcp.Tool{Name: "send_message_and_wait", Description: descSendWait},
		func(ctx context.Context, req *mcp.CallToolRequest, input SendMessageAndWaitInput) (*mcp.CallToolResult, any, error) {
			return textResult(SendMessageAndWaitHandler(ctx, resolveMySession(), input))
		})

	mcp.AddTool(server, &mcp.Tool{Name: "check_messages", Description: descCheck},
		func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, any, error) {
			return textResult(CheckMessagesHandler(resolveMySession()))
		})

	mcp.AddTool(server, &mcp.Tool{Name: "reply", Description: descReply},
		func(ctx context.Context, req *mcp.CallToolRequest, input ReplyInput) (*mcp.CallToolResult, any, error) {
			return textResult(ReplyHandler(resolveMySession(), input))
		})

	return server.Run(context.Background(), &mcp.StdioTransport{})
}
