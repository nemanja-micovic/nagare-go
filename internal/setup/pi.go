package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// piExtensionTemplate is the nagare extension installed into pi. The single
// verb is %q, the absolute path to the nagare binary.
//
// pi has no MCP client by design, so the five nagare messaging tools are
// registered as pi tools that shell out to "nagare-go tool <name> <json>".
// That bridge calls the same handlers the MCP server calls, so pi behaves
// identically to the MCP-based agents.
//
// Status reporting goes through "nagare-go hook-state" reading JSON on stdin,
// the same interface Claude Code and Gemini CLI hooks use. pi's docs recommend
// agent_settled rather than agent_end for status integrations, because pi may
// still auto-retry, auto-compact, or drain queued follow-ups after agent_end.
const piExtensionTemplate = `// Installed by "nagare-go setup". Regenerated on every run — edit nagare instead.
import { Type } from "typebox";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

const NAGARE = %q;

// pi session ids are file paths; nagare keys state files by id, so send the
// bare UUID. Ephemeral sessions (no file) fall back to the working directory.
function sessionId(ctx: ExtensionContext): string {
  const file = ctx.sessionManager?.getSessionFile?.();
  if (!file) return "pi-" + ctx.cwd;
  const base = file.split(/[/\\]/).pop() ?? file;
  return base.replace(/\.jsonl?$/, "");
}

// Report a state change to nagare. pi's event names are mapped to states inside
// nagare's hook handler, so the event name is passed through verbatim. The
// payload goes to stdin via a positional argument so no shell quoting applies
// to the JSON itself.
async function report(pi: ExtensionAPI, ctx: ExtensionContext, event: string): Promise<void> {
  const payload = JSON.stringify({
    hook_event_name: event,
    session_id: sessionId(ctx),
    cwd: ctx.cwd,
  });
  try {
    await pi.exec("sh", ["-c", 'printf %%s "$1" | "$0" hook-state', NAGARE, payload], {
      timeout: 5000,
    });
  } catch {
    // Status reporting is best-effort; never break the session over it.
  }
}

// Call a nagare tool through the CLI bridge.
async function callTool(pi: ExtensionAPI, name: string, args: unknown, signal?: AbortSignal) {
  const result = await pi.exec(NAGARE, ["tool", name, JSON.stringify(args ?? {})], {
    signal,
    timeout: 15 * 60 * 1000,
  });
  const text = (result.stdout || result.stderr || "").trim();
  if (result.code !== 0) {
    throw new Error(text || ("nagare tool " + name + " failed"));
  }
  return { content: [{ type: "text" as const, text: text || "(no output)" }] };
}

export default function (pi: ExtensionAPI) {
  const statusEvents = [
    "session_start",
    "before_agent_start",
    "agent_start",
    "turn_start",
    "agent_settled",
    "session_shutdown",
  ] as const;

  for (const event of statusEvents) {
    pi.on(event, async (_e: unknown, ctx: ExtensionContext) => {
      await report(pi, ctx, event);
    });
  }

  pi.registerTool({
    name: "list_agents",
    label: "Nagare: list agents",
    description: "List all active AI agent sessions with their status",
    promptSnippet: "List other running AI agent sessions and their status",
    parameters: Type.Object({}),
    async execute(_id, _params, signal) {
      return callTool(pi, "list_agents", {}, signal);
    },
  });

  pi.registerTool({
    name: "send_message",
    label: "Nagare: send message",
    description:
      "Send a message to another agent session. The target must be idle. This is for informational messages that don't require a reply.",
    promptSnippet: "Send a message to another agent session",
    parameters: Type.Object({
      target: Type.String({ description: "target session name" }),
      message: Type.String({ description: "message to send" }),
    }),
    async execute(_id, params, signal) {
      return callTool(pi, "send_message", params, signal);
    },
  });

  pi.registerTool({
    name: "send_message_and_wait",
    label: "Nagare: send message and wait",
    description:
      "Send a message to another agent session and wait for a reply. The target must be idle. Use this when you need a response from the other agent.",
    promptSnippet: "Send a message to another agent session and wait for their reply",
    parameters: Type.Object({
      target: Type.String({ description: "target session name" }),
      message: Type.String({ description: "message to send" }),
      timeout: Type.Optional(Type.Number({ description: "timeout in seconds (default 120)" })),
    }),
    async execute(_id, params, signal) {
      return callTool(pi, "send_message_and_wait", params, signal);
    },
  });

  pi.registerTool({
    name: "check_messages",
    label: "Nagare: check messages",
    description:
      "Check for incoming messages from other agents and responses to your messages. Call this periodically to see if you have new messages to respond to.",
    promptSnippet: "Check the nagare inbox for messages from other agents",
    parameters: Type.Object({}),
    async execute(_id, _params, signal) {
      return callTool(pi, "check_messages", {}, signal);
    },
  });

  pi.registerTool({
    name: "remember",
    label: "Nagare: remember",
    description:
      "Save a lesson for future sessions and the other agents on this repository: something learned the hard way that is not obvious from the code (a build quirk, a gotcha and its cause, a decision and why, a user preference). Not task progress, not secrets. First line is the title.",
    promptSnippet: "Save a lesson learned to the shared nagare memory",
    parameters: Type.Object({
      text: Type.String({ description: "one self-contained memory; first line is a short title" }),
      kind: Type.Optional(Type.String({ description: "gotcha, decision, convention, fact or preference" })),
      tags: Type.Optional(Type.Array(Type.String())),
      paths: Type.Optional(Type.Array(Type.String(), { description: "repo-relative files it concerns" })),
      scope: Type.Optional(Type.String({ description: "project (default) or global" })),
      supersedes: Type.Optional(Type.Array(Type.String(), { description: "ids this replaces" })),
      force: Type.Optional(Type.Boolean()),
    }),
    async execute(_id, params, signal) {
      return callTool(pi, "remember", params, signal);
    },
  });

  pi.registerTool({
    name: "recall",
    label: "Nagare: recall",
    description:
      "Search memories saved by agents and the user on this repository. Use before non-trivial work and after surprising errors; fetch full text with get_memory.",
    promptSnippet: "Search the shared nagare memory for this repository",
    parameters: Type.Object({
      query: Type.String({ description: "identifiers, error text or topics" }),
      kind: Type.Optional(Type.String()),
      scope: Type.Optional(Type.String()),
      paths: Type.Optional(Type.Array(Type.String())),
      limit: Type.Optional(Type.Number()),
    }),
    async execute(_id, params, signal) {
      return callTool(pi, "recall", params, signal);
    },
  });

  pi.registerTool({
    name: "get_memory",
    label: "Nagare: get memory",
    description: "Read memories in full, by id (from recall).",
    promptSnippet: "Read nagare memories in full",
    parameters: Type.Object({ ids: Type.Array(Type.String()) }),
    async execute(_id, params, signal) {
      return callTool(pi, "get_memory", params, signal);
    },
  });

  pi.registerTool({
    name: "update_memory",
    label: "Nagare: update memory",
    description:
      "Correct, retag, pin or archive (status=archived) a memory. The previous version is kept.",
    promptSnippet: "Correct or archive a nagare memory",
    parameters: Type.Object({
      id: Type.String(),
      text: Type.Optional(Type.String()),
      tags: Type.Optional(Type.Array(Type.String())),
      status: Type.Optional(Type.String({ description: "active or archived" })),
      pinned: Type.Optional(Type.Boolean()),
    }),
    async execute(_id, params, signal) {
      return callTool(pi, "update_memory", params, signal);
    },
  });

  pi.registerTool({
    name: "reply",
    label: "Nagare: reply",
    description:
      "Reply to a message you received. Use check_messages() to see your pending messages and their IDs.",
    promptSnippet: "Reply to a message received from another agent",
    parameters: Type.Object({
      message_id: Type.String({ description: "ID of the message to reply to" }),
      content: Type.String({ description: "reply content" }),
    }),
    async execute(_id, params, signal) {
      return callTool(pi, "reply", params, signal);
    },
  });
}
`

// installPiExtension writes the nagare extension into pi's global extension
// directory, where pi auto-discovers it for every project.
func installPiExtension(home, nagareBin string) error {
	dir := filepath.Join(home, ".pi", "agent", "extensions")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, "nagare.ts")
	content := fmt.Sprintf(piExtensionTemplate, nagareBin)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return err
	}
	fmt.Printf("  Extension: pi — %s\n", path)
	return nil
}

// piPromptTemplate renders a nagare slash command as a pi prompt template.
// pi expands $ARGUMENTS, but errors on a missing argument unless a default is
// given, so the placeholder gets one.
func piPromptTemplate(description, prompt string) string {
	body := strings.ReplaceAll(prompt, "$ARGUMENTS", "${ARGUMENTS:-}")
	return fmt.Sprintf("---\ndescription: %s\n---\n\n%s\n", description, body)
}
