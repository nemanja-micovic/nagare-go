-- nagare for Neovim: the editor is the multiplexer.
--
-- A project is a tabpage (tab-local cwd). An agent is a terminal buffer that
-- belongs to a project. Jumping to an agent is showing its buffer beside your
-- code in that project's tab; peeking is showing it in a float over whatever
-- you are editing. The board is one view of every agent in every project —
-- including agents still living in tmux.
--
-- Nothing here runs at require time. plugin/nagare.lua defines the command
-- and <Plug> maps and schedules M._init(); setup() only merges options.
local config = require("nagare.config")

local api = vim.api

local M = {}

-- Modules load on first use.
local function agents()
  return require("nagare.agents")
end
local function projects()
  return require("nagare.projects")
end
local function util()
  return require("nagare.util")
end

M.status_hl = {
  waiting_input = "NagareWaiting",
  running = "NagareRunning",
  idle = "NagareIdle",
  dead = "NagareDead",
  saved = "NagareSaved",
}

M.review_icon = "◆"

M.status_icon = {
  waiting_input = "●",
  running = "◐",
  idle = "○",
  dead = "✕",
  saved = "◌",
}

-- Status colours follow the colorscheme through the diagnostic groups, so a
-- waiting agent is exactly as red as an error in your theme.
local links = {
  NagareWaiting = "DiagnosticError",
  NagareRunning = "DiagnosticWarn",
  NagareIdle = "DiagnosticOk",
  NagareDead = "NonText",
  NagareSaved = "Comment",
  NagareReview = "DiagnosticInfo",
  NagareProject = "Title",
  NagareDim = "Comment",
  NagareNormal = "NormalFloat",
  NagareBorder = "FloatBorder",
  NagareTitle = "FloatTitle",
  NagareFooter = "FloatFooter",
  NagareKey = "Special",
  NagareSlot = "Number",
  NagareTmux = "Comment",
}

local function highlights()
  for name, target in pairs(links) do
    api.nvim_set_hl(0, name, { link = target, default = true })
  end
  for kind, spec in pairs(config.agents) do
    api.nvim_set_hl(0, "NagareAgent_" .. kind, { fg = spec.color, bold = true, default = true })
  end
end

function M.sigil(kind)
  local spec = config.agents[kind]
  return spec and spec.sigil or (kind or "?"):sub(1, 1):upper()
end

--- Every agent, editor-owned and tmux, as one list.
function M.entries()
  local tmux = require("nagare.tmux")
  tmux.start()
  local out = vim.list_extend({}, agents().list)
  return vim.list_extend(out, tmux.list)
end

--- Counts by status across every agent, plus `review`: agents that settled
--- with changes nobody has looked at yet.
function M.summary()
  local counts = { waiting_input = 0, running = 0, idle = 0, dead = 0, saved = 0, review = 0 }
  local review = package.loaded["nagare.review"]
  for _, a in ipairs(M.entries()) do
    counts[a.status] = (counts[a.status] or 0) + 1
    if review and review.needs_review(a) then
      counts.review = counts.review + 1
    end
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
  if c.review > 0 then
    table.insert(parts, ("%%#NagareReview#%s %d%%*"):format(M.review_icon, c.review))
  end
  return table.concat(parts, " ")
end

--- A lualine component table, for users who build lualine sections by hand.
--- `lualine_x = { "nagare" }` works too (lua/lualine/components/nagare.lua).
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
      if c.review > 0 then
        table.insert(parts, ("%s %d"):format(M.review_icon, c.review))
      end
      return table.concat(parts, " ")
    end,
    cond = function()
      local c = M.summary()
      return c.waiting_input + c.running + c.review > 0
    end,
    color = function()
      return M.summary().waiting_input > 0 and "NagareWaiting" or "NagareRunning"
    end,
  }
end

--- The most urgent status among a project's agents, or nil.
function M.project_status(root)
  local rank = agents().rank
  local best
  for _, a in ipairs(M.entries()) do
    if a.root == root and (not best or rank[a.status] < rank[best]) then
      best = a.status
    end
  end
  return best
end

function M.tabline()
  local parts = {}
  local current = api.nvim_get_current_tabpage()
  for i, tab in ipairs(api.nvim_list_tabpages()) do
    local root = projects().tab_root(tab)
    local name
    if root then
      name = util().basename(root)
    else
      local file = api.nvim_buf_get_name(api.nvim_win_get_buf(api.nvim_tabpage_get_win(tab)))
      name = file ~= "" and vim.fn.fnamemodify(file, ":t") or "[No Name]"
    end
    local hl = tab == current and "%#TabLineSel#" or "%#TabLine#"
    local dot = ""
    local st = root and M.project_status(root)
    if st and st ~= "dead" and st ~= "saved" then
      dot = ("%%#%s#%s%s"):format(M.status_hl[st], M.status_icon[st], hl)
    end
    table.insert(parts, ("%s%%%dT %s %s "):format(hl, i, name, dot))
  end
  return table.concat(parts) .. "%#TabLineFill#%T"
end

--- Puts the cursor into an agent's terminal the way the user left it:
--- normal mode if they were reading its output, else terminal mode.
local function enter(agent)
  if agent.status ~= "dead" and agent.mode ~= "normal" and config.insert_on_jump then
    vim.cmd("startinsert")
  else
    vim.cmd("stopinsert")
  end
end

--- Shows an agent in its project's tab, beside the code: reuses a window
--- that already shows an agent, otherwise opens one per `layout`. A saved
--- agent is resumed first.
function M.show(agent)
  if agent.status == "saved" then
    local ok, err = agents().resume(agent)
    if not ok then
      vim.notify("nagare: " .. err, vim.log.levels.ERROR)
      return
    end
  end
  projects().open(agent.root)
  agents().touch(agent)
  local layout = config.layout
  if layout == "float" then
    require("nagare.peek").open(agent)
    return
  end
  local target
  for _, win in ipairs(api.nvim_tabpage_list_wins(0)) do
    if api.nvim_win_get_config(win).relative == "" and agents().from_buf(api.nvim_win_get_buf(win)) then
      target = win
      break
    end
  end
  if target then
    api.nvim_set_current_win(target)
  elseif layout == "split" then
    vim.cmd(("botright %dsplit"):format(math.max(math.floor(vim.o.lines * config.size), 5)))
    vim.wo.winfixheight = true
  elseif layout == "vsplit" then
    vim.cmd(("botright %dvsplit"):format(math.max(math.floor(vim.o.columns * config.size), 20)))
    vim.wo.winfixwidth = true
  end
  api.nvim_win_set_buf(0, agent.buf)
  vim.wo.number, vim.wo.relativenumber, vim.wo.signcolumn = false, false, "no"
  enter(agent)
end

--- Goes to any board entry: an editor agent is shown, a tmux one is
--- switched to, a project is opened.
function M.jump(entry)
  if not entry then
    return
  end
  if entry.source == "tmux" then
    require("nagare.tmux").jump(entry)
  elseif entry.source == "nvim" then
    M.show(entry)
  elseif entry.root then
    projects().open(entry.root, { explicit = true })
  end
end

function M.peek(entry)
  entry = entry or M.most_urgent()
  if not entry then
    vim.notify("nagare: no agents", vim.log.levels.INFO)
    return
  end
  if entry.source == "tmux" then
    require("nagare.tmux").jump(entry)
    return
  end
  if entry.status == "saved" then
    local ok, err = agents().resume(entry)
    if not ok then
      vim.notify("nagare: " .. err, vim.log.levels.ERROR)
      return
    end
  end
  require("nagare.peek").open(entry)
end

--- The agent that most wants attention: waiting before running before idle,
--- the current project's before others, then the most recently changed.
function M.most_urgent()
  local rank = agents().rank
  local root = projects().current_root()
  local function key(a)
    return { rank[a.status], a.root == root and 0 or 1, -(a.changed or 0) }
  end
  local best, best_key
  for _, a in ipairs(M.entries()) do
    if a.status ~= "dead" and a.status ~= "saved" then
      local k = key(a)
      if not best or k[1] < best_key[1] or (k[1] == best_key[1] and (k[2] < best_key[2]
        or (k[2] == best_key[2] and k[3] < best_key[3]))) then
        best, best_key = a, k
      end
    end
  end
  return best
end

--- Walks the queue of waiting agents: forward from the current one and
--- wrapping, so repeated presses reach every waiting agent once before
--- repeating. Most-urgent-first would ping-pong between the same two.
function M.next_waiting(from)
  local waiting = vim.tbl_filter(function(a)
    return a.status == "waiting_input"
  end, M.entries())
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
  from = from or agents().from_buf(0) or M._last_jump
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

--- Walks the review queue like next_waiting walks the waiting one: each
--- press opens the next agent whose changes you have not reviewed.
function M.next_review()
  local q = require("nagare.review").queue()
  if #q == 0 then
    vim.notify("nagare: nothing to review", vim.log.levels.INFO)
    return
  end
  if #q > 1 then
    vim.notify(("nagare: %d to review"):format(#q), vim.log.levels.INFO)
  end
  return require("nagare.review").open(q[1])
end

--- Sends text to every live agent in a project (default: the current one),
--- pressing Enter — "stop and commit", "rebase on main", "run the tests".
function M.broadcast(text, root)
  root = root or projects().current_root()
  local n = 0
  for _, a in ipairs(agents().list) do
    if a.root == root and agents().alive(a) and agents().send(a, text .. "\r") then
      n = n + 1
    end
  end
  vim.notify(("nagare: sent to %d agent%s in %s"):format(n, n == 1 and "" or "s", util().basename(root)),
    vim.log.levels.INFO)
  return n
end

--- Best-of-N: the same task in N fresh worktrees, cycling through the given
--- agents, so you can compare the attempts and merge the winner.
function M.fanout(n, kinds, task)
  if not task or task == "" then
    vim.notify("nagare: fanout needs a task", vim.log.levels.WARN)
    return {}
  end
  kinds = (kinds and #kinds > 0) and kinds or { config.default_agent }
  local base = M.slug(task)
  local out = {}
  for i = 1, n do
    local kind = kinds[((i - 1) % #kinds) + 1]
    local a = M.worktree(("%s-%d"):format(base, i), kind, task)
    if a then
      a.group = base
      table.insert(out, a)
    end
  end
  return out
end

--- The editor's agents in slot order: creation order, which never changes
--- under you the way urgency order does, so a slot is muscle memory.
function M.slots()
  return agents().list
end

function M.slot(n)
  local agent = M.slots()[n]
  if not agent then
    vim.notify(("nagare: no agent in slot %d"):format(n), vim.log.levels.INFO)
    return
  end
  M.jump(agent)
  return agent
end

--- Starts an agent in the current project (or opts.cwd) and shows it.
function M.new(opts)
  opts = opts or {}
  local cwd = opts.cwd or projects().current_root()
  local agent, err = agents().spawn({ kind = opts.kind, cwd = cwd, name = opts.name, args = opts.args, prompt = opts.prompt })
  if not agent then
    vim.notify("nagare: " .. err, vim.log.levels.ERROR)
    return
  end
  if opts.show ~= false then
    M.show(agent)
  end
  return agent
end

--- Asks which agent to start, then starts it in the project at root.
function M.choose(root)
  local kinds = vim.tbl_keys(config.agents)
  table.sort(kinds)
  vim.ui.select(kinds, {
    prompt = "Agent",
    kind = "nagare_agent",
    format_item = function(kind)
      local installed = vim.fn.executable(config.agents[kind].cmd[1]) == 1
      return ("%s  %s%s"):format(M.sigil(kind), kind, installed and "" or "  (not installed)")
    end,
  }, function(kind)
    if not kind then
      return
    end
    vim.ui.input({ prompt = "Task (optional, Enter to just start): " }, function(task)
      if task == nil then
        return -- cancelled
      end
      M.new({ kind = kind, cwd = root, prompt = task ~= "" and task or nil })
    end)
  end)
end

--- A worktree name from a task: "Fix the login redirect" -> "fix-login-redirect".
function M.slug(text)
  local stop = { a = true, an = true, the = true, to = true, of = true, ["and"] = true, ["in"] = true, on = true, ["for"] = true }
  local words = {}
  for w in text:lower():gmatch("[%w]+") do
    if not stop[w] and #words < 5 then
      table.insert(words, w)
    end
  end
  local s = table.concat(words, "-")
  return s ~= "" and s or ("task-" .. os.time())
end

--- Creates a worktree in the current project and starts an agent in it,
--- optionally on a task. With only a task, the worktree is named after it.
function M.worktree(name, kind, prompt)
  local function go(n, task)
    n = (n and n ~= "") and n or (task and task ~= "" and M.slug(task))
    if not n then
      return
    end
    local path, err = projects().add_worktree(projects().current_root(), n)
    if not path then
      vim.notify("nagare: " .. err, vim.log.levels.ERROR)
      return
    end
    return M.new({ cwd = path, kind = kind, name = n, prompt = task })
  end
  if name or prompt then
    return go(name, prompt)
  end
  vim.ui.input({ prompt = "Task for the new worktree agent (or just a name): " }, function(text)
    if not text or text == "" then
      return
    end
    -- One word is a name; a sentence is a task the worktree is named after.
    if text:find("%s") then
      go(nil, text)
    else
      go(text, nil)
    end
  end)
end

--- Shows or hides the current project's agent. Starts one if the project
--- has none, so the key works from a cold start.
function M.toggle()
  local wins = api.nvim_tabpage_list_wins(0)
  local shown = vim.tbl_filter(function(w)
    return api.nvim_win_get_config(w).relative == "" and agents().from_buf(api.nvim_win_get_buf(w)) ~= nil
  end, wins)
  -- Hide, unless the agent is all the tab holds: closing it would leave
  -- nothing to return to.
  if #shown > 0 and #wins > #shown then
    vim.cmd("stopinsert")
    for _, w in ipairs(shown) do
      api.nvim_win_close(w, false)
    end
    return
  end
  local agent = agents().last_used(projects().current_root())
  if agent then
    M.show(agent)
  else
    M.new()
  end
end

--- Leaves an agent's terminal: closes the peek float, or returns to the
--- code window beside it. Leaving this way is not "reading its output", so
--- the next jump comes back in terminal mode.
function M.leave(agent)
  local peek = require("nagare.peek")
  agent.leaving = true
  agent.mode = "terminal"
  vim.cmd("stopinsert")
  vim.schedule(function()
    agent.leaving = nil
  end)
  if peek.is_peek() then
    peek.close()
    return
  end
  vim.cmd("wincmd p")
  if agents().from_buf(0) == agent then
    for _, w in ipairs(api.nvim_tabpage_list_wins(0)) do
      if not agents().from_buf(api.nvim_win_get_buf(w)) and api.nvim_win_get_config(w).relative == "" then
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
  local root = projects().current_root()
  local agent = agents().last_used(root)
  if not agent or not agents().alive(agent) then
    vim.notify("nagare: no live agent in " .. util().basename(root), vim.log.levels.WARN)
    return
  end
  local payload = text or ""
  if file ~= "" and vim.bo.buftype == "" then
    local path = util().normalize(file)
    local rel = path:sub(1, #agent.cwd + 1) == agent.cwd .. "/" and path:sub(#agent.cwd + 2) or path
    local ref = config.reference:gsub("{path}", rel):gsub("{from}", line1):gsub("{to}", line2)
    payload = ref .. payload
  end
  agents().send(agent, payload)
  M.show(agent)
end

--- True inside a runtime started by `nagare-go nvim`. The environment says
--- so: since 0.10 the built-in TUI is a remote UI too, so a UI channel
--- cannot tell a persistent runtime from a plain nvim.
function M.persistent()
  return (vim.env.NAGARE_RUNTIME or "") ~= ""
end

--- Detaches this UI from a `nagare-go nvim` runtime, leaving the editor and
--- every agent running for the next attach.
function M.detach()
  if not M.persistent() then
    vim.notify("nagare: not a persistent runtime — start the editor with `nagare-go nvim`", vim.log.levels.WARN)
    return
  end
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
end

function M.board()
  require("nagare.board").open()
end

--- Reviews an agent's changes: the agent under the cursor, else this
--- project's agent with the most recent activity that changed anything.
function M.review(agent)
  agent = agent or agents().from_buf(0)
  if not agent then
    local root = projects().current_root()
    local review = require("nagare.review")
    local best
    for _, a in ipairs(agents().list) do
      if a.root == root and a.status ~= "saved" then
        local s = review.summary(a)
        if s and s.files > 0 and (not best or (a.changed or 0) > (best.changed or 0)) then
          best = a
        end
      end
    end
    agent = best
  end
  if not agent then
    vim.notify("nagare: no agent in this project has changed anything", vim.log.levels.INFO)
    return
  end
  return require("nagare.review").open(agent)
end

--- A fuzzy agent picker: snacks' picker when available (with a live
--- preview of each agent's screen), else vim.ui.select.
function M.pick()
  local snacks = rawget(_G, "Snacks")
  if snacks and snacks.picker and snacks.picker.sources and snacks.picker.sources.nagare then
    return snacks.picker.nagare()
  end
  vim.ui.select(M.entries(), {
    prompt = "Agent",
    kind = "nagare_agent",
    format_item = function(a)
      return ("%s %s  %s/%s  %s"):format(M.status_icon[a.status], M.sigil(a.kind), a.project, a.name, a.status)
    end,
  }, M.jump)
end

-- Keymaps -------------------------------------------------------------------

M.descriptions = {
  ["<Plug>(nagare-board)"] = "Agents board",
  ["<Plug>(nagare-toggle)"] = "Toggle project agent",
  ["<Plug>(nagare-next)"] = "Next waiting agent",
  ["<Plug>(nagare-peek)"] = "Peek most urgent agent",
  ["<Plug>(nagare-new)"] = "New agent",
  ["<Plug>(nagare-worktree)"] = "New worktree + agent",
  ["<Plug>(nagare-project)"] = "Open project",
  ["<Plug>(nagare-pick)"] = "Find agent",
  ["<Plug>(nagare-send)"] = "Send reference to agent",
  ["<Plug>(nagare-review)"] = "Review agent changes",
  ["<Plug>(nagare-memory)"] = "Agent memory",
  ["<Plug>(nagare-task)"] = "New task (buffer)",
  ["<Plug>(nagare-next-review)"] = "Next agent to review",
  ["<Plug>(nagare-comment)"] = "Review comment",
}

local function map(lhs, plug, modes)
  if not lhs then
    return
  end
  for _, mode in ipairs(modes or { "n" }) do
    -- A <Plug> map the user bound themselves is theirs; leave it. Our own
    -- earlier binding (a repeated setup) is replaced.
    local ours = vim.fn.maparg(lhs, mode) == plug
    if ours or vim.fn.hasmapto(plug, mode) == 0 then
      vim.keymap.set(mode, lhs, plug, { remap = true, desc = M.descriptions[plug] })
    end
  end
end

local function keymaps()
  local keys = config.keys
  if type(keys) ~= "table" then
    return
  end
  map(keys.board, "<Plug>(nagare-board)")
  map(keys.toggle, "<Plug>(nagare-toggle)")
  map(keys.next_waiting, "<Plug>(nagare-next)")
  map(keys.peek, "<Plug>(nagare-peek)")
  map(keys.new, "<Plug>(nagare-new)")
  map(keys.worktree, "<Plug>(nagare-worktree)")
  map(keys.project, "<Plug>(nagare-project)")
  map(keys.pick, "<Plug>(nagare-pick)")
  map(keys.send, "<Plug>(nagare-send)", { "n", "x" })
  map(keys.review, "<Plug>(nagare-review)")
  map(keys.memory, "<Plug>(nagare-memory)")
  map(keys.task, "<Plug>(nagare-task)")
  map(keys.next_review, "<Plug>(nagare-next-review)")
  map(keys.comment, "<Plug>(nagare-comment)", { "n", "x" })
  if keys.slots and keys.prefix then
    for n = 1, 9 do
      local plug = ("<Plug>(nagare-slot-%d)"):format(n)
      M.descriptions[plug] = "Agent in slot " .. n
      map(keys.prefix .. n, plug)
    end
  end
  -- which-key (in LazyVim) picks up each desc; the prefix needs a label.
  local ok, wk = pcall(require, "which-key")
  if ok and keys.prefix and wk.add then
    wk.add({ { keys.prefix, group = "agents", icon = { icon = "󱚣 ", color = "orange" } } })
  end
end

-- Lifecycle -----------------------------------------------------------------

--- Merges options. Optional: the plugin runs on defaults (or vim.g.nagare)
--- without it. Safe to call again; keymaps follow the new options.
function M.setup(opts)
  config.setup(opts)
  if M._initialized then
    highlights()
    keymaps()
  end
  return M
end

--- One-time initialisation, scheduled by plugin/nagare.lua. Cheap: no
--- watcher and no tmux polling start here — they start when first needed.
function M._init()
  if M._initialized then
    return
  end
  M._initialized = true
  highlights()

  local group = api.nvim_create_augroup("nagare", { clear = true })
  api.nvim_create_autocmd("ColorScheme", { group = group, callback = highlights })
  api.nvim_create_autocmd({ "BufEnter", "TermEnter" }, {
    group = group,
    callback = function(ev)
      local agent = agents().from_buf(ev.buf)
      if agent then
        agents().touch(agent)
      end
    end,
  })
  api.nvim_create_autocmd("ModeChanged", {
    group = group,
    pattern = { "t:nt", "nt:t" },
    callback = function(ev)
      agents().track_mode(ev)
    end,
  })
  require("nagare.trust").setup_autocmd(group)
  api.nvim_create_autocmd("VimResized", {
    group = group,
    callback = function()
      local board = package.loaded["nagare.board"]
      if board and board.is_open() then
        board.render()
      end
    end,
  })
  api.nvim_create_autocmd("VimLeavePre", {
    group = group,
    callback = function()
      agents().shutdown()
      pcall(function()
        require("nagare.memory").stop()
        require("nagare.ci").stop()
      end)
      require("nagare.status").stop()
      require("nagare.tmux").stop()
    end,
  })

  keymaps()
  if config.tabline then
    vim.o.showtabline = 2
    vim.o.tabline = "%!v:lua.require'nagare'.tabline()"
  end
  if config.restore then
    agents().restore()
  end
  pcall(function()
    require("nagare.snacks").register()
  end)
end

return M
