# nagare.nvim: Neovim as the multiplexer

An experiment: what nagare looks like if Neovim, not tmux, is where agents live.

## The idea

nagare today sits on top of tmux (camp 2 in `competitive-landscape.md`). herdr is camp 1:
a server owns the PTYs and every UI is a client. Neovim turns out to be *both* already.

| herdr needs | Neovim already has |
|---|---|
| a PTY per agent | `:terminal` (libvterm): each agent is a terminal buffer |
| workspaces | tabpages with a tab-local cwd (`:tcd`): one project per tab |
| a server that outlives the UI | `nvim --headless --listen` + `nvim --remote-ui` |
| an API for plugins | Lua |

So the plugin doesn't wrap Neovim around nagare. It *is* nagare, with Neovim as the
multiplexer. The Go binary keeps the parts that aren't UI: agent hooks, MCP, setup, and a
view of agents still running in tmux.

Because agents are buffers, you can:
- `/`-search an agent's output, yank from it, or `gf` a path it printed;
- peek at it in a float over the file you're editing and drop straight back;
- send it `@file#L10-20` from a visual selection.

This is **not a chat panel**. There's no prompt box and no chat buffer, and the agent CLIs
run unmodified and full-screen. The plugin is about **navigation**: which agent needs me,
in which project, and how to get there in one key.

## Where it stands among Neovim plugins

We surveyed the field in September 2026:
- sidekick.nvim, claudecode.nvim, claude-code.nvim, codecompanion;
- agent-session.nvim, arborist.nvim, agents.nvim;
- harpoon, toggleterm, resession, tabby.

The terminal-per-agent part is table stakes, and folke's **sidekick.nvim** does it with
more polish. Six things set nagare apart, and no single plugin combines them:

1. **Status from each agent's own hooks, for six agents.** Status (waiting / running / idle)
   is pushed within milliseconds.
   - sidekick has no status at all.
   - agent-session guesses from output silence, so it can't tell "waiting for approval"
     from "idle".
   - arborist has hooks, but only for Claude.
2. **Navigation across projects, most urgent first.** Everyone else is scoped to one
   project or cwd.
3. **Several agents per project and per worktree.** sidekick keys a session by (tool, cwd),
   so it can't run two claudes in one directory; users have asked for this, see sidekick
   discussion #268.
4. **One board for Neovim and tmux agents.**
5. **Approving from the board or picker** without entering the terminal.
6. **A headless runtime** that keeps agents *and their layout* alive (`nagare-go nvim`).

The pitch, then: *work in several projects at once; nagare tells you which agent needs you
and gets you there in one key, whether it lives in Neovim or tmux.*

nagare and sidekick don't clash: sidekick uses `<leader>a`, nagare uses `<leader>j`.

## Shape

```
┌ tab: api ●─┬ tab: web ●──────────────────────────────────────────────┐
│ your code                          │ nagare://web/fix-auth  (claude)  │
│         ╭──────────────── agents ─────────────────╮                   │
│         │ nagare   ● 2 waiting  ◐ 1 working       │                   │
│         │  ● web                     tab 2 · main │                   │
│         │  1 ● C fix-auth     waiting   ⎇ fix-auth                    │
│         │  2 ◐ X codex_01     working             │                   │
│         │  ● api                     tab 1 · main │                   │
│         │  3 ● C claude_01    waiting             │                   │
│         │    ◐ C review       working   tmux      │  ← a tmux agent    │
│         │ ⏎ jump  p peek  a new  y approve … g? help  q close         │
│         ╰─────────────────────────────────────────╯                   │
└───────────────────────────────────────────────────────────────────────┘
```

- **Project**: a git repository, shown as a tab. The project is the main checkout, so
  worktrees group under their repo.
- **Agent**: a terminal buffer named `nagare://<project>/<name>`, with filetype
  `nagare_terminal`. It is unlisted, so it doesn't clutter `:ls` or the bufferline.
- **Slot**: an agent's number, in creation order. It never reshuffles the way urgency
  does, so it becomes muscle memory: `<leader>j1`–`9`, or a digit on the board.
- **Jump**: switch to the project's tab and show the agent in the tab's agent split. The
  agent comes back in the mode you left it in (normal mode if you were reading its output).
- **Peek**: a float over whatever you're editing. It closes the moment focus leaves it.
- **Board**: every project and agent, most urgent first, live.
- **Picker**: the same list, fuzzy, in snacks' picker, with a live preview of each agent's
  screen.

## Notifications

LazyVim's toasts come from the snacks.nvim notifier, and nagare is designed around it:

- **One toast per agent.** Each toast is keyed by `nagare:<agent>`, so a "finished" toast
  replaces a "needs you" toast instead of stacking under it.
- **"Needs you" stays up until the agent is answered.** It has no timeout. The moment the
  agent leaves waiting, the toast hides itself, however it was answered: board, picker,
  peek, or its own terminal.
- **No toast when the agent is already on screen.**
- **A crash is reported; a clean exit is not.**
- **History comes free.** Every toast lands in the notifier history (`<leader>n` in
  LazyVim), which doubles as a log of what your agents did while you were elsewhere.
- **Without snacks**, the same calls go to plain `vim.notify`, minus the markdown.

Two bugs found while verifying this live:
- LazyVim routes `vim.notify` through noice, which returns its own table instead of our id
  but passes the id on to snacks. The hide has to use our id, and a regression test pins
  that.
- A toast is invisible when the terminal window isn't focused. Desktop notifications
  remain the Go side's job: `os_notify` in `~/.config/nagare/config.toml` still fires for
  agents inside Neovim. Only the tmux toast and popup are skipped.

## Status: instant, no polling

`nagare-go setup` installs hooks for every agent, and each hook runs
`nagare-go hook-state`. That command writes a state file keyed by *pane*. Inside Neovim,
`TMUX_PANE` would be the editor's own pane, shared by every agent in it. So the plugin
starts each agent with `NAGARE_PANE=nvim:<pid>:<n>`, and `hooks.PaneID()` prefers it.

The plugin watches the states directory with a libuv `fs_event`. The watcher follows
gitsigns' patterns:
- a per-file 30 ms debounce;
- a full rescan when libuv reports no filename;
- a fall back to polling on a watcher error, so `watching()` never lies;
- each apply wrapped in `pcall`.

Hook states also carry each agent's own **session id**, which is what makes resume exact.

Agents that have never reported a hook fall back to scraping their terminal buffer. A
permission prompt stops counting once a fresh input prompt is drawn below it.

## Restarting: saved agents and resume

Every agent's identity is recorded in `stdpath("data")/nagare/agents.json`: kind, name,
directory and session id.

After a restart, those agents return to the board as **saved** rows (`◌`), and nothing is
started. Jumping to one resumes it:
- with `claude --resume <id>`, `codex resume <id>` or `opencode --session <id>` when a
  hook reported the session id;
- otherwise with the agent's continue flag.

So reopening your editor doesn't launch ten agent CLIs at once. On the board, `c` resumes
an agent that has exited and `x` forgets one.

## Persistence: `nagare-go nvim`

`nagare-go nvim [name]` attaches to a headless Neovim on
`$XDG_RUNTIME_DIR/nagare/nvim.sock`, starting it first if needed. `:Nagare detach` (or
closing the terminal) leaves the editor and every agent running. That's herdr's "server
owns the PTYs" model, built from Neovim's own remote UI.

The runtime sets `NAGARE_RUNTIME`. Since 0.10 the built-in TUI is a remote UI too, so
nothing else can tell a persistent runtime from a plain `nvim`. The first version guessed
from UI channels and was wrong in plain LazyVim.

**A crash this uncovered.** Neovim 0.11 segfaults on a later buffer creation once
`:redrawtabline` has run with no UI attached. That is exactly a detached runtime whose
agents keep reporting status. `util.redraw()` skips redraws when no UI is attached, and a
test replays the sequence that crashed.

## tmux, still

`nagare-go ls` prints tmux agents as JSON. `internal/nvim.Entries` is a pinned contract
between the Go side and the plugin. The plugin polls it (starting only once something asks
for the list) and merges those agents into the same projects, tagged `tmux`.

## Code conventions (from folke, echasnovski, stevearc, lewis6991, mrcjkb)

- **Startup costs nothing.**
  - `plugin/nagare.lua` defines `:Nagare`, with lazy requires in its callbacks, the
    `<Plug>(nagare-*)` maps, and a scheduled `_init`.
  - The state watcher starts with the first agent, and tmux polling with the first request
    for the agent list.
  - `setup()` is optional. Options also come from `vim.g.nagare`, which can be a table or a
    function.
- **Config** is typed (LuaCATS) and validated with dotted paths; problems show in
  `:checkhealth`. Options are read as `Config.x` through a metatable, which runs setup on
  first access. Agent definitions merge with the defaults, and `agents.gemini = false`
  drops one.
- **Keymaps** map to `<Plug>` names. A `<Plug>` you bound yourself is left alone
  (`hasmapto`), and which-key gets a group label.
- **Board**: one table drives its buffer maps (each with a `desc`), its hint line and its
  `g?` help, so the three can't drift apart.
  - The hint line drops entries by priority to fit, and `q close` is always kept.
  - Highlights are extmarks.
  - A float opened over the board (a `vim.ui.select`, the help) doesn't close it.
  - It re-renders on `VimResized`.
- **Highlights** link to the `Diagnostic*` groups, so a waiting agent is exactly as red as
  an error in your colorscheme. Floats follow `'winborder'`.
- **Terminals**:
  - Each agent gets the editor's environment minus `VIMRUNTIME`/`VIM`/`MYVIMRC`, so an
    agent that opens `$EDITOR` gets a clean Neovim, plus `NVIM` pointing back here.
  - The agent split gets `winfixwidth`.
  - On exit, every job gets `jobstop` and then `jobwait`, because some CLIs ignore SIGHUP.
- **Commands** use the `{ impl, complete }` subcommand pattern. Completion is sorted and
  prefix-filtered, and an unknown subcommand lists the valid ones.

## Verified

- **63 headless specs** (`tests/nvim/`), including end to end through the real
  `nagare-go hook-state`. They pass on Neovim 0.11.4.
  - Direct binary downloads are blocked in the build sandbox, so 0.11.4 was built from
    source, with its dependencies fetched by `git`.
  - Run: `nvim --headless --clean -l tests/nvim/run.lua`.
- **Real LazyVim** (the starter, all 33 plugins installed by lazy.nvim), driven in tmux:
  - dashboard replaced by the project tab;
  - `<leader>j` shown as "+agents" in which-key, confirmed free in LazyVim;
  - snacks toasts appear, stay while waiting, and clear on approve;
  - the `"nagare"` lualine component;
  - the snacks picker, with preview and `<c-y>` approve. A clash with snacks' own `<c-p>`
    was caught here and moved to `<c-o>`;
  - restart, then saved rows, then resume by slot;
  - `:checkhealth nagare`.
- **Persistence**: three agents survived killing the terminal that held the UI, and
  reattaching restored the same tabs and splits.

## Trade-offs, honestly

- **One process holds everything.** A Neovim crash takes its agents with it, where with
  tmux the TUI can crash and the agents survive. `nagare-go nvim` narrows this: the UI can
  die, but the server can't. Saved agents and resume soften the rest. herdr's split is
  still cleaner.
- **Terminal fidelity.** libvterm is good but not tmux. Kitty graphics and some mouse
  modes don't pass through.
- **Mode friction.** This is Neovim's tax. It is reduced by `<C-q>`, by returning to the
  mode you left, and by `insert_on_jump`.

## Next, from the research (not built yet)

1. **Worktree diff review from the board.** Review an agent's worktree changes against the
   merge base: quickfix, then diffview, then merge, discard or keep. The review loop is the
   frontier, and Neovim is the best place for it.
2. **A resession extension** that restores tab and window placement along with agents.
3. **Jump-mode letters** over waiting agents, tabby/barbar style.
4. **Mailbox and MCP messaging** surfaced on the board.
5. **Opt-in claudecode.nvim interop**, passing `CLAUDE_CODE_SSE_PORT` so diffs open in
   Neovim.
6. **Screenshot tests with mini.test** (child Neovim) for the board and peek.

## Install

lazy.nvim / LazyVim, e.g. `~/.config/nvim/lua/plugins/nagare.lua`:

```lua
return {
  "nemanja-micovic/nagare-go",
  branch = "claude/nagare-herdr-nvim-plugin-8n9sdp",
  event = "VeryLazy",
  opts = {
    projects = { "~/code/*" },
    -- default_agent = "claude",
    -- layout = "vsplit",       -- or "float" to always peek
    -- agents = { gemini = false, claude = { cmd = { "claude", "--model", "opus" } } },
  },
  specs = {
    {
      "nvim-lualine/lualine.nvim",
      optional = true,
      opts = function(_, opts)
        table.insert(opts.sections.lualine_x, 1, "nagare")
      end,
    },
  },
}
```

Then run `nagare-go setup` once for instant status, and `:checkhealth nagare` to check.
Requires Neovim 0.10+ (LazyVim itself needs 0.11).

## Keys

| Key | Action |
|---|---|
| `<leader>jj` | board |
| `<leader>jf` | find agent (snacks picker, with a live preview) |
| `<leader>jw` | next waiting agent (walks the queue, wrapping) |
| `<leader>j1`–`9` | agent in that slot |
| `<leader>jp` | peek the most urgent agent |
| `<leader>ja` | toggle this project's agent split (starts one if there is none) |
| `<leader>jn` | new agent (choose which) |
| `<leader>jt` | new worktree + agent |
| `<leader>jo` | open a project |
| `<leader>js` | send `@file#Lx-y` of the selection or line |
| `<C-q>` (in agent) | back to code / close peek |

Each has a `<Plug>(nagare-…)` equivalent: board, find, next, slot-N, peek, toggle, new,
worktree, project, send.

**Board:**
- `⏎` jump · `p` peek · `a`/`A` new agent · `y`/`Y` approve (always) · `n` next waiting
- `w` worktree · `c` resume · `x` kill/forget · `r` rename · `o` project · `R` refresh
- `1`–`9` slot · `g?` help · `q` close

**Picker:** `<c-y>` approve · `<c-o>` peek · `<c-x>` kill/forget.

**Commands:** `:Nagare` followed by one of:
`board`, `pick`, `next`, `peek`, `toggle`, `slot N`, `new [agent] [dir]`,
`worktree <name> [agent]`, `project [dir]`, `send [text]`, `rename <name>`, `resume`,
`detach`.
