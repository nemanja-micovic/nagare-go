# Launch kit

Everything needed to put nagare in front of people. Every claim below is
something the product does today; keep it that way when editing.

## Before announcing

1. The module path is `github.com/nemanja-micovic/nagare-go`, matching the repo, so
   `go install github.com/nemanja-micovic/nagare-go@latest` works once pushed.
2. Tag `v0.1.0` and push the tag: the release workflow builds Linux and macOS
   binaries for amd64 and arm64, which `install.sh` downloads.
3. Check a clean machine end to end: `install.sh`, `nagare-go demo`,
   `nagare-go setup`, `nagare-go doctor`.
4. Optional: create a `homebrew-tap` repository and add a `brews:` section to
   `.goreleaser.yaml`.

## terminaltrove listing

- **Name:** nagare
- **Tagline:** Every coding agent, one screen.
- **Description:** A terminal workspace for AI coding agents. Run Claude Code,
  Codex, OpenCode, Gemini CLI, Crush, pi and OhMyPi side by side in live tiles,
  see which one is waiting for you, answer it in place, send one prompt to all of
  them, and review what each changed as a diff — without leaving the TUI. Built on
  tmux, so agents survive nagare closing and your existing sessions just appear.
- **Language:** Go
- **License:** MIT
- **Platforms:** Linux, macOS
- **Install:** `curl -fsSL https://raw.githubusercontent.com/nemanja-micovic/nagare-go/main/install.sh | sh`
- **Try it:** `nagare-go demo`
- **Preview:** `images/demo.gif`
- **Categories:** AI, developer tools, terminal multiplexer, TUI

## Show HN / r/commandline

**Title:** Show HN: nagare – every coding agent in one terminal screen

I run several coding agents at once (Claude Code in one worktree, Codex in
another), and the part that wore me down was the switching: which one is waiting
for me, which one finished, what did it change.

nagare is a TUI for that. Enter on an agent opens its live terminal next to a
sidebar of all the others; Alt+v splits the screen into up to four live agents;
a blocked agent says "needs you" in its tile and F4 walks the queue of waiting
ones. Alt+d shows what an agent changed as a diff, Alt+b sends one prompt to
every agent on screen, and Alt+s drops you into a shell in the agent's worktree.

It is built on tmux rather than replacing it: agents are ordinary tmux sessions,
so they outlive nagare, and nagare itself does not have to run inside tmux. Agent
state comes from hooks each agent calls, not screen scraping. Redraws are driven
by tmux control-mode output events, so a keystroke's echo is on screen in about
9ms and idle agents cost nothing.

Try it without any agent or API key: `nagare-go demo` runs simulated agents in
throwaway repos on a private tmux server.

Single Go binary, MIT. Feedback welcome — especially from people running more
than two agents at a time.

## Pitch to creators

> nagare is a terminal UI for running several AI coding agents at once — Claude
> Code, Codex, OpenCode and others in live side-by-side tiles, with a queue of
> which ones are waiting on you and a diff view of what each changed.
>
> For a video it needs no setup and no API keys: `nagare-go demo` starts
> simulated agents that work, stop at permission prompts, and answer whatever you
> type. Happy to answer questions or show anything specific.

## 60-second demo script

Record with `vhs docs/demo.tape`, or live with `nagare-go demo --speed 1.5`.

| Time | Action | Say |
|------|--------|-----|
| 0–6s | The list: four agents across three repos, one red | "Four agents, three repos. One of them needs me." |
| 6–12s | Ctrl+k, type `token`, Enter | "Anything is a fuzzy search away." |
| 12–20s | The permission prompt; press Enter | "I answer it right here — no switching to tmux." |
| 20–32s | Alt+v twice | "Split. Every tile is live; the blocked one says so." |
| 32–42s | Alt+b, `summarize what you changed`, Enter | "One prompt to all of them." |
| 42–54s | Alt+← to the first tile, Alt+d, Down | "And before I trust it: the diff." |
| 54–60s | Esc, Ctrl+] | "One screen. It's nagare — try `nagare-go demo`." |
