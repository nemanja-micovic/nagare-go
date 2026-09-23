-- The agent registry. An agent is a :terminal buffer running an agent CLI:
-- Neovim owns its PTY the way tmux owns a pane's, so jumping to an agent is
-- just showing a buffer.
--
-- Agents outlive their process in two ways: a dead agent keeps its buffer
-- (to read what it said last), and every agent is recorded on disk so after
-- a restart it returns as a "saved" row that resumes its session on demand.
local config = require("nagare.config")
local util = require("nagare.util")

local api = vim.api

---@class nagare.Agent
---@field id integer stable for the editor's lifetime, survives resume
---@field key string NAGARE_PANE value of the current process ("nvim:<pid>:<n>")
---@field kind string agent name in config.agents
---@field name string
---@field cwd string
---@field root string main checkout of the repository
---@field project string
---@field worktree? string
---@field branch? string
---@field status nagare.Status
---@field source "nvim"
---@field buf? integer
---@field job? integer
---@field session_id? string the agent's own session, from its hooks
---@field last_message? string
---@field notification_type? string
---@field event? string
---@field changed integer epoch of the last status change
---@field used integer epoch of the last time it was shown
---@field hooked? boolean a hook has reported, so scraping stops
---@field hook_ts? string
---@field exit_code? integer
---@field mode? "normal"|"terminal" how the user last left it

local M = {}

---@type nagare.Agent[]
M.list = {} -- in creation order; also the board's slot order
M.by_key = {} ---@type table<string, nagare.Agent>
local seq = 0

-- Loudest first. The board sorts by it, and a project takes its most urgent
-- agent's rank so a waiting worktree lifts its whole repo.
M.rank = { waiting_input = 1, running = 2, idle = 3, dead = 4, saved = 5 }

local function emit(agent, prev)
  util.redraw()
  pcall(api.nvim_exec_autocmds, "User", {
    pattern = "NagareStatus",
    modeline = false,
    data = { id = agent.id, key = agent.key, status = agent.status, prev = prev },
  })
end

--- True when the agent is on screen in the current tab, i.e. the user can
--- already see what a notification would tell them.
local function visible(agent)
  if not (agent.buf and api.nvim_buf_is_valid(agent.buf)) then
    return false
  end
  for _, win in ipairs(api.nvim_tabpage_list_wins(0)) do
    if api.nvim_win_get_buf(win) == agent.buf then
      return true
    end
  end
  return false
end

function M.label(agent)
  return agent.project .. "/" .. agent.name
end

-- Persistence ---------------------------------------------------------------

local function store_path()
  return vim.fn.stdpath("data") .. "/nagare/agents.json"
end

--- Writes every agent's identity (not its state) so it can be resumed after
--- a restart.
function M.save()
  local out = {}
  for _, a in ipairs(M.list) do
    table.insert(out, {
      kind = a.kind, name = a.name, cwd = a.cwd, root = a.root, project = a.project,
      worktree = a.worktree, session_id = a.session_id,
    })
  end
  util.write_file(store_path(), vim.json.encode(out))
end

local function add(agent)
  seq = seq + 1
  agent.id = seq
  table.insert(M.list, agent)
  return agent
end

--- Brings back the agents recorded by the last session as "saved" rows.
--- Nothing is started; jumping to one resumes it.
function M.restore()
  local data = util.json_decode(util.read_file(store_path()))
  if type(data) ~= "table" then
    return 0
  end
  local n = 0
  for _, r in ipairs(data) do
    if type(r) == "table" and r.cwd and config.agents[r.kind] and vim.fn.isdirectory(r.cwd) == 1 then
      local a = add({
        kind = r.kind, name = r.name, cwd = r.cwd, root = r.root, project = r.project,
        worktree = r.worktree, session_id = r.session_id,
        status = "saved", source = "nvim", changed = os.time(), used = 0,
      })
      a.key = "saved:" .. a.id
      M.by_key[a.key] = a
      n = n + 1
    end
  end
  return n
end

-- Status --------------------------------------------------------------------

--- Records a status change and hands the transition to the notifier.
function M.set_status(agent, status, fields)
  local prev = agent.status
  local learned_session = fields and fields.session_id and fields.session_id ~= agent.session_id
  for k, v in pairs(fields or {}) do
    agent[k] = v
  end
  if learned_session then
    M.save()
  end
  if status == prev then
    if fields then
      emit(agent, prev)
    end
    return
  end
  local now = os.time()
  local held = now - (agent.changed or now)
  agent.status = status
  agent.changed = now
  require("nagare.notify").transition(agent, prev, held, visible(agent))
  emit(agent, prev)
end

-- Processes -----------------------------------------------------------------

local function next_name(kind, root)
  local n = 0
  for _, a in ipairs(M.list) do
    if a.root == root and a.kind == kind then
      n = n + 1
    end
  end
  return ("%s_%02d"):format(kind, n + 1)
end

--- The environment an agent runs in: the editor's, minus what would make a
--- nested `nvim` (an agent opening $EDITOR) load this editor's runtime, plus
--- the id hooks report under.
local function environment(agent)
  local env = vim.fn.environ()
  for _, k in ipairs({ "VIMRUNTIME", "VIM", "MYVIMRC", "NVIM_LISTEN_ADDRESS", "NVIM_LOG_FILE" }) do
    env[k] = nil
  end
  env.NVIM = vim.v.servername
  env.NAGARE_PANE = agent.key
  env.NAGARE_PROJECT = agent.root
  return env
end

local function buffer_name(agent)
  return ("nagare://%s/%s#%d"):format(agent.project, agent.name, agent.id)
end

--- Starts (or restarts) the process behind an agent in a fresh terminal
--- buffer. Returns true, or false and an error.
local function start(agent, args)
  local spec = config.agents[agent.kind]
  local cmd = vim.list_extend(vim.deepcopy(spec.cmd), args or {})
  if vim.fn.executable(cmd[1]) ~= 1 then
    return false, ("%s is not installed (%s not in PATH)"):format(agent.kind, cmd[1])
  end
  local old, old_key = agent.buf, agent.key
  agent.key = ("nvim:%d:%d"):format(vim.fn.getpid(), agent.id)
  agent.hooked, agent.hook_ts, agent.exit_code = nil, nil, nil

  local buf = api.nvim_create_buf(false, true)
  local ok, job = pcall(api.nvim_buf_call, buf, function()
    local opts = {
      cwd = agent.cwd,
      env = environment(agent),
      clear_env = true,
      on_exit = function(_, code)
        vim.schedule(function()
          if agent.buf ~= buf then
            return -- a resumed agent's old process
          end
          agent.exit_code = code
          M.set_status(agent, "dead", { event = "exit" })
        end)
      end,
    }
    if vim.fn.has("nvim-0.11") == 1 then
      opts.term = true
      return vim.fn.jobstart(cmd, opts)
    end
    return vim.fn.termopen(cmd, opts)
  end)
  if not ok or not job or job <= 0 then
    pcall(api.nvim_buf_delete, buf, { force = true })
    agent.key = old_key
    return false, ("could not start %s: %s"):format(agent.kind, tostring(job))
  end
  if old_key then
    M.by_key[old_key] = nil
  end
  agent.buf, agent.job = buf, job
  M.by_key[agent.key] = agent

  vim.bo[buf].bufhidden = "hide"
  vim.bo[buf].buflisted = false
  vim.bo[buf].filetype = "nagare_terminal"
  vim.b[buf].nagare_agent = agent.id
  pcall(api.nvim_buf_set_name, buf, buffer_name(agent))

  local leave = type(config.keys) == "table" and config.keys.term_leave
  if leave then
    vim.keymap.set({ "t", "n" }, leave, function()
      require("nagare").leave(agent)
    end, { buffer = buf, desc = "nagare: back to code" })
  end

  -- If the old buffer (a dead agent's) is on screen, put the new one in its
  -- place rather than closing the window under the user.
  if old and old ~= buf and api.nvim_buf_is_valid(old) then
    for _, win in ipairs(api.nvim_list_wins()) do
      if api.nvim_win_get_buf(win) == old then
        pcall(api.nvim_win_set_buf, win, buf)
      end
    end
    pcall(api.nvim_buf_delete, old, { force = true })
  end
  return true
end

--- Starts an agent. opts: kind, cwd, name, args (extra CLI arguments).
---@return nagare.Agent?, string?
function M.spawn(opts)
  opts = opts or {}
  local kind = opts.kind or config.default_agent
  if not config.agents[kind] then
    return nil, ("unknown agent %q (configure it under agents)"):format(kind)
  end
  local cwd = util.normalize(opts.cwd or vim.fn.getcwd())
  local repo = util.describe(cwd)
  local agent = {
    kind = kind,
    cwd = cwd,
    root = repo.root,
    project = repo.name,
    worktree = repo.worktree,
    branch = repo.branch,
    status = "idle",
    event = "start",
    changed = os.time(),
    used = os.time(),
    source = "nvim",
  }
  agent.name = opts.name or repo.worktree or next_name(kind, repo.root)
  add(agent)
  local ok, err = start(agent, opts.args)
  if not ok then
    table.remove(M.list)
    seq = seq - 1
    return nil, err
  end
  require("nagare.status").start()
  require("nagare.projects").remember(agent.root)
  M.save()
  emit(agent, nil)
  return agent
end

--- The arguments that pick up where an agent left off: its own session when
--- a hook told us the id, else the agent's "continue the latest" flag.
function M.resume_args(agent)
  local spec = config.agents[agent.kind] or {}
  if agent.session_id and spec.resume then
    return vim.tbl_map(function(a)
      return (a:gsub("{id}", agent.session_id))
    end, spec.resume)
  end
  return spec.continue and vim.deepcopy(spec.continue) or {}
end

--- Restarts a dead or saved agent, resuming its session.
function M.resume(agent)
  if agent.status ~= "dead" and agent.status ~= "saved" then
    return true
  end
  local ok, err = start(agent, M.resume_args(agent))
  if not ok then
    return false, err
  end
  require("nagare.status").start()
  M.set_status(agent, "idle", { event = "resume" })
  return true
end

function M.rename(agent, name)
  agent.name = name
  if agent.buf and api.nvim_buf_is_valid(agent.buf) then
    pcall(api.nvim_buf_set_name, agent.buf, buffer_name(agent))
  end
  M.save()
  emit(agent, agent.status)
end

function M.get(id)
  for _, a in ipairs(M.list) do
    if a.id == id then
      return a
    end
  end
end

function M.from_buf(buf)
  local id = vim.b[buf or 0].nagare_agent
  return id and M.get(id) or nil
end

function M.for_root(root)
  return vim.tbl_filter(function(a)
    return a.root == root
  end, M.list)
end

function M.alive(agent)
  return agent.job ~= nil and agent.status ~= "dead" and agent.status ~= "saved"
end

--- The agent to show for a project: the one used most recently, preferring
--- a live one over a dead or saved one.
function M.last_used(root)
  local function live(a)
    return M.alive(a) and 1 or 0
  end
  local best
  for _, a in ipairs(M.for_root(root)) do
    -- >= so that of two used in the same second, the newer agent wins.
    if not best or live(a) > live(best) or (live(a) == live(best) and a.used >= best.used) then
      best = a
    end
  end
  return best
end

function M.touch(agent)
  agent.used = os.time()
end

function M.send(agent, text)
  if not M.alive(agent) then
    return false
  end
  return pcall(vim.fn.chansend, agent.job, text)
end

--- Answers a permission prompt. Claude's default choice is "Yes"; "always"
--- is the option below it — the same keys the tmux picker sends.
function M.approve(agent, always)
  if agent.status ~= "waiting_input" then
    return false
  end
  return M.send(agent, always and "\27[B\r" or "\r")
end

function M.kill(agent)
  if M.alive(agent) then
    pcall(vim.fn.jobstop, agent.job)
  end
end

--- Kills the agent and forgets it, here and on disk. Its buffer goes too;
--- a window showing it switches to another buffer instead of closing.
function M.remove(agent)
  M.kill(agent)
  for i, a in ipairs(M.list) do
    if a == agent then
      table.remove(M.list, i)
      break
    end
  end
  M.by_key[agent.key] = nil
  require("nagare.notify").clear(agent)
  if agent.buf and api.nvim_buf_is_valid(agent.buf) then
    for _, win in ipairs(api.nvim_list_wins()) do
      if api.nvim_win_get_buf(win) == agent.buf then
        api.nvim_win_call(win, function()
          local alt = vim.fn.bufnr("#")
          if alt > 0 and alt ~= agent.buf and api.nvim_buf_is_valid(alt) then
            api.nvim_win_set_buf(win, alt)
          else
            vim.cmd("enew")
          end
        end)
      end
    end
    pcall(api.nvim_buf_delete, agent.buf, { force = true })
  end
  agent.status = "dead"
  M.save()
  emit(agent, nil)
end

--- Stops every agent process on exit. Some CLIs ignore SIGHUP and would
--- outlive the editor, so stop them explicitly and wait briefly.
function M.shutdown()
  local jobs = {}
  for _, a in ipairs(M.list) do
    if M.alive(a) then
      pcall(vim.fn.jobstop, a.job)
      table.insert(jobs, a.job)
    end
  end
  if #jobs > 0 then
    pcall(vim.fn.jobwait, jobs, 500)
  end
end

--- Tracks whether the user left an agent in normal mode (to read or yank)
--- so a later jump restores that rather than forcing terminal mode.
function M.track_mode(ev)
  local agent = M.from_buf(ev.buf)
  if not agent or agent.leaving then
    return
  end
  local to = vim.v.event and vim.v.event.new_mode or ""
  agent.mode = to:sub(1, 1) == "t" and "terminal" or "normal"
end

-- Test hook: forget every agent without touching jobs.
function M._reset()
  M.list, M.by_key, seq = {}, {}, 0
end

return M
