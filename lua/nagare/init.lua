-- nagare for Neovim: the editor is the multiplexer.
--
-- A project is a tabpage (tab-local cwd). An agent is a terminal buffer that
-- belongs to a project. Jumping to an agent is showing its buffer beside your
-- code in that project's tab; peeking is showing it in a float over whatever
-- you are editing. The board is one view of every agent in every project —
-- including agents still living in tmux.
local agents = require("nagare.agents")
local config = require("nagare.config")
local projects = require("nagare.projects")
local status = require("nagare.status")
local tmux = require("nagare.tmux")
local util = require("nagare.util")

local api = vim.api

local M = {}

M.agents = agents
M.projects = projects

local status_colors = {
  NagareWaiting = "#db4b4b",
  NagareRunning = "#e0af68",
  NagareIdle = "#00D26A",
  NagareDead = "#565f89",
}

M.status_hl = {
  waiting_input = "NagareWaiting",
  running = "NagareRunning",
  idle = "NagareIdle",
  dead = "NagareDead",
}

M.status_icon = {
  waiting_input = "●",
  running = "◐",
  idle = "○",
  dead = "✕",
}

local function highlights()
  for name, fg in pairs(status_colors) do
    api.nvim_set_hl(0, name, { fg = fg, default = true })
  end
  api.nvim_set_hl(0, "NagareProject", { link = "Title", default = true })
  api.nvim_set_hl(0, "NagareDim", { link = "Comment", default = true })
  api.nvim_set_hl(0, "NagareBorder", { link = "FloatBorder", default = true })
  api.nvim_set_hl(0, "NagareTitle", { link = "Title", default = true })
  api.nvim_set_hl(0, "NagareHeader", { link = "Special", default = true })
  api.nvim_set_hl(0, "NagareTmux", { link = "Comment", default = true })
  for kind, spec in pairs(config.options.agents) do
    api.nvim_set_hl(0, "NagareAgent_" .. kind, { fg = spec.color, bold = true, default = true })
  end
end

function M.sigil(kind)
  local spec = config.options.agents[kind]
  return spec and spec.sigil or (kind or "?"):sub(1, 1):upper()
end

--- Every agent, editor-owned and tmux, as one list.
function M.entries()
  local out = {}
  for _, a in ipairs(agents.list) do
    table.insert(out, a)
  end
  for _, a in ipairs(tmux.list) do
    table.insert(out, a)
  end
  return out
end

--- Counts by status across every agent.
function M.summary()
  local counts = { waiting_input = 0, running = 0, idle = 0, dead = 0 }
  for _, a in ipairs(M.entries()) do
    counts[a.status] = (counts[a.status] or 0) + 1
  end
  return counts
end

--- A statusline component: "● 2 ◐ 1", empty when nothing is live.
function M.statusline()
  local c = M.summary()
  local parts = {}
  for _, s in ipairs({ "waiting_input", "running" }) do
    if c[s] > 0 then
      table.insert(parts, ("%%#%s#%s %d%%*"):format(M.status_hl[s], M.status_icon[s], c[s]))
    end
  end
  return table.concat(parts, " ")
end

--- A lualine component. In LazyVim:
---   opts = function(_, o) table.insert(o.sections.lualine_x, 1, require("nagare").lualine()) end
function M.lualine()
  return {
    function()
      local c = M.summary()
      local parts = {}
      for _, s in ipairs({ "waiting_input", "running" }) do
        if c[s] > 0 then
          table.insert(parts, ("%s %d"):format(M.status_icon[s], c[s]))
        end
      end
      return table.concat(parts, " ")
    end,
    cond = function()
      local c = M.summary()
      return c.waiting_input + c.running > 0
    end,
    color = function()
      local fg = M.summary().waiting_input > 0 and status_colors.NagareWaiting or status_colors.NagareRunning
      return { fg = fg }
    end,
  }
end

--- The most urgent status among a project's agents, or nil.
function M.project_status(root)
  local best
  for _, a in ipairs(M.entries()) do
    if a.root == root and (not best or agents.rank[a.status] < agents.rank[best]) then
      best = a.status
    end
  end
  return best
end

function M.tabline()
  local parts = {}
  local current = api.nvim_get_current_tabpage()
  for i, tab in ipairs(api.nvim_list_tabpages()) do
    local root = projects.tab_root(tab)
    local name
    if root then
      name = util.basename(root)
    else
      local win = api.nvim_tabpage_get_win(tab)
      local file = api.nvim_buf_get_name(api.nvim_win_get_buf(win))
      name = file ~= "" and vim.fn.fnamemodify(file, ":t") or "[No Name]"
    end
    local hl = tab == current and "%#TabLineSel#" or "%#TabLine#"
    local dot = ""
    local st = root and M.project_status(root)
    if st and st ~= "dead" then
      dot = ("%%#%s#%s%s"):format(M.status_hl[st], M.status_icon[st], hl)
    end
    table.insert(parts, ("%s%%%dT %s %s "):format(hl, i, name, dot))
  end
  return table.concat(parts) .. "%#TabLineFill#%T"
end

--- Shows an agent in its project's tab, beside the code: reuses a window
--- that already shows an agent, otherwise opens one per `layout`.
function M.show(agent)
  projects.open(agent.root)
  agents.touch(agent)
  local layout = config.options.layout
  if layout == "float" then
    require("nagare.peek").open(agent)
    return
  end
  local target
  for _, win in ipairs(api.nvim_tabpage_list_wins(0)) do
    if api.nvim_win_get_config(win).relative == "" and agents.from_buf(api.nvim_win_get_buf(win)) then
      target = win
      break
    end
  end
  if target then
    api.nvim_set_current_win(target)
  elseif layout == "split" then
    vim.cmd(("botright %dsplit"):format(math.max(math.floor(vim.o.lines * config.options.size), 5)))
  elseif layout == "vsplit" then
    vim.cmd(("botright %dvsplit"):format(math.max(math.floor(vim.o.columns * config.options.size), 20)))
  end
  api.nvim_win_set_buf(0, agent.buf)
  vim.wo.number, vim.wo.relativenumber, vim.wo.signcolumn = false, false, "no"
  if config.options.insert_on_jump and agent.status ~= "dead" then
    vim.cmd("startinsert")
  end
end

--- Goes to any board entry: an editor agent is shown, a tmux one is
--- switched to, a project is opened.
function M.jump(entry)
  if not entry then
    return
  end
  if entry.source == "tmux" then
    tmux.jump(entry)
  elseif entry.source == "nvim" then
    M.show(entry)
  elseif entry.root then
    projects.open(entry.root, { explicit = true })
  end
end

function M.peek(entry)
  entry = entry or M.most_urgent()
  if not entry then
    vim.notify("nagare: no agents", vim.log.levels.INFO)
    return
  end
  if entry.source == "tmux" then
    tmux.jump(entry)
  else
    require("nagare.peek").open(entry)
  end
end

--- The agent that most wants attention: waiting before running before idle,
--- the current project's before others, then the most recently changed.
function M.most_urgent()
  local root = projects.current_root()
  local best
  for _, a in ipairs(M.entries()) do
    if a.status ~= "dead" then
      local better = not best
        or agents.rank[a.status] < agents.rank[best.status]
        or (agents.rank[a.status] == agents.rank[best.status] and (a.root == root) and best.root ~= root)
        or (agents.rank[a.status] == agents.rank[best.status] and (a.root == root) == (best.root == root)
          and (a.changed or 0) > (best.changed or 0))
      if better then
        best = a
      end
    end
  end
  return best
end

--- Walks the queue of waiting agents: forward from the current one and
--- wrapping, so repeated presses reach every waiting agent once before
--- repeating. Most-urgent-first would ping-pong between the same two.
function M.next_waiting(from)
  local waiting = {}
  for _, a in ipairs(M.entries()) do
    if a.status == "waiting_input" then
      table.insert(waiting, a)
    end
  end
  if #waiting == 0 then
    vim.notify("nagare: nothing is waiting", vim.log.levels.INFO)
    return
  end
  local function order(a)
    return a.project .. "\0" .. a.name .. "\0" .. a.key
  end
  table.sort(waiting, function(a, b)
    return order(a) < order(b)
  end)
  from = from or agents.from_buf(0) or M._last_jump
  local pick = waiting[1]
  if from then
    local here = order(from)
    for _, a in ipairs(waiting) do
      if order(a) > here then
        pick = a
        break
      end
    end
  end
  M._last_jump = pick
  if #waiting > 1 then
    vim.notify(("nagare: %d waiting"):format(#waiting), vim.log.levels.INFO)
  end
  M.jump(pick)
  return pick
end

--- Starts an agent in the current project (or opts.cwd) and shows it.
function M.new(opts)
  opts = opts or {}
  local cwd = opts.cwd or projects.current_root()
  local agent, err = agents.spawn({ kind = opts.kind, cwd = cwd, name = opts.name, args = opts.args })
  if not agent then
    vim.notify("nagare: " .. err, vim.log.levels.ERROR)
    return
  end
  if opts.show ~= false then
    M.show(agent)
  end
  return agent
end

--- Creates a worktree in the current project and starts an agent in it.
function M.worktree(name, kind)
  local function go(n)
    if not n or n == "" then
      return
    end
    local root = projects.current_root()
    local path, err = projects.add_worktree(root, n)
    if not path then
      vim.notify("nagare: " .. err, vim.log.levels.ERROR)
      return
    end
    return M.new({ cwd = path, kind = kind, name = n })
  end
  if name then
    return go(name)
  end
  vim.ui.input({ prompt = "Worktree name: " }, go)
end

--- Shows or hides the current project's agent. Starts one if the project
--- has none, so the key works from a cold start.
function M.toggle()
  local wins = api.nvim_tabpage_list_wins(0)
  local shown = {}
  for _, w in ipairs(wins) do
    if api.nvim_win_get_config(w).relative == "" and agents.from_buf(api.nvim_win_get_buf(w)) then
      table.insert(shown, w)
    end
  end
  -- Hide, unless the agent is all the tab holds: closing it would leave
  -- nothing to return to.
  if #shown > 0 and #wins > #shown then
    vim.cmd("stopinsert")
    for _, w in ipairs(shown) do
      api.nvim_win_close(w, false)
    end
    return
  end
  local agent = agents.last_used(projects.current_root())
  if agent then
    M.show(agent)
  else
    M.new()
  end
end

--- Leaves an agent's terminal: closes the peek float, or returns to the
--- code window beside it.
function M.leave(agent)
  local peek = require("nagare.peek")
  vim.cmd("stopinsert")
  if peek.is_peek() then
    peek.close()
    return
  end
  vim.cmd("wincmd p")
  if agents.from_buf(0) == agent then
    for _, w in ipairs(api.nvim_tabpage_list_wins(0)) do
      if not agents.from_buf(api.nvim_win_get_buf(w)) and api.nvim_win_get_config(w).relative == "" then
        api.nvim_set_current_win(w)
        return
      end
    end
  end
end

--- Sends a reference to lines of the current file to the project's agent,
--- without pressing Enter, so you can finish the sentence there.
function M.send(line1, line2, text)
  local file = api.nvim_buf_get_name(0)
  local root = projects.current_root()
  local agent = agents.last_used(root)
  if not agent or agent.status == "dead" then
    vim.notify("nagare: no live agent in " .. util.basename(root), vim.log.levels.WARN)
    return
  end
  local payload = text or ""
  if file ~= "" and vim.bo.buftype == "" then
    local path = util.normalize(file)
    local rel = path:sub(1, #agent.cwd + 1) == agent.cwd .. "/" and path:sub(#agent.cwd + 2) or path
    local ref = config.options.reference:gsub("{path}", rel):gsub("{from}", line1):gsub("{to}", line2)
    payload = ref .. payload
  end
  agents.send(agent, payload)
  M.show(agent)
end

--- Detaches this UI from a `nagare-go nvim` runtime, leaving the editor and
--- every agent running for the next attach.
function M.detach()
  if vim.fn.exists(":detach") == 2 then
    vim.cmd("detach")
    return
  end
  for _, ui in ipairs(api.nvim_list_uis()) do
    if ui.chan and ui.chan > 0 then
      vim.fn.chanclose(ui.chan)
      return
    end
  end
  vim.notify("nagare: not attached remotely — start the editor with `nagare-go nvim`", vim.log.levels.WARN)
end

function M.board()
  require("nagare.board").open()
end

local subcommands = {
  board = function()
    M.board()
  end,
  new = function(args)
    M.new({ kind = args[1], cwd = args[2] and util.normalize(args[2]) or nil })
  end,
  worktree = function(args)
    M.worktree(args[1], args[2])
  end,
  next = function()
    M.next_waiting()
  end,
  peek = function()
    M.peek()
  end,
  toggle = function()
    M.toggle()
  end,
  project = function(args)
    if args[1] then
      projects.open(util.describe(util.normalize(args[1])).root, { explicit = true })
    else
      projects.pick()
    end
  end,
  send = function(args, cmd)
    M.send(cmd.line1, cmd.line2, #args > 0 and table.concat(args, " ") or nil)
  end,
  rename = function(args)
    local agent = agents.from_buf(0)
    if agent and args[1] then
      agents.rename(agent, args[1])
    else
      vim.notify("nagare: run :Nagare rename <name> from an agent's terminal", vim.log.levels.WARN)
    end
  end,
  detach = function()
    M.detach()
  end,
}

local function complete(arglead, line)
  local words = vim.split(line, "%s+", { trimempty = false })
  if #words <= 2 then
    return vim.tbl_filter(function(s)
      return s:sub(1, #arglead) == arglead
    end, vim.tbl_keys(subcommands))
  end
  if words[2] == "new" and #words == 3 then
    return vim.tbl_filter(function(s)
      return s:sub(1, #arglead) == arglead
    end, vim.tbl_keys(config.options.agents))
  end
  if (words[2] == "new" and #words == 4) or words[2] == "project" then
    return vim.fn.getcompletion(arglead, "dir")
  end
  return {}
end

local function command(cmd)
  local args = vim.split(cmd.args, "%s+", { trimempty = true })
  local name = table.remove(args, 1) or "board"
  local fn = subcommands[name]
  if not fn then
    vim.notify("nagare: unknown subcommand " .. name, vim.log.levels.ERROR)
    return
  end
  fn(args, cmd)
end

local function keymaps()
  local keys = config.options.keys
  if not keys then
    return
  end
  local maps = {
    { keys.board, M.board, "agents board" },
    { keys.toggle, M.toggle, "toggle project agent" },
    { keys.next_waiting, M.next_waiting, "next waiting agent" },
    { keys.peek, M.peek, "peek most urgent agent" },
    { keys.new, function()
      local kinds = vim.tbl_keys(config.options.agents)
      table.sort(kinds)
      vim.ui.select(kinds, { prompt = "Agent" }, function(kind)
        if kind then
          M.new({ kind = kind })
        end
      end)
    end, "new agent in project" },
    { keys.project, function()
      projects.pick()
    end, "open project" },
    { keys.worktree, function()
      M.worktree()
    end, "new worktree + agent" },
  }
  for _, m in ipairs(maps) do
    if m[1] then
      vim.keymap.set("n", m[1], m[2], { desc = "nagare: " .. m[3] })
    end
  end
  -- Label the prefix in which-key (LazyVim ships it); v3 has add(), v2
  -- register().
  local ok, wk = pcall(require, "which-key")
  if ok and keys.prefix then
    if wk.add then
      wk.add({ { keys.prefix, group = "agents", icon = { icon = "󱚣 ", color = "orange" } } })
    elseif wk.register then
      wk.register({ [keys.prefix] = { name = "+agents" } })
    end
  end
  if keys.send then
    vim.keymap.set("x", keys.send, ":Nagare send<CR>", { desc = "nagare: send selection reference", silent = true })
    vim.keymap.set("n", keys.send, "<Cmd>Nagare send<CR>", { desc = "nagare: send line reference" })
  end
end

M._setup_done = false

function M.setup(opts)
  config.setup(opts)
  highlights()

  local group = api.nvim_create_augroup("nagare", { clear = true })
  api.nvim_create_autocmd("ColorScheme", { group = group, callback = highlights })
  api.nvim_create_autocmd({ "BufEnter", "TermEnter" }, {
    group = group,
    callback = function(ev)
      local agent = agents.from_buf(ev.buf)
      if agent then
        agents.touch(agent)
      end
    end,
  })
  api.nvim_create_autocmd("User", {
    group = group,
    pattern = "NagareStatus",
    callback = function()
      vim.cmd("redrawstatus!")
    end,
  })
  api.nvim_create_autocmd("VimLeavePre", {
    group = group,
    callback = function()
      status.stop()
      tmux.stop()
    end,
  })

  api.nvim_create_user_command("Nagare", command, {
    nargs = "*",
    range = true,
    complete = complete,
    desc = "nagare: agents across projects",
  })

  keymaps()
  if config.options.tabline then
    vim.o.showtabline = 2
    vim.o.tabline = "%!v:lua.require'nagare'.tabline()"
  end

  status.start()
  tmux.start()
  M._setup_done = true
  return M
end

return M
