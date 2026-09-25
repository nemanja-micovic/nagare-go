package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ohMyPiExtensionTemplate is the nagare extension installed into OhMyPi. The
// single verb is %q, the absolute path to the nagare binary.
//
// OhMyPi has a native MCP client, so messaging tools are registered through its
// mcp.json rather than duplicated here. What the extension adds is the pi
// treatment for incoming messages: it is the pane's listener, injecting each
// message with sendUserMessage (steering while OhMyPi works), and it reports
// the final text of each run so a message's answer is sent back without a
// reply tool call. That code is shared with pi (piFamilyShared).
//
// OhMyPi settles with agent_end, but agent_end also fires before an automatic
// retry; willContinue marks those, and they are not reported, or the pane
// would flicker idle — and an automatic reply would go out mid-run.
const ohMyPiExtensionTemplate = `// Installed by "nagare-go setup". Regenerated on every run — edit nagare instead.
import { mkdirSync, readFileSync, readdirSync, renameSync, unlinkSync, watch, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import type { ExtensionAPI, ExtensionContext } from "@oh-my-pi/pi-coding-agent";

const NAGARE = %q;

async function report(
  pi: ExtensionAPI,
  ctx: ExtensionContext,
  event: string,
  extra: Record<string, string> = {},
): Promise<void> {
  const payload = JSON.stringify({
    hook_event_name: event,
    session_id: ctx.sessionManager.getSessionId(),
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

__SHARED__
export default function (pi: ExtensionAPI) {
  let ctxNow: ExtensionContext | undefined;
  let stopListening: (() => void) | undefined;
  process.once("exit", () => stopListening?.());

  pi.on("session_start", async (_e: unknown, ctx: ExtensionContext) => {
    ctxNow = ctx;
    stopListening?.();
    stopListening = listen(pi, () => ctxNow, "omp");
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
    "auto_compaction_start",
    "auto_retry_start",
    "tool_approval_requested",
    "tool_approval_resolved",
    "agent_end",
    "session_shutdown",
  ] as const;

  for (const event of statusEvents) {
    pi.on(event, async (e: any, ctx: ExtensionContext) => {
      ctxNow = ctx;
      const extra: Record<string, string> = {};
      // The prompt tells nagare whether the user spoke, which cancels any
      // reply OhMyPi owed another agent.
      if (event === "before_agent_start" && typeof e?.prompt === "string") extra.prompt = e.prompt;
      if (event === "agent_end") {
        if (e?.willContinue) return; // a retry is coming: not settled yet
        const text = lastAssistantText(e?.messages).slice(0, 100000);
        if (text) extra.last_assistant_message = text;
      }
      await report(pi, ctx, event, extra);
    });
  }
}
`

// installOhMyPiExtension writes nagare's status extension into OhMyPi's global
// extension directory, where it is auto-discovered for every project.
func installOhMyPiExtension(home, nagareBin string) error {
	dir := filepath.Join(home, ".omp", "agent", "extensions")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, "nagare.ts")
	content := strings.Replace(fmt.Sprintf(ohMyPiExtensionTemplate, nagareBin), "__SHARED__", piFamilyShared, 1)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return err
	}
	fmt.Printf("  Extension: OhMyPi — %s\n", path)
	return nil
}
