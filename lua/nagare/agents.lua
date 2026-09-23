-- The agent registry. An agent is a :terminal buffer running an agent CLI:
-- Neovim owns its PTY the way tmux owns a pane's, so jumping to an agent is
-- just showing a buffer.
local config = require("nagare.config")
local util = require("nagare.util")

local api = vim.api

local M = {}

M.list = {} -- in creation order
M.by_key = {} -- NAGARE_PANE id -> agent
local seq = 0

-- Loudest first. The board sorts by it, and a project takes its most urgent
-- agent's rank so a waiting worktree lifts its whole repo.
M.rank = { waiting_input = 1, running = 2, idle = 3, dead = 4 }

local function emit(agent, prev)
  vim.cmd("redrawtabline")
  pcall(api.nvim_exec_autocmds, "User", {
    pattern = "NagareStatus",
    modeline = false,
    data = { id = agent.id, key = agent.key, status = agent.status, prev = prev },
  })
end

--- True when the agent is on screen in the current tab, i.e. the user can
--- already see what a notification would tell them.
local function visible(agent)
  if not agent.buf then
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

--- Records a status change and fires the notifications for the two
--- transitions worth interrupting for: starting to wait, and finishing a
--- task that ran long enough to have been left alone.
function M.set_status(agent, status, fields)
  local prev = agent.status
  for k, v in pairs(fields or {}) do
    agent[k] = v
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

  local opts = config.options.notify
  if status == "waiting_input" and opts.waiting and not visible(agent) then
    vim.notify(("● %s needs you"):format(M.label(agent)), vim.log.levels.WARN, { title = "nagare" })
  elseif status == "idle" and prev == "running" and opts.finished and held >= opts.min_seconds and not visible(agent) then
    local msg = util.oneline(agent.last_message, 80)
    vim.notify(
      ("✓ %s finished after %s%s"):format(M.label(agent), util.ago(now - held), msg ~= "" and (": " .. msg) or ""),
      vim.log.levels.INFO,
      { title = "nagare" }
    )
  end
  emit(agent, prev)
end

local function next_name(kind, root)
  local n = 0
  for _, a in ipairs(M.list) do
    if a.root == root and a.kind == kind then
      n = n + 1
    end
  end
  return ("%s_%02d"):format(kind, n + 1)
end

local function start_job(cmd, opts)
  if vim.fn.has("nvim-0.11") == 1 then
    opts.term = true
    return vim.fn.jobstart(cmd, opts)
  end
  return vim.fn.termopen(cmd, opts)
end

--- Starts an agent. opts: kind, cwd, name, args (extra CLI arguments).
--- Returns the agent, or nil and an error.
function M.spawn(opts)
  opts = opts or {}
  local kind = opts.kind or config.options.default_agent
  local spec = config.options.agents[kind]
  if not spec then
    return nil, ("unknown agent %q (configure it under agents)"):format(kind)
  end
  local cmd = vim.deepcopy(spec.cmd)
  if vim.fn.executable(cmd[1]) ~= 1 then
    return nil, ("%s is not installed (%s not in PATH)"):format(kind, cmd[1])
  end
  vim.list_extend(cmd, opts.args or {})

  local cwd = util.normalize(opts.cwd or vim.fn.getcwd())
  local repo = util.describe(cwd)
  seq = seq + 1
  local agent = {
    id = seq,
    key = ("nvim:%d:%d"):format(vim.fn.getpid(), seq),
    kind = kind,
    cwd = cwd,
    root = repo.root,
    project = repo.name,
    worktree = repo.worktree,
    branch = repo.branch,
    status = "idle",
    event = "start",
    started = os.time(),
    changed = os.time(),
    used = os.time(),
    source = "nvim",
  }
  agent.name = opts.name or repo.worktree or next_name(kind, repo.root)

  local buf = api.nvim_create_buf(false, false)
  agent.buf = buf
  local ok, job = pcall(api.nvim_buf_call, buf, function()
    return start_job(cmd, {
      cwd = cwd,
      env = { NAGARE_PANE = agent.key, NAGARE_PROJECT = agent.root },
      on_exit = function(_, code)
        vim.schedule(function()
          agent.exit_code = code
          M.set_status(agent, "dead", { event = "exit" })
        end)
      end,
    })
  end)
  if not ok or not job or job <= 0 then
    pcall(api.nvim_buf_delete, buf, { force = true })
    return nil, ("could not start %s: %s"):format(kind, tostring(job))
  end
  agent.job = job

  vim.bo[buf].bufhidden = "hide"
  vim.bo[buf].buflisted = false
  vim.b[buf].nagare_agent = agent.id
  M.rename(agent, agent.name)

  local leave = config.options.keys and config.options.keys.term_leave
  if leave then
    vim.keymap.set({ "t", "n" }, leave, function()
      require("nagare").leave(agent)
    end, { buffer = buf, desc = "nagare: back to code" })
  end

  table.insert(M.list, agent)
  M.by_key[agent.key] = agent
  require("nagare.projects").remember(agent.root)
  emit(agent, nil)
  return agent
end

function M.rename(agent, name)
  agent.name = name
  if not (agent.buf and api.nvim_buf_is_valid(agent.buf)) then
    return
  end
  local old = api.nvim_buf_get_name(agent.buf)
  pcall(api.nvim_buf_set_name, agent.buf, ("nagare://%s/%s#%d"):format(agent.project, name, agent.id))
  -- Renaming a buffer leaves an unlisted buffer behind carrying the old
  -- name (the alternate file); nobody wants the term:// ghost.
  for _, b in ipairs(api.nvim_list_bufs()) do
    if b ~= agent.buf and old ~= "" and api.nvim_buf_get_name(b) == old and not api.nvim_buf_is_loaded(b) then
      pcall(api.nvim_buf_delete, b, { force = true })
    end
  end
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
  local out = {}
  for _, a in ipairs(M.list) do
    if a.root == root then
      table.insert(out, a)
    end
  end
  return out
end

--- The agent to show for a project: the one used most recently, preferring
--- a live one over a dead one.
function M.last_used(root)
  local function live(a)
    return a.status ~= "dead" and 1 or 0
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
  if agent.status == "dead" or not agent.job then
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
  if agent.job and agent.status ~= "dead" then
    pcall(vim.fn.jobstop, agent.job)
  end
end

--- Kills the agent and forgets it, wiping its buffer.
function M.remove(agent)
  M.kill(agent)
  for i, a in ipairs(M.list) do
    if a == agent then
      table.remove(M.list, i)
      break
    end
  end
  M.by_key[agent.key] = nil
  if agent.buf and api.nvim_buf_is_valid(agent.buf) then
    pcall(api.nvim_buf_delete, agent.buf, { force = true })
  end
  agent.status = "dead"
  emit(agent, nil)
end

-- Test hook: forget every agent without touching jobs.
function M._reset()
  M.list, M.by_key, seq = {}, {}, 0
end

return M
