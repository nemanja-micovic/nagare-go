<h1 align="center">nagare 流れ</h1>
<p align="center"><b>Every coding agent, one screen.</b><br>
Run Claude Code, Codex, OpenCode, Gemini CLI, Crush, pi and OhMyPi side by side, see which one needs you, answer it, review what it changed — without ever leaving the terminal UI.</p>

<p align="center">
  <img src="images/demo.gif" alt="nagare: agents in a sidebar, one waiting for permission, answered in place, then split into three live tiles and a diff review" width="900">
</p>

<p align="center"><i>Try it in ten seconds, no agents or API keys needed:</i> <code>nagare-go demo</code></p>

## Why nagare

- **Work inside it.** Press Enter on any agent and its live terminal opens right there, next to a sidebar of everything else that is running. Type to it, answer its permission prompts, interrupt it with Esc.
- **Watch several at once.** `Alt+v` splits the screen into up to four live agents. The one with the keyboard wears a gradient frame; the rest keep streaming and shout **needs you** the moment they block.
- **Ask them all at once.** `Alt+b` sends one prompt to every agent on screen — the same question to three agents, or "run the tests" to all of them.
- **Know who is waiting.** Agents report their state through hooks and plugins, not screen scraping, so nagare knows exactly who is working, idle or waiting for you. `F4` walks the queue of waiting agents.
- **Review before you trust.** `Alt+d` shows what an agent changed: every touched file with line counts, and git's own diff. Enter opens the file in your editor.
- **Hear about the ones you are not watching.** When an agent off the keyboard starts waiting or finishes, a toast says so in the corner of your screen.
- **Pick up where you left off.** nagare reopens the tiles you last had open, as long as their agents are still running.
- **A shell one chord away.** `Alt+s` opens a shell in the agent's directory, for the `git status` and test runs that every session ends with.
- **Worktrees built in.** `F3` starts a new agent in a fresh git worktree of the same repo, grouped under it, with the worktree kept out of your main checkout's `git status`.
- **Tickets to pull requests.** `nagare-go board` is a cross-project ticket board: run a ticket with any agent in its own branch and worktree, inspect the submitted diff, and open or recover the GitHub pull request.
- **Agents that talk to each other.** A built-in MCP server lets your agents discover, message and wait on one another.
- **Built on tmux, not instead of it.** Your agents run in ordinary tmux sessions: they survive nagare closing, you can attach to them from anywhere, and the sessions you already have show up immediately. nagare itself does not need to run inside tmux.
- **Fast and small.** One Go binary, 3ms startup, event-driven redraws (~9ms from keystroke to echo), under a millisecond a frame even with four live agents. 13 themes.

## Quick start

```bash
git clone https://github.com/nmicovic/nagare-go
cd nagare-go
./compile.bash          # or: go build -o nagare-go .

./nagare-go demo        # try it with simulated agents — nothing touches your real setup
./nagare-go setup       # connect your real agents (hooks, MCP, slash commands)
./nagare-go doctor      # check that everything is connected
./nagare-go             # open nagare
```

Requires tmux 3.2+ and git. Prebuilt binaries: see [Releases](https://github.com/nmicovic/nagare-go/releases), or

```bash
curl -fsSL https://raw.githubusercontent.com/nmicovic/nagare-go/main/install.sh | sh
```

### The demo

`nagare-go demo` starts a private tmux server with simulated Claude Code, Codex and
OpenCode agents working in throwaway git repositories. They behave like the real
thing: they report status, make real edits you can review, stop at permission
prompts that wait for your answer, and reply to whatever you type. Everything is
deleted when you quit. `--speed 2` runs them twice as fast.

## Working inside nagare

| Key | In the list | In focus mode |
|-----|-------------|---------------|
| Enter | open the agent inside nagare | *(goes to the agent)* |
| Ctrl+k / Alt+k | command palette | command palette |
| F4 | jump to the next agent waiting on you | same |
| Alt+v | | split: add the next agent as a live tile |
| Alt+← / Alt+→ | | move between tiles |
| Alt+x | | close the tile |
| Alt+b | | send one prompt to every agent on screen |
| Alt+↑ / Alt+↓ | | previous / next agent in this tile |
| Ctrl+d / Alt+d | review the agent's changes | same |
| Alt+s | | shell in the agent's directory (again: back) |
| Shift+PgUp / PgDn | | scroll back through history (the wheel works too) |
| Alt+z | | zoom: hide the sidebar |
| Ctrl+] | | back to the list (`picker.focus_leave_key`) |
| F5 | session note | |
| F6 | open the agent in tmux | same; outside tmux, detaching returns to nagare |
| F1 | every key | every key |

In focus mode everything not in this table goes straight to the agent — including
Esc, which is how you interrupt Claude Code.

<details>
<summary>All list keys</summary>

| Key | Action |
|-----|--------|
| Type | Fuzzy search |
| Tab / Shift+Tab | Cycle list / board / grid |
| Ctrl+y / Ctrl+a | Approve a permission / approve always |
| Ctrl+l / Ctrl+g | Send a prompt inline / from $EDITOR |
| Ctrl+n / Ctrl+r | New session / quick prototype |
| F2 / F3 | Name the task / new git worktree |
| Ctrl+f / Ctrl+o / Ctrl+s | Star / cycle sort / show saved sessions |
| Ctrl+w / Ctrl+x | Unload the agent / kill its window |
| Ctrl+t / Ctrl+e | Theme picker / edit config |
| Esc | Quit |

</details>

## Setup

`nagare-go setup` connects every agent you have installed:

1. **Status reporting**, so each agent tells nagare what it is doing:

   | Agent | Mechanism |
   |-------|-----------|
   | Claude Code | hooks in `~/.claude/settings.json` |
   | Codex | hooks in `~/.codex/hooks.json` |
   | Gemini CLI | hooks in `~/.gemini/settings.json` |
   | OpenCode | plugin at `~/.config/opencode/plugins/nagare.js` |
   | pi | extension at `~/.pi/agent/extensions/nagare.ts` |
   | OhMyPi (`omp`) | extension at `~/.omp/agent/extensions/nagare.ts` |

2. **The MCP server** for inter-agent messaging, registered with Claude Code, Codex, Gemini CLI, OpenCode, Crush and OhMyPi. pi has no MCP client by design, so its extension routes the same tools through `nagare-go tool`.

3. **Messaging workflows** as slash commands (`/nagare-ls`, `/nagare-send`, `/nagare-send-wait`, `/nagare-inbox`), and as Agent Skills for Codex and Crush.

Re-running `setup` is safe. Codex asks you to review newly installed hooks once: open `/hooks` in Codex and trust them.

Optionally, open nagare from tmux with a key:

```bash
# ~/.tmux.conf — prefix + g
bind g display-popup -w100% -h100% -B -E "/path/to/nagare-go"
```

## Commands

```bash
nagare-go              # open nagare (default)
nagare-go demo         # try it with simulated agents
nagare-go new ~/proj   # new session with Claude (-a codex|opencode|gemini|crush|pi|omp)
nagare-go new ~/proj -w my-feature   # new agent in a fresh git worktree
nagare-go new myproto  # quick prototype in ~/Prototypes/
nagare-go board        # cross-project tickets and isolated agent attempts
nagare-go notifs       # notification center + settings
nagare-go setup        # connect agents: status, MCP, slash commands
nagare-go doctor       # check tmux, git and every agent's connection, with fixes
nagare-go mcp          # MCP server (stdio, used by agent CLIs)
```

## Board Keybindings

| Key | Action |
|-----|--------|
| `?` | Open the four-page board field guide |
| `h/l` or arrows | Move between columns |
| `1`–`5` | Jump directly to Backlog, Ready, Running, Review, or Done |
| `j/k` or arrows | Select a ticket |
| `[` / `]` | Move ticket left / right |
| `n` | Create ticket |
| `e` | Edit ticket |
| `d` | Run a ticket with a selected agent and optional model in an isolated worktree |
| `v` | Inspect the submitted attempt's stats and scrollable diff |
| `p` | Confirm a non-force push of the recorded branch and create or recover its GitHub pull request |
| `c` | Archive a done ticket's clean worktree after its agent pane closes; retain the branch |
| `a` | Show available agents |
| Enter | Jump to the assigned agent |
| `t` | Toggle Today / All |
| Tab / Shift+Tab | Cycle list / board / grid forward or backward |
| `q` / Esc | Quit |

## Ticket Review and Pull Requests
Press `?` from the board to open the built-in field guide. Use `h/l`, the left
and right arrows, or `1`–`4` to move between Plan, Isolate, Review, and Finish.
The guide explains the complete workflow without leaving Nagare.

Each ticket records its repository and target branch. Press `d` on a Backlog or
Ready ticket to select an agent, then enter a model or leave the model empty to
use that agent's default. Agents with a per-session model option receive it when
their process starts; Crush currently uses its configured default because its
CLI has no per-session model flag. Nagare resolves the target to an immutable
base commit, creates a dedicated branch and managed worktree, launches the agent
there, and keeps the source checkout unchanged.
Managed ticket worktrees live at
`~/.local/share/nagare/workspaces/<attempt-id>/<repository>/`, on branches named
`nagare/<ticket>-<attempt>`. Archiving removes only the clean managed worktree;
the original checkout, branch, commits, pull request, and durable attempt record
remain intact.

When the agent submits the ticket through Nagare, the attempt and ticket move
to Review. The board then supports:

1. Press `v` to inspect the attempt's commit count, dirty-file count, diff
   statistics, and scrollable unified diff from its recorded base.
2. Commit any remaining work. Pull-request creation deliberately refuses dirty
   worktrees and attempts with no commits beyond their base.
3. Press `p` and confirm to push only the recorded branch to the matching
   `origin` branch with a non-force refspec. Nagare creates a GitHub pull request
   against the recorded target, or finds the existing pull request after a
   retry or interrupted creation.
4. Move the reviewed ticket to Done, close its agent pane, then press `c` to
   remove the clean managed worktree. The branch and commits remain intact.

Pull-request creation requires an authenticated
[GitHub CLI](https://cli.github.com/) (`gh`) in `PATH` and an `origin` remote.
Nagare persists the pull-request URL, number, state, and creation time on the
attempt, and shows the pull-request number on the ticket card.

## Configuration

`~/.config/nagare/config.toml` (Ctrl+e opens it):

```toml
[picker]
enter_action = "focus"       # "jump" switches to the session in tmux instead
focus_leave_key = "ctrl+]"   # e.g. "ctrl+q" where Ctrl+] is awkward to type
restore_layout = true        # reopen the tiles nagare was closed on
mouse = true
animations = true
show_help_bar = true

[appearance]
theme = "tokyonight"         # aura, catppuccin, dracula, flexoki, gruvbox, kanagawa,
                             # monokai, nord, onedark, onedarkpro, rosepine, vesper

[notifications.needs_input]
toast = true
bell = true
os_notify = true

[notifications.task_complete]
toast = true
min_working_seconds = 30
```

## How it works

tmux runs the agents; nagare is the screen you work on them from. For each agent on
screen, nagare fits its tmux window to the tile it is drawn in, and a tmux
control-mode client tells it the moment the pane prints — so it captures exactly
when something changed (a keystroke's echo is on screen in ~9ms, and an idle agent
costs nothing) — then forwards your keystrokes back with `send-keys` — so every agent works unmodified, and closing
nagare leaves every window exactly as it found it. Agent state comes from hooks and
plugins that each agent calls on every event. See [CLAUDE.md](CLAUDE.md) for the
design notes.

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Lip Gloss](https://github.com/charmbracelet/lipgloss) and [Cobra](https://github.com/spf13/cobra). A Go rewrite of [nagare](https://github.com/nmicovic/nagare), compatible with its state files and hooks.

## License

MIT
