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
	descSend       = "Message other agent sessions. Delivered straight into their conversation, even while they are busy. Set expects_reply when you ask something: the answer comes back into your conversation automatically when they finish, while you keep working — do not poll. Call it directly, without list_agents. target may name several agents separated by commas, or be \"all\"."
	descSendWait   = "Message another agent session and block until it answers, returning the answer. Most agents answer simply by finishing their turn, so this returns as soon as they are done. Use it when you cannot continue without the answer."
	descCheck      = "Read your nagare inbox. Rarely needed: messages from other agents are delivered into your conversation automatically, starting with \"[nagare]\"."
	descReply      = "Answer a message another agent sent you, using the message_id shown in it. Usually unnecessary: when a message says your final message is sent back, just answer in it. Use this to answer mid-turn, or a message you were not asked to answer."
)

// TargetDescription documents the target argument of the send tools.
const TargetDescription = "who to message: a session name, or a repo, worktree, or agent type (claude, codex, pi, opencode...) if that is unique; several separated by commas, or \"all\""

// ExpectsReplyDescription documents send_message's expects_reply argument.
const ExpectsReplyDescription = "true when you are asking something: their answer comes back to you automatically when they finish their turn, and you can keep working meanwhile"

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
	b.WriteString("- Messages to you arrive the same way. When one asks for an answer, just answer: the final message of your turn is sent back to the sender. reply(message_id, content) is for answering mid-turn.\n")
	b.WriteString("- target can list several agents separated by commas, or be \"all\".\n")
	b.WriteString("- Asking something? Use send_message with expects_reply=true: the answer arrives by itself when they finish. Use send_message_and_wait only when you cannot continue without it.\n")
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
		Version: "2.1.0",
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
