# nagare.nvim — the editor is the multiplexer

An experiment: what nagare looks like if Neovim, not tmux, is where agents live.

## The idea

nagare today sits on top of tmux (camp 2 in `competitive-landscape.md`). herdr is camp 1:
a server owns the PTYs and every UI is a client. Neovim turns out to be *both* already:

| herdr needs | Neovim already has |
|---|---|
| a PTY per agent | `:terminal` (libvterm): each agent is a terminal buffer |
| workspaces | tabpages with a tab-local cwd (`:tcd`): one project per tab |
| a server that outlives the UI | `nvim --headless --listen` + `nvim --remote-ui` (0.9+) |
| an API for plugins | Lua |

So the plugin doesn't wrap Neovim around nagare. It *is* nagare, with Neovim as the
multiplexer. The Go binary keeps the parts that aren't UI: agent hooks, MCP, setup, and a
view of agents still running in tmux.

What this gets you that tmux can't: **agents are buffers**. You can `/`-search an agent's
output, yank from it, `gf` on a path it printed, peek at it in a float over the file you're
editing and drop straight back, or send it `@file#L10-20` from a visual selection. No
switching between tmux and the editor, because there's only one of them.

This is not an AI chat panel. There's no prompt box and no chat buffer. The agent CLIs run
unmodified, full-screen, in their own terminals. The plugin is about **navigation**: which
agent needs me, in which project, and getting there in one key.

## Shape

```
┌ tab: api ●─┬ tab: web ●─────────────────────────────────────────────┐
│ your code                          │ nagare://web/fix-auth  (claude)  │
│                                    │ Do you want to proceed?          │
│                                    │ ❯ 1. Yes                         │
│         ╭──────────── agents ────────────────╮                        │
│         │ nagare   ● 2 waiting  ◐ 1 working  │                        │
│         │  ● web                tab 2 · main │                        │
│         │    ● C fix-auth   waiting  ⎇ fix-auth                       │
│         │    ◐ X codex_01   working          │                        │
│         │  ● api                tab 1 · main │                        │
│         │    ● C claude_01  waiting          │                        │
│         │    ◐ C api/review working  tmux    │  ← a tmux agent, same board
│         ╰────────────────────────────────────╯                        │
└───────────────────────────────────────────────────────────────────────┘
```

- **Project** = git repository (the main checkout, so worktrees group under their repo),
  shown as a tab.
- **Agent** = terminal buffer `nagare://<project>/<name>`. Hidden and unlisted, so it
  doesn't clutter `:ls` or the bufferline. Reached through the board, `next`, `toggle`
  or `peek`.
- **Jump** = open the project's tab, then show the agent in the tab's agent split (reusing
  it if one is already open).
- **Peek** = the agent in a float over whatever you're editing. It closes the moment focus
  leaves it.
- **Board** = every project and agent, most urgent first, live-updating. It's the picker's
  list view as a buffer.

## Status: instant, no polling

`nagare-go setup` already installs hooks for every agent. Each hook runs
`nagare-go hook-state`, which writes a state file keyed by *pane*. Inside Neovim,
`TMUX_PANE` would be the editor's own pane, shared by every agent in it. So the plugin
starts each agent with `NAGARE_PANE=nvim:<pid>:<n>`, and `hooks.PaneID()` prefers that
value.

The plugin watches the states directory with a libuv `fs_event`. A hook write becomes a
status change within milliseconds, with no polling. Agents that have never reported a hook
fall back to scraping their own terminal buffer, using the same patterns as
`internal/tmux/status.go`, with one improvement: a permission prompt stops counting once a
fresh input prompt is drawn below it.

When the agent is inside Neovim, the Go side skips its tmux toast, bell and popup. The
plugin announces the change with `vim.notify` instead (snacks' notifier in LazyVim),
unless that agent is already on screen.

## Persistence: `nagare-go nvim`

Agents in a plain `nvim` die with it. `nagare-go nvim [name]` attaches to a headless
Neovim on `$XDG_RUNTIME_DIR/nagare/nvim.sock`, starting it first in its own session if
needed. `:Nagare detach` (or closing the terminal) leaves the editor and every agent
running. That's herdr's "the server owns the PTYs" model, built from Neovim's own remote UI.
A name selects a separate runtime (`nagare-go nvim work`).

Verified by hand: three agents, kill the tmux server holding the UI, and the runtime still
reports three agents. Reattaching restores the same tabs, splits and terminals.

## tmux, still

`nagare-go ls` prints tmux agents as JSON (`internal/nvim.Entries`, a pinned contract). The
plugin polls it and merges those agents into the same projects, tagged `tmux`. Jumping to
one switches the tmux client, or attaches to it in a float if Neovim isn't inside tmux. So
the experiment doesn't force a migration: both worlds show on one board.

## Trade-offs, honestly

- **One process holds everything.** A Neovim crash takes its agents with it. With tmux,
  the TUI can crash and the agents survive. `nagare-go nvim` narrows this: the UI can die,
  but the server can't. herdr's split is cleaner.
- **Terminal fidelity.** libvterm is good but not tmux. Heavy TUIs render fine in practice,
  but kitty graphics and some mouse modes don't pass through.
- **Mode friction.** Terminal mode vs normal mode is Neovim's tax. `<C-q>` leaves an agent
  (closing a peek float, or returning to your code), and `insert_on_jump` puts you straight
  into typing.

## Files

| Path | Role |
|---|---|
| `plugin/nagare.lua` | `:Nagare` stub; runs `setup()` on first use if you never called it |
| `lua/nagare/init.lua` | public API, commands, keys, statusline/lualine/tabline |
| `lua/nagare/agents.lua` | registry: spawn, rename, approve, send, kill; notifications |
| `lua/nagare/projects.lua` | repo roots, tab-per-project, recent list, worktrees |
| `lua/nagare/status.lua` | state-file watcher and screen scraping |
| `lua/nagare/board.lua` | the board |
| `lua/nagare/peek.lua` | floats |
| `lua/nagare/tmux.lua` | `nagare-go ls` bridge |
| `lua/nagare/health.lua` | `:checkhealth nagare` |
| `internal/nvim` | `nagare-go ls` and `nagare-go nvim` |
| `tests/nvim` | headless specs: `nvim --headless --clean -l tests/nvim/run.lua` |

## Install

lazy.nvim / LazyVim, e.g. `~/.config/nvim/lua/plugins/nagare.lua`:

```lua
return {
  "nemanja-micovic/nagare-go",
  branch = "claude/nagare-herdr-nvim-plugin-8n9sdp",
  event = "VeryLazy", -- status must be live before you open anything
  cmd = "Nagare",
  opts = {
    projects = { "~/code/*" },
    -- default_agent = "claude",
    -- layout = "vsplit",       -- or "float" to always peek
    -- tabline = true,          -- project tabs with status dots (hide bufferline if you do)
  },
  specs = {
    -- agent counts in LazyVim's statusline
    {
      "nvim-lualine/lualine.nvim",
      optional = true,
      opts = function(_, opts)
        table.insert(opts.sections.lualine_x, 1, require("nagare").lualine())
      end,
    },
  },
}
```

Then `nagare-go setup` once for instant status. Run `:checkhealth nagare` to confirm.

LazyVim specifics, all automatic:

- Keys live under `<leader>j` (labelled "agents" in which-key). LazyVim uses `<leader>n`
  for notifications and `<leader>a` for AI.
- Pickers go through `vim.ui.select`, so they use snacks' picker, and notifications use
  snacks' notifier.
- Opening a project replaces the dashboard instead of leaving a dashboard tab behind, then
  opens `Snacks.picker.files()` in the new tab (fzf-lua or telescope if that's what you have).

## Keys

| Key | Action |
|---|---|
| `<leader>jj` | board |
| `<leader>jw` | next waiting agent (walks the queue, wrapping) |
| `<leader>jp` | peek the most urgent agent |
| `<leader>ja` | toggle this project's agent split (starts one if none) |
| `<leader>jn` | new agent in this project (choose which) |
| `<leader>jt` | new worktree + agent |
| `<leader>jo` | open a project |
| `<leader>js` | send `@file#Lx-y` of the selection / line to the project's agent |
| `<C-q>` (in agent) | back to code / close peek |

Board: `⏎` jump · `p` peek · `a`/`A` new agent · `w` worktree · `y`/`Y` approve (always) ·
`x` kill (again to remove) · `r` rename · `n` next waiting · `o` project · `R` refresh ·
`q` close.

Commands: `:Nagare [board|new [agent] [dir]|worktree <name> [agent]|next|peek|toggle|project [dir]|send [text]|rename <name>|detach]`.
