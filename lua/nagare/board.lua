-- The board: every project and every agent in one floating window, most
-- urgent first. It is the picker's list view rebuilt as a Neovim buffer —
-- projects are group headers, agents their children, and a project takes the
-- position of its most urgent agent so a waiting worktree lifts its repo.
local agents = require("nagare.agents")
local config = require("nagare.config")
local projects = require("nagare.projects")
local util = require("nagare.util")

local api = vim.api

local M = {}

local ns = api.nvim_create_namespace("nagare_board")
local state -- { buf, win, rows, timer }

local status_word = {
  waiting_input = "waiting",
  running = "working",
  idle = "idle",
  dead = "exited",
}

--- Groups entries under their projects. Projects with an open tab are
--- listed even with no agents, so the board doubles as a tab switcher.
function M.groups(entries)
  local by_root, groups = {}, {}
  local function group(root)
    local g = by_root[root]
    if not g then
      g = { root = root, name = util.basename(root), entries = {}, rank = 5 }
      by_root[root] = g
      table.insert(groups, g)
    end
    return g
  end
  for _, tab in ipairs(api.nvim_list_tabpages()) do
    local root = projects.tab_root(tab)
    if root then
      group(root).tab = api.nvim_tabpage_get_number(tab)
    end
  end
  for _, e in ipairs(entries) do
    local g = group(e.root)
    table.insert(g.entries, e)
    g.rank = math.min(g.rank, agents.rank[e.status] or 4)
  end
  local function by_name(a, b)
    return a.name < b.name
  end
  for _, g in ipairs(groups) do
    table.sort(g.entries, function(a, b)
      local ra, rb = agents.rank[a.status] or 4, agents.rank[b.status] or 4
      if ra ~= rb then
        return ra < rb
      end
      if a.name ~= b.name then
        return a.name < b.name
      end
      return a.key < b.key
    end)
  end
  table.sort(groups, function(a, b)
    if a.rank ~= b.rank then
      return a.rank < b.rank
    end
    return by_name(a, b)
  end)
  return groups
end

-- In drop order: on a narrow board the tail goes first. "q close" is not in
-- the list — it is reserved, because trimming from the end would take the
-- way out with it.
local hint_list = {
  "⏎ jump", "p peek", "a new", "y approve", "n next", "w worktree",
  "x kill", "o project", "A agent…", "r rename",
}

function M.hints(width)
  local out = " "
  local exit = "q close"
  for _, h in ipairs(hint_list) do
    local candidate = out .. h .. "  "
    if vim.fn.strdisplaywidth(candidate .. exit) > width then
      break
    end
    out = candidate
  end
  return out .. exit
end

-- A line under construction: text plus highlight spans in byte columns.
local function line()
  local l = { text = "", hls = {} }
  function l:add(s, hl)
    if hl and s ~= "" then
      table.insert(self.hls, { hl, #self.text, #self.text + #s })
    end
    self.text = self.text .. s
    return self
  end
  return l
end

--- Lays the board out at `width` cells. Returns lines (each with text and
--- highlights) and rows, which map a line number to the entry or project
--- on it. Pure apart from reading editor state, so tests call it directly.
function M.build(width)
  local nagare = require("nagare")
  local groups = M.groups(nagare.entries())
  local lines, rows = {}, {}

  local counts = nagare.summary()
  local head = line():add(" nagare ", "NagareTitle")
  for _, s in ipairs({ "waiting_input", "running", "idle" }) do
    if counts[s] > 0 then
      head:add("  "):add(("%s %d %s"):format(nagare.status_icon[s], counts[s], status_word[s]), nagare.status_hl[s])
    end
  end
  if #groups == 0 then
    head:add("  no agents yet — a starts one here, o opens a project", "NagareDim")
  end
  table.insert(lines, head)
  table.insert(lines, line())

  -- Columns: 4 indent, icon, sigil, name, status, age, message, branch.
  local name_w = math.max(math.min(24, math.floor(width * 0.26)), 10)
  local branch_w = math.max(math.min(22, math.floor(width * 0.2)), 8)
  local msg_w = width - (4 + 2 + 2 + name_w + 1 + 8 + 5 + branch_w + 1)

  for gi, g in ipairs(groups) do
    local st = g.rank <= 4 and g.entries[1] and g.entries[1].status or nil
    local l = line():add("  ")
    if st then
      l:add(nagare.status_icon[st], nagare.status_hl[st])
    else
      l:add(" ")
    end
    l:add(" "):add(g.name, "NagareProject")
    local right = {}
    if g.tab then
      table.insert(right, "tab " .. g.tab)
    end
    local repo = util.describe(g.root)
    if repo.branch then
      table.insert(right, repo.branch)
    end
    g.branch = repo.branch
    local r = table.concat(right, " · ")
    local pad = width - vim.fn.strdisplaywidth(l.text) - vim.fn.strdisplaywidth(r) - 1
    l:add(string.rep(" ", math.max(pad, 1))):add(r, "NagareDim")
    table.insert(lines, l)
    rows[#lines] = { kind = "project", root = g.root, key = "project:" .. g.root }

    if #g.entries == 0 then
      table.insert(lines, line():add("      no agents", "NagareDim"))
      rows[#lines] = { kind = "project", root = g.root, key = "project:" .. g.root .. ":empty" }
    end
    for _, e in ipairs(g.entries) do
      local a = line():add("    ")
      a:add(nagare.status_icon[e.status] or "?", nagare.status_hl[e.status])
      a:add(" "):add(nagare.sigil(e.kind), "NagareAgent_" .. (e.kind or ""))
      a:add(" "):add(util.fit(e.name, name_w), e.source == "tmux" and "NagareTmux" or nil)
      a:add(" "):add(util.fit(status_word[e.status] or e.status, 8), nagare.status_hl[e.status])
      a:add(util.fit(e.changed and util.ago(e.changed) or "", 5), "NagareDim")
      if msg_w > 8 then
        a:add(util.fit(util.oneline(e.last_message, msg_w), msg_w), "NagareDim")
      end
      -- The header already names the main checkout's branch; repeating it on
      -- every child is noise. A branch that differs is worth showing.
      local tag = e.worktree and ("⎇ " .. e.worktree)
        or (e.branch ~= g.branch and e.branch)
        or ""
      if e.source == "tmux" then
        tag = "tmux" .. (tag ~= "" and (" · " .. tag) or "")
      end
      a:add(" "):add(util.fit(tag, branch_w), "NagareDim")
      table.insert(lines, a)
      rows[#lines] = { kind = "agent", entry = e, root = e.root, key = e.key }
    end
    if gi < #groups then
      table.insert(lines, line())
    end
  end

  table.insert(lines, line())
  table.insert(lines, line():add(M.hints(width), "NagareDim"))
  return lines, rows
end

local function item()
  if not state then
    return nil
  end
  local lnum = api.nvim_win_get_cursor(state.win)[1]
  return state.rows[lnum]
end

local function selectable(lnum)
  return state.rows[lnum] ~= nil
end

local function move(delta)
  local lnum = api.nvim_win_get_cursor(state.win)[1]
  local n = api.nvim_buf_line_count(state.buf)
  local i = lnum + delta
  while i >= 1 and i <= n do
    if selectable(i) then
      api.nvim_win_set_cursor(state.win, { i, 0 })
      return
    end
    i = i + delta
  end
end

local function render(focus_key)
  if not (state and api.nvim_win_is_valid(state.win)) then
    return
  end
  local keep = focus_key or (item() or {}).key
  local width = api.nvim_win_get_width(state.win)
  local lines, rows = M.build(width)
  state.rows = rows

  local text = {}
  for i, l in ipairs(lines) do
    text[i] = l.text
  end
  vim.bo[state.buf].modifiable = true
  api.nvim_buf_set_lines(state.buf, 0, -1, false, text)
  vim.bo[state.buf].modifiable = false
  api.nvim_buf_clear_namespace(state.buf, ns, 0, -1)
  for i, l in ipairs(lines) do
    for _, h in ipairs(l.hls) do
      api.nvim_buf_add_highlight(state.buf, ns, h[1], i - 1, h[2], h[3])
    end
  end

  local height = math.min(#lines, vim.o.lines - 6)
  api.nvim_win_set_config(state.win, {
    relative = "editor",
    width = width,
    height = math.max(height, 3),
    row = math.floor((vim.o.lines - height) / 2) - 1,
    col = math.floor((vim.o.columns - width) / 2),
  })

  -- Keep the cursor on the same agent across re-sorts; otherwise land on
  -- whatever needs you most.
  local target
  for lnum, r in pairs(rows) do
    if keep and r.key == keep then
      target = lnum
    end
  end
  if not target then
    for lnum = 1, #lines do
      local r = rows[lnum]
      if r and r.kind == "agent" then
        target = lnum
        break
      end
    end
  end
  target = target or next(rows) and math.min(unpack(vim.tbl_keys(rows)))
  if target then
    api.nvim_win_set_cursor(state.win, { target, 0 })
  end
end

M.render = render

function M.close()
  if not state then
    return
  end
  if state.timer then
    state.timer:stop()
    state.timer:close()
  end
  if api.nvim_win_is_valid(state.win) then
    pcall(api.nvim_win_close, state.win, true)
  end
  if api.nvim_buf_is_valid(state.buf) then
    pcall(api.nvim_buf_delete, state.buf, { force = true })
  end
  pcall(api.nvim_del_augroup_by_name, "nagare_board")
  state = nil
end

function M.is_open()
  return state ~= nil
end

-- Runs fn after the board is gone, so it acts on the editor behind it.
local function after_close(fn)
  return function()
    local it = item()
    if not it then
      return
    end
    M.close()
    fn(it)
  end
end

local function actions()
  local nagare = require("nagare")
  local function agent_of(it)
    return it.kind == "agent" and it.entry or nil
  end
  return {
    ["<CR>"] = after_close(function(it)
      nagare.jump(it.entry or { root = it.root })
    end),
    p = after_close(function(it)
      if agent_of(it) then
        nagare.peek(it.entry)
      else
        projects.open(it.root, { explicit = true })
      end
    end),
    a = after_close(function(it)
      nagare.new({ cwd = it.root })
    end),
    A = after_close(function(it)
      local kinds = vim.tbl_keys(config.options.agents)
      table.sort(kinds)
      vim.ui.select(kinds, { prompt = "Agent" }, function(kind)
        if kind then
          nagare.new({ cwd = it.root, kind = kind })
        end
      end)
    end),
    w = after_close(function(it)
      projects.open(it.root)
      nagare.worktree()
    end),
    y = function()
      local e = agent_of(item() or {})
      if e then
        local ok = e.source == "tmux" and require("nagare.tmux").approve(e) or agents.approve(e)
        if not ok then
          vim.notify("nagare: " .. e.name .. " is not waiting", vim.log.levels.INFO)
        end
      end
    end,
    Y = function()
      local e = agent_of(item() or {})
      if e then
        local _ = e.source == "tmux" and require("nagare.tmux").approve(e, true) or agents.approve(e, true)
      end
    end,
    x = function()
      local e = agent_of(item() or {})
      if not e then
        return
      end
      if e.source == "tmux" then
        vim.notify("nagare: tmux agents are managed from tmux", vim.log.levels.INFO)
        return
      end
      if e.status == "dead" then
        agents.remove(e)
        render()
        return
      end
      vim.ui.select({ "yes", "no" }, { prompt = "Kill " .. agents.label(e) .. "?" }, function(choice)
        if choice == "yes" then
          agents.kill(e)
        end
      end)
    end,
    r = function()
      local e = agent_of(item() or {})
      if not e or e.source ~= "nvim" then
        return
      end
      vim.ui.input({ prompt = "Name: ", default = e.name }, function(name)
        if name and name ~= "" then
          agents.rename(e, name)
          render(e.key)
        end
      end)
    end,
    n = function()
      M.close()
      nagare.next_waiting()
    end,
    o = function()
      M.close()
      projects.pick()
    end,
    R = function()
      util.forget_repos()
      require("nagare.tmux").refresh()
      render()
    end,
    q = M.close,
    ["<Esc>"] = M.close,
    j = function()
      move(1)
    end,
    k = function()
      move(-1)
    end,
    ["<Down>"] = function()
      move(1)
    end,
    ["<Up>"] = function()
      move(-1)
    end,
  }
end

function M.open()
  if state then
    render()
    return
  end
  local buf = api.nvim_create_buf(false, true)
  vim.bo[buf].bufhidden = "wipe"
  vim.bo[buf].filetype = "nagare"
  local width = math.max(math.min(vim.o.columns - 8, 118), 40)
  local cfg = {
    relative = "editor",
    width = width,
    height = 3,
    row = 2,
    col = math.floor((vim.o.columns - width) / 2),
    style = "minimal",
    border = "rounded",
    zindex = 50,
  }
  if vim.fn.has("nvim-0.9") == 1 then
    cfg.title = " agents "
    cfg.title_pos = "center"
  end
  local win = api.nvim_open_win(buf, true, cfg)
  vim.wo[win].cursorline = true
  vim.wo[win].wrap = false
  vim.wo[win].winhighlight = "NormalFloat:Normal,FloatBorder:NagareBorder,FloatTitle:NagareTitle"
  state = { buf = buf, win = win, rows = {} }

  for lhs, fn in pairs(actions()) do
    vim.keymap.set("n", lhs, fn, { buffer = buf, nowait = true, silent = true })
  end

  local group = api.nvim_create_augroup("nagare_board", { clear = true })
  api.nvim_create_autocmd("User", {
    group = group,
    pattern = "NagareStatus",
    callback = function()
      render()
    end,
  })
  api.nvim_create_autocmd("WinLeave", {
    group = group,
    buffer = buf,
    callback = function()
      vim.schedule(function()
        if state and state.win == win and api.nvim_get_current_win() ~= win then
          M.close()
        end
      end)
    end,
  })
  -- The age column goes stale while the board sits open.
  state.timer = util.uv.new_timer()
  state.timer:start(5000, 5000, vim.schedule_wrap(function()
    render()
  end))

  render()
end

return M
