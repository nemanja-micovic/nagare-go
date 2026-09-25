package setup

import (
	"fmt"
	"os"
	"path/filepath"
)

// opencodePluginTemplate is the nagare status plugin installed into OpenCode.
// The single verb is %q, the absolute path to the nagare binary.
//
// OpenCode has no command-hook mechanism like Claude Code's settings.json
// hooks, so status reporting rides on its plugin event bus. Event names are
// passed through verbatim and mapped to states inside nagare's hook handler.
//
// Only the events nagare acts on are forwarded — the bus is chatty, and every
// forwarded event costs a process spawn.
//
// The plugin is also the pane's message listener: it watches the pane's push
// directory and hands each message from another agent to the session through
// the SDK client, so it arrives without anyone typing into the pane.
const opencodePluginTemplate = `// Installed by "nagare-go setup". Regenerated on every run — edit nagare instead.
import { existsSync, mkdirSync, readFileSync, readdirSync, renameSync, unlinkSync, watch, writeFileSync } from "node:fs"
import { homedir } from "node:os"
import { join } from "node:path"

const NAGARE = %q

const REPORTED = new Set([
  "session.created",
  "session.status",
  "session.idle",
  "session.error",
  "permission.asked",
  "permission.replied",
])

// Messages from other agents. nagare queues each one as a file under
// push/<pane>/; claiming it by rename means exactly one consumer delivers it.
const PANE = (process.env.TMUX_PANE ?? "").replace(/^%%/, "")
const DATA = join(homedir(), ".local", "share", "nagare")
const PUSH_DIR = join(DATA, "push", PANE)
const LISTENER = join(DATA, "listeners", PANE + ".json")

function takePushes() {
  let names
  try {
    names = readdirSync(PUSH_DIR).filter((n) => n.endsWith(".json")).sort()
  } catch {
    return []
  }
  const texts = []
  for (const name of names) {
    const claimed = join(PUSH_DIR, name + ".taken")
    try {
      renameSync(join(PUSH_DIR, name), claimed)
    } catch {
      continue // claimed by another consumer
    }
    try {
      const text = JSON.parse(readFileSync(claimed, "utf8")).text
      if (text) texts.push(text)
    } catch {}
    try {
      unlinkSync(claimed)
    } catch {}
  }
  return texts
}

export const NagarePlugin = async ({ $, client, directory, worktree }) => {
  const report = async (type, extra = {}) => {
    const payload = JSON.stringify({
      hook_event_name: type,
      session_id: "opencode-" + (worktree ?? directory ?? ""),
      cwd: directory ?? worktree ?? "",
      ...extra,
    })
    try {
      await $` + "`printf %%s ${payload} | ${NAGARE} hook-state`" + `.quiet()
    } catch {
      // Status reporting is best-effort; never break the session over it.
    }
  }

  // The top-level session the user is working in. Subagent sessions carry a
  // parentID and are never the one a message is meant for.
  let sessionID
  const children = new Set()
  const track = (event) => {
    const p = event.properties ?? {}
    const info = p.info ?? {}
    if (event.type === "session.created" || event.type === "session.updated") {
      if (info.parentID) children.add(info.id)
      else if (info.id) sessionID = info.id
      return
    }
    const id = p.sessionID ?? info.sessionID
    if (id && !children.has(id)) sessionID = id
  }

  // A prompt sent to a busy session is queued by OpenCode and picked up by
  // the loop already running, so one path serves idle and busy alike.
  const inject = async (text) => {
    if (sessionID) {
      try {
        await client.session.promptAsync({ path: { id: sessionID }, body: { parts: [{ type: "text", text }] } })
        return
      } catch {}
    }
    try {
      await client.tui.appendPrompt({ body: { text } })
      await client.tui.submitPrompt()
    } catch {}
  }

  if (PANE) {
    try {
      mkdirSync(PUSH_DIR, { recursive: true })
      mkdirSync(join(DATA, "listeners"), { recursive: true })
      writeFileSync(LISTENER, JSON.stringify({ pid: process.pid, agent: "opencode" }))
      let timer
      const deliver = () => {
        timer = undefined
        const texts = takePushes()
        if (texts.length > 0) inject(texts.join("\n\n---\n\n"))
      }
      // Coalesce the burst of events one atomic write produces.
      const schedule = () => {
        if (!timer) timer = setTimeout(deliver, 20)
      }
      watch(PUSH_DIR, schedule)
      schedule() // anything queued before OpenCode started
      process.once("exit", () => {
        try {
          unlinkSync(LISTENER)
        } catch {}
      })
    } catch {
      // Without a listener, nagare falls back to typing into the pane.
    }
  }

  // The final assistant text of a session's last turn. nagare sends it back
  // as the reply when the turn answered another agent, sparing a reply tool
  // call — so it is only fetched when this pane owes a reply at all.
  const finalText = async (id) => {
    if (!PANE || !existsSync(join(DATA, "owed", PANE + ".json"))) return ""
    try {
      const res = await client.session.messages({ path: { id } })
      const list = res?.data ?? res ?? []
      for (let i = list.length - 1; i >= 0; i--) {
        const m = list[i]
        if (m?.info?.role !== "assistant") continue
        const text = (m.parts ?? [])
          .filter((p) => p?.type === "text" && !p.synthetic && typeof p.text === "string")
          .map((p) => p.text)
          .join("\n")
          .trim()
        if (text) return text.slice(0, 100000)
      }
    } catch {}
    return ""
  }

  return {
    event: async ({ event }) => {
      track(event)
      if (!REPORTED.has(event.type)) return
      const extra = {}
      const id = event.properties?.sessionID
      if (event.type === "session.idle" && id && id === sessionID) {
        const text = await finalText(id)
        if (text) extra.last_assistant_message = text
      }
      await report(event.type, extra)
    },
    // A prompt the user types cancels any reply this pane owed another agent;
    // one nagare injected (starting "[nagare]") does not.
    "chat.message": async (_input, output) => {
      const text = (output?.parts ?? [])
        .filter((p) => p?.type === "text" && typeof p.text === "string")
        .map((p) => p.text)
        .join("\n")
      if (text) await report("chat.message", { prompt: text })
    },
  }
}
`

// installOpenCodePlugin writes the nagare status plugin into OpenCode's global
// plugin directory, where it loads for every project.
func installOpenCodePlugin(home, nagareBin string) error {
	dir := filepath.Join(home, ".config", "opencode", "plugins")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, "nagare.js")
	content := fmt.Sprintf(opencodePluginTemplate, nagareBin)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return err
	}
	fmt.Printf("  Plugin: OpenCode — %s\n", path)
	return nil
}
