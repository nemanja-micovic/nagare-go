package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/nemke/nagare-go/internal/memory"
)

// ToolNames returns the bridgeable tool names, sorted.
func ToolNames() []string {
	names := make([]string, 0, 5)
	for name := range bridgeTools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// bridgeTools maps a tool name to a function taking raw JSON arguments.
// Each entry calls exactly the handler the MCP server calls, so agents reaching
// nagare through the bridge behave identically to agents reaching it over MCP.
var bridgeTools = map[string]func(ctx context.Context, mySession string, args []byte) (string, error){
	"list_agents": func(_ context.Context, mySession string, _ []byte) (string, error) {
		return ListAgentsHandler(mySession), nil
	},
	"send_message": func(_ context.Context, mySession string, args []byte) (string, error) {
		var input SendMessageInput
		if err := decodeArgs(args, &input); err != nil {
			return "", err
		}
		return SendMessageHandler(mySession, input), nil
	},
	"send_message_and_wait": func(ctx context.Context, mySession string, args []byte) (string, error) {
		var input SendMessageAndWaitInput
		if err := decodeArgs(args, &input); err != nil {
			return "", err
		}
		return SendMessageAndWaitHandler(ctx, mySession, input), nil
	},
	"check_messages": func(_ context.Context, mySession string, _ []byte) (string, error) {
		return CheckMessagesHandler(mySession), nil
	},
	"reply": func(_ context.Context, mySession string, args []byte) (string, error) {
		var input ReplyInput
		if err := decodeArgs(args, &input); err != nil {
			return "", err
		}
		return ReplyHandler(mySession, input), nil
	},
	"remember": func(_ context.Context, mySession string, args []byte) (string, error) {
		var input memory.RememberInput
		if err := decodeArgs(args, &input); err != nil {
			return "", err
		}
		return RememberHandler(mySession, memoryCwd(), input), nil
	},
	"recall": func(_ context.Context, _ string, args []byte) (string, error) {
		var input memory.RecallInput
		if err := decodeArgs(args, &input); err != nil {
			return "", err
		}
		return RecallHandler(memoryCwd(), input), nil
	},
	"get_memory": func(_ context.Context, _ string, args []byte) (string, error) {
		var input memory.GetInput
		if err := decodeArgs(args, &input); err != nil {
			return "", err
		}
		return GetMemoryHandler(memoryCwd(), input), nil
	},
	"update_memory": func(_ context.Context, _ string, args []byte) (string, error) {
		var input memory.UpdateInput
		if err := decodeArgs(args, &input); err != nil {
			return "", err
		}
		return UpdateMemoryHandler(memoryCwd(), input), nil
	},
}

// RunTool invokes a nagare tool by name with raw JSON arguments and returns the
// same text an MCP client would receive. It exists for agents without an MCP
// client — pi calls it from its nagare extension via pi.exec.
func RunTool(ctx context.Context, name string, args []byte) (string, error) {
	fn, ok := bridgeTools[name]
	if !ok {
		return "", fmt.Errorf("unknown tool %q (available: %s)", name, strings.Join(ToolNames(), ", "))
	}
	return fn(ctx, resolveMySession(), args)
}

// decodeArgs unmarshals tool arguments, treating empty input as an empty object
// so tools with only optional fields can be called with no arguments.
func decodeArgs(args []byte, target any) error {
	if len(strings.TrimSpace(string(args))) == 0 {
		return nil
	}
	if err := json.Unmarshal(args, target); err != nil {
		return fmt.Errorf("invalid JSON arguments: %w", err)
	}
	return nil
}
