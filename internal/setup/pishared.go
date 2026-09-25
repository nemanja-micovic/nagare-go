package setup

// piFamilyShared is the TypeScript the pi and OhMyPi extensions have in
// common: the message listener and the final-text extraction behind
// automatic replies. OhMyPi is a pi fork whose extension API matches pi's for
// everything used here — sendUserMessage with deliverAs "steer", isIdle,
// agent_end carrying the run's messages — so one copy serves both, and a fix
// cannot land in one and be forgotten in the other.
//
// It is spliced in after the template's Sprintf, so a "%" here is literal and
// must not be doubled.
const piFamilyShared = `// Messages from other agents. nagare queues each one as a file under
// push/<pane>/; claiming it by rename means exactly one consumer delivers it.
const PANE = (process.env.TMUX_PANE ?? "").replace(/^%/, "");
const DATA = join(homedir(), ".local", "share", "nagare");
const PUSH_DIR = join(DATA, "push", PANE);
const LISTENER = join(DATA, "listeners", PANE + ".json");

function takePushes(): string[] {
  let names: string[];
  try {
    names = readdirSync(PUSH_DIR).filter((n) => n.endsWith(".json")).sort();
  } catch {
    return [];
  }
  const texts: string[] = [];
  for (const name of names) {
    const claimed = join(PUSH_DIR, name + ".taken");
    try {
      renameSync(join(PUSH_DIR, name), claimed);
    } catch {
      continue; // claimed by another consumer
    }
    try {
      const text = JSON.parse(readFileSync(claimed, "utf8")).text;
      if (text) texts.push(text);
    } catch {}
    try {
      unlinkSync(claimed);
    } catch {}
  }
  return texts;
}

// Watch the push directory and inject messages as they arrive. Returns a
// function that stops listening.
function listen(pi: ExtensionAPI, current: () => ExtensionContext | undefined, agent: string): () => void {
  if (!PANE) return () => {};
  try {
    mkdirSync(PUSH_DIR, { recursive: true });
    mkdirSync(join(DATA, "listeners"), { recursive: true });
    writeFileSync(LISTENER, JSON.stringify({ pid: process.pid, agent }));
  } catch {
    return () => {};
  }
  let timer: ReturnType<typeof setTimeout> | undefined;
  const deliver = () => {
    timer = undefined;
    const texts = takePushes();
    if (texts.length === 0) return;
    const text = texts.join("\n\n---\n\n");
    const ctx = current();
    try {
      if (ctx && !ctx.isIdle()) pi.sendUserMessage(text, { deliverAs: "steer" });
      else pi.sendUserMessage(text);
    } catch {
      try {
        pi.sendUserMessage(text, { deliverAs: "followUp" });
      } catch {}
    }
  };
  // Coalesce the burst of events one atomic write produces.
  const schedule = () => {
    if (!timer) timer = setTimeout(deliver, 20);
  };
  const watcher = watch(PUSH_DIR, schedule);
  schedule(); // anything queued before the agent started
  return () => {
    watcher.close();
    try {
      unlinkSync(LISTENER);
    } catch {}
  };
}

// The text of the last assistant message in a run. nagare sends it back as
// the reply when the run was answering a message from another agent, which
// saves the agent a reply tool call. Content is a string or an array of parts.
function lastAssistantText(messages: unknown): string {
  if (!Array.isArray(messages)) return "";
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i] as { role?: string; content?: unknown };
    if (m?.role !== "assistant") continue;
    if (typeof m.content === "string" && m.content.trim()) return m.content;
    if (Array.isArray(m.content)) {
      const text = m.content
        .filter((c: any) => c?.type === "text" && typeof c.text === "string")
        .map((c: any) => c.text)
        .join("\n")
        .trim();
      if (text) return text;
    }
  }
  return "";
}

`
