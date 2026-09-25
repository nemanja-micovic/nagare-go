package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nemke/nagare-go/internal/mcp"
)

// piExtensionTemplate is the nagare extension installed into pi. The single
// verb is %q, the absolute path to the nagare binary.
//
// pi has no MCP client by design, so the five nagare messaging tools are
// registered as pi tools that shell out to "nagare-go tool <name> <json>".
// That bridge calls the same handlers the MCP server calls, so pi behaves
// identically to the MCP-based agents.
//
// Incoming messages are pushed, not polled: the extension registers itself
// as the pane's listener and watches the pane's push directory, injecting
// each message with pi.sendUserMessage — as a steering message while pi is
// working, so it lands mid-turn instead of after it.
//
// Status reporting goes through "nagare-go hook-state" reading JSON on stdin,
// the same interface Claude Code and Gemini CLI hooks use. pi's docs recommend
// agent_settled rather than agent_end for status integrations, because pi may
// still auto-retry, auto-compact, or drain queued follow-ups after agent_end.
const piExtensionTemplate = `// Installed by "nagare-go setup". Regenerated on every run — edit nagare instead.
import { mkdirSync, readFileSync, readdirSync, renameSync, unlinkSync, watch, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
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
async function report(
  pi: ExtensionAPI,
  ctx: ExtensionContext,
  event: string,
  extra: Record<string, string> = {},
): Promise<void> {
  const payload = JSON.stringify({
    hook_event_name: event,
    session_id: sessionId(ctx),
    cwd: ctx.cwd,
    ...extra,
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

__SHARED__
export default function (pi: ExtensionAPI) {
  let ctxNow: ExtensionContext | undefined;
  let stopListening: (() => void) | undefined;
  process.once("exit", () => stopListening?.());

  pi.on("session_start", async (_e: unknown, ctx: ExtensionContext) => {
    ctxNow = ctx;
    stopListening?.();
    stopListening = listen(pi, () => ctxNow, "pi");
  });
  pi.on("session_shutdown", async () => {
    stopListening?.();
    stopListening = undefined;
  });

  const statusEvents = [
    "session_start",
    "before_agent_start",
    "agent_start",
    "turn_start",
    "agent_settled",
    "session_shutdown",
  ] as const;

  // agent_end carries the run's messages; agent_settled, which reports the
  // run as finished, does not — so the final text is kept until then.
  let finalText = "";
  pi.on("agent_end", async (e: any) => {
    finalText = lastAssistantText(e?.messages).slice(0, 100000);
  });

  for (const event of statusEvents) {
    pi.on(event, async (e: any, ctx: ExtensionContext) => {
      ctxNow = ctx;
      const extra: Record<string, string> = {};
      // The prompt tells nagare whether the user spoke, which cancels any
      // reply pi owed another agent.
      if (event === "before_agent_start" && typeof e?.prompt === "string") extra.prompt = e.prompt;
      if (event === "agent_settled" && finalText) {
        extra.last_assistant_message = finalText;
        finalText = "";
      }
      await report(pi, ctx, event, extra);
    });
  }

  pi.registerTool({
    name: "list_agents",
    label: "Nagare: list agents",
    description: __DESC_list_agents__,
    promptSnippet: "List other running AI agent sessions and their status",
    parameters: Type.Object({}),
    async execute(_id, _params, signal) {
      return callTool(pi, "list_agents", {}, signal);
    },
  });

  pi.registerTool({
    name: "send_message",
    label: "Nagare: send message",
    description: __DESC_send_message__,
    promptSnippet: "Send a message to another agent session",
    parameters: Type.Object({
      target: Type.String({ description: __DESC_target__ }),
      message: Type.String({ description: "message to send" }),
      expects_reply: Type.Optional(Type.Boolean({ description: __DESC_expects_reply__ })),
    }),
    async execute(_id, params, signal) {
      return callTool(pi, "send_message", params, signal);
    },
  });

  pi.registerTool({
    name: "send_message_and_wait",
    label: "Nagare: send message and wait",
    description: __DESC_send_message_and_wait__,
    promptSnippet: "Send a message to another agent session and wait for their reply",
    parameters: Type.Object({
      target: Type.String({ description: __DESC_target__ }),
      message: Type.String({ description: "message to send" }),
      timeout: Type.Optional(Type.Number({ description: "timeout in seconds (default 300)" })),
    }),
    async execute(_id, params, signal) {
      return callTool(pi, "send_message_and_wait", params, signal);
    },
  });

  pi.registerTool({
    name: "check_messages",
    label: "Nagare: check messages",
    description: __DESC_check_messages__,
    promptSnippet: "Check the nagare inbox for messages from other agents",
    parameters: Type.Object({}),
    async execute(_id, _params, signal) {
      return callTool(pi, "check_messages", {}, signal);
    },
  });

  pi.registerTool({
    name: "reply",
    label: "Nagare: reply",
    description: __DESC_reply__,
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
	content := piDescriptions().Replace(fmt.Sprintf(piExtensionTemplate, nagareBin))
	content = strings.Replace(content, "__SHARED__", piFamilyShared, 1)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return err
	}
	fmt.Printf("  Extension: pi — %s\n", path)
	return nil
}

// piDescriptions fills the tool-description placeholders in the pi extension
// with the MCP server's own wording, so pi is steered exactly like the MCP
// agents. They are substituted after Sprintf so a "%" in a description can
// never be read as a format verb.
func piDescriptions() *strings.Replacer {
	pairs := []string{
		"__DESC_target__", strconv.Quote(mcp.TargetDescription),
		"__DESC_expects_reply__", strconv.Quote(mcp.ExpectsReplyDescription),
	}
	for _, name := range mcp.ToolNames() {
		pairs = append(pairs, "__DESC_"+name+"__", strconv.Quote(mcp.ToolDescription(name)))
	}
	return strings.NewReplacer(pairs...)
}

// piPromptTemplate renders a nagare slash command as a pi prompt template.
// pi expands $ARGUMENTS, but errors on a missing argument unless a default is
// given, so the placeholder gets one.
func piPromptTemplate(description, prompt string) string {
	body := strings.ReplaceAll(prompt, "$ARGUMENTS", "${ARGUMENTS:-}")
	return fmt.Sprintf("---\ndescription: %s\n---\n\n%s\n", description, body)
}
