---@alias nagare.Status "waiting_input"|"running"|"idle"|"dead"|"saved"
---@alias nagare.Layout "vsplit"|"split"|"current"|"float"

---@class nagare.AgentSpec
---@field cmd string[] command, no shell
---@field sigil? string one-cell letter shown on the board
---@field color? string sigil colour
---@field resume? string[] arguments that resume a session; "{id}" is its session id
---@field continue? string[] fallback when no session id is known

---@class nagare.Config
local defaults = {
  -- Agent used by :Nagare new and the board's `a` when none is named.
  default_agent = "claude",

  -- How each agent is started. Merged with yours: set one to false to drop
  -- it. `resume` restarts a known session after the editor restarted.
  ---@type table<string, nagare.AgentSpec|false>
  agents = {
    claude = { cmd = { "claude" }, sigil = "C", color = "#da7756", resume = { "--resume", "{id}" }, continue = { "--continue" } },
    codex = { cmd = { "codex" }, sigil = "X", color = "#10a37f", resume = { "resume", "{id}" }, continue = { "resume", "--last" } },
    opencode = { cmd = { "opencode" }, sigil = "O", color = "#00e5ff", resume = { "--session", "{id}" }, continue = { "--continue" } },
    gemini = { cmd = { "gemini" }, sigil = "G", color = "#4285f4" },
    crush = { cmd = { "crush" }, sigil = "R", color = "#ff5fd7" },
    pi = { cmd = { "pi" }, sigil = "P", color = "#a78bfa", continue = { "-c" } },
  },

  -- Where projects come from, besides open tabs and running agents. Globs
  -- are kept if they contain a .git.
  projects = {}, ---@type string[] e.g. { "~/code/*" }
  recent_limit = 30,

  -- Called when you open a project into a new tab (board, project picker,
  -- :Nagare project — not when jumping to an agent). "auto" opens the file
  -- picker you have: snacks (LazyVim), fzf-lua, telescope. false: nothing.
  ---@type "auto"|false|fun(root: string)
  on_project_open = "auto",

  -- Where an agent appears when you jump to it inside its project tab.
  layout = "vsplit", ---@type nagare.Layout
  size = 0.42, -- fraction of the tab the agent split takes
  -- Enter terminal mode on jump. An agent you left in normal mode (to read
  -- or yank its output) comes back in normal mode regardless.
  insert_on_jump = true,

  -- Agents from the last session come back as "saved" rows on the board;
  -- jumping to one resumes it (claude --resume <id>, codex resume <id>...).
  -- Nothing is started until you ask.
  restore = true,

  -- Hook state written by `nagare-go hook-state`, matched by NAGARE_PANE.
  states_dir = vim.fn.expand("~/.local/share/nagare/states"),
  -- Screen scraping is the fallback for agents with no hooks installed.
  scrape = true,
  poll_ms = 1500,

  -- Show tmux agents (via `nagare-go ls`) alongside the editor's own. Polling
  -- starts the first time anything asks for the agent list.
  tmux = { enabled = true, bin = "nagare-go", poll_ms = 4000 },

  notify = {
    waiting = true, -- a toast when an agent needs you; it stays until answered
    finished = true, -- a toast when a task that ran >= min_seconds finishes
    min_seconds = 20,
  },

  -- Text :Nagare send puts in front of the agent for a range. {path} is
  -- relative to the agent's directory.
  reference = "@{path}#L{from}-{to} ",

  -- Floating windows. border = nil follows 'winborder' (0.11+), else rounded.
  board = { width = 118, border = nil },
  peek = { width = 0.86, height = 0.8, border = nil },

  -- Global mappings, each to a <Plug>(nagare-*) map. Set the table to false
  -- for none, or one entry to false to drop it; a <Plug> map you bound
  -- yourself is left alone. The prefix avoids LazyVim's <leader>n
  -- (notifications) and <leader>a (AI); which-key labels it "agents".
  keys = {
    prefix = "<leader>j",
    board = "<leader>jj",
    toggle = "<leader>ja",
    next_waiting = "<leader>jw",
    peek = "<leader>jp",
    new = "<leader>jn",
    worktree = "<leader>jt",
    project = "<leader>jo",
    pick = "<leader>jf",
    send = "<leader>js",
    slots = true, -- <leader>j1..9 jump to the agent in that board slot
    -- In an agent's terminal, pressed twice: leave terminal mode, then go
    -- back to your code (or close the peek float).
    term_leave = "<C-q>",
  },

  -- Replace the tabline with project names and agent status dots.
  tabline = false,
}

local M = {}

---@type nagare.Config
local options

local function merge(user)
  local opts = vim.tbl_deep_extend("force", vim.deepcopy(defaults), user or {})
  -- Agents merge one level deep, so { claude = { cmd = {...} } } overrides
  -- just claude, and false drops an agent entirely.
  for name, spec in pairs(opts.agents) do
    if spec == false then
      opts.agents[name] = nil
    elseif type(spec) == "table" then
      local d = defaults.agents[name] or {}
      spec.sigil = spec.sigil or d.sigil or name:sub(1, 1):upper()
    end
  end
  return opts
end

local types = {
  default_agent = "string",
  agents = "table",
  projects = "table",
  recent_limit = "number",
  layout = "string",
  size = "number",
  insert_on_jump = "boolean",
  restore = "boolean",
  states_dir = "string",
  scrape = "boolean",
  poll_ms = "number",
  tmux = "table",
  notify = "table",
  reference = "string",
  board = "table",
  peek = "table",
  tabline = "boolean",
}

--- Checks options, returning a list of problems (empty when valid). Plain
--- type checks rather than vim.validate, whose signature changed in 0.11.
---@return string[]
function M.validate(opts)
  local problems = {}
  local function bad(msg)
    table.insert(problems, msg)
  end
  for key, value in pairs(opts) do
    local want = types[key]
    if key == "keys" then
      if value ~= false and type(value) ~= "table" then
        bad("keys: expected table or false")
      end
    elseif key == "on_project_open" then
      if value ~= "auto" and value ~= false and type(value) ~= "function" then
        bad('on_project_open: expected "auto", false or a function')
      end
    elseif not want then
      bad(("unknown option %q"):format(key))
    elseif type(value) ~= want then
      bad(("%s: expected %s, got %s"):format(key, want, type(value)))
    end
  end
  if not vim.tbl_contains({ "vsplit", "split", "current", "float" }, opts.layout) then
    bad(("layout: %q is not vsplit, split, current or float"):format(tostring(opts.layout)))
  end
  if type(opts.agents) == "table" then
    for name, spec in pairs(opts.agents) do
      if type(spec) ~= "table" or type(spec.cmd) ~= "table" or type(spec.cmd[1]) ~= "string" then
        bad(("agents.%s.cmd: expected a list like { %q }"):format(name, name))
      end
    end
    if not opts.agents[opts.default_agent] then
      bad(("default_agent: %q is not a configured agent"):format(tostring(opts.default_agent)))
    end
  end
  return problems
end

M.problems = {} ---@type string[]

--- Merges vim.g.nagare (a table, or a function returning one) and opts over
--- the defaults. Safe to call more than once; the last call wins.
---@param opts? table
function M.setup(opts)
  local g = vim.g.nagare
  if type(g) == "function" then
    g = g()
  end
  local user = vim.tbl_deep_extend("force", {}, type(g) == "table" and g or {}, opts or {})
  options = merge(user)
  M.problems = M.validate(options)
  if #M.problems > 0 then
    vim.notify("nagare: invalid config:\n  " .. table.concat(M.problems, "\n  "), vim.log.levels.WARN)
  end
  return options
end

M.defaults = defaults

--- Changes one option in place (tests, and plugins that want a single
--- override without re-running setup).
function M.set(key, value)
  if not options then
    M.setup()
  end
  options[key] = value
end

-- Config.layout reads the merged options, running setup() with defaults on
-- first access so the plugin works without a setup() call at all.
return setmetatable(M, {
  __index = function(_, key)
    if not options then
      M.setup()
    end
    return options[key]
  end,
})
