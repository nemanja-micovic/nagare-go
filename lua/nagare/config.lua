-- Configuration. Everything here has a working default; setup() merges the
-- user's table over it.
local M = {}

M.defaults = {
  -- Agent used by :Nagare new and the board's `a` when none is named.
  default_agent = "claude",

  -- How each agent is started. `cmd` is a list (no shell); `sigil` is the
  -- one-cell letter the board shows, `color` its highlight.
  agents = {
    claude = { cmd = { "claude" }, sigil = "C", color = "#da7756" },
    codex = { cmd = { "codex" }, sigil = "X", color = "#10a37f" },
    opencode = { cmd = { "opencode" }, sigil = "O", color = "#00e5ff" },
    gemini = { cmd = { "gemini" }, sigil = "G", color = "#4285f4" },
    crush = { cmd = { "crush" }, sigil = "R", color = "#ff5fd7" },
    pi = { cmd = { "pi" }, sigil = "P", color = "#a78bfa" },
  },

  -- Where projects come from, besides open tabs and running agents. Globs
  -- are expanded and kept if they contain a .git. Recent projects are
  -- remembered in stdpath("data")/nagare/projects.json.
  projects = {}, -- e.g. { "~/code/*", "~/work/*" }
  recent_limit = 30,

  -- Called when you open a project into a new tab (from the board, the
  -- project picker or :Nagare project — not when jumping to an agent), with
  -- the project root. "auto" opens the file picker you already have:
  -- snacks.nvim (LazyVim's default), then fzf-lua, then telescope. Set a
  -- function of your own, or false for an empty window.
  on_project_open = "auto",

  -- Where an agent appears when you jump to it inside its project tab:
  -- "vsplit" | "split" | "current" | "float".
  layout = "vsplit",
  size = 0.42, -- fraction of the tab the agent split takes
  insert_on_jump = true, -- enter terminal mode on jump, ready to type

  -- Hook state written by `nagare-go hook-state`. Agents are matched by the
  -- NAGARE_PANE id the plugin gives them.
  states_dir = vim.fn.expand("~/.local/share/nagare/states"),

  -- Pane scraping is the fallback for agents with no hooks installed.
  scrape = true,
  poll_ms = 1500,

  -- Show tmux agents (via `nagare-go ls`) alongside the editor's own.
  tmux = { enabled = true, bin = "nagare-go", poll_ms = 4000 },

  notify = {
    waiting = true, -- an agent needs you
    finished = true, -- an agent finished a task that ran at least min_seconds
    min_seconds = 20,
  },

  -- Text sent by :Nagare send for a visual range. {path} is relative to the
  -- project root.
  reference = "@{path}#L{from}-{to} ",

  -- Global mappings. Set to false to define your own, or set one to false
  -- to drop it. The prefix avoids LazyVim's defaults (<leader>n is its
  -- notification history, <leader>a its AI group); which-key, when present,
  -- labels it "agents".
  keys = {
    prefix = "<leader>j",
    board = "<leader>jj",
    toggle = "<leader>ja",
    next_waiting = "<leader>jw",
    peek = "<leader>jp",
    new = "<leader>jn",
    worktree = "<leader>jt",
    project = "<leader>jo",
    send = "<leader>js",
    -- In an agent's terminal: leave terminal mode and return to your code
    -- (or close the peek float).
    term_leave = "<C-q>",
  },

  -- Replace the tabline with project names and agent status dots.
  tabline = false,
}

M.options = vim.deepcopy(M.defaults)

function M.setup(opts)
  M.options = vim.tbl_deep_extend("force", vim.deepcopy(M.defaults), opts or {})
  -- A user who lists their own agents means that list, not a merge with ours
  -- that would resurrect agents they removed.
  if opts and opts.agents then
    M.options.agents = vim.tbl_deep_extend("force", {}, opts.agents)
    for name, spec in pairs(M.options.agents) do
      local d = M.defaults.agents[name] or {}
      spec.sigil = spec.sigil or d.sigil or name:sub(1, 1):upper()
      spec.color = spec.color or d.color
    end
  end
  return M.options
end

return M
