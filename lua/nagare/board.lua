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
  saved = "saved",
}

--- Groups entries under their projects. Projects with an open tab are
--- listed even with no agents, so the board doubles as a tab switcher.
function M.groups(entries)
  local by_root, groups = {}, {}
  local function group(root)
    local g = by_root[root]
    if not g then
      g = { root = root, name = util.basename(root), entries = {}, rank = 9 }
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
  local review = require("nagare.review")
  -- Unreviewed changes rank just below waiting: after "answer me" comes
  -- "look at what I did".
  local function rank(e)
    if review.needs_review(e) then
      return 1.5
    end
    return agents.rank[e.status] or 8
  end
  for _, e in ipairs(entries) do
    local g = group(e.root)
    table.insert(g.entries, e)
    g.rank = math.min(g.rank, rank(e))
  end
  for _, g in ipairs(groups) do
    table.sort(g.entries, function(a, b)
      local ra, rb = rank(a), rank(b)
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
    return a.name < b.name
  end)
  return groups
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

-- Actions -------------------------------------------------------------------

local function item()
  if not (state and api.nvim_win_is_valid(state.win)) then
    return nil
  end
  return state.rows[api.nvim_win_get_cursor(state.win)[1]]
end

local function selected_agent()
  local it = item()
  return it and it.kind == "agent" and it.entry or nil
end

local function move(delta)
  local lnum = api.nvim_win_get_cursor(state.win)[1]
  local n = api.nvim_buf_line_count(state.buf)
  local i = lnum + delta
  while i >= 1 and i <= n do
    if state.rows[i] then
      api.nvim_win_set_cursor(state.win, { i, 0 })
      return
    end
    i = i + delta
  end
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

local function approve(always)
  local e = selected_agent()
  if not e then
    return
  end
  local ok = e.source == "tmux" and require("nagare.tmux").approve(e, always) or agents.approve(e, always)
  if not ok then
    vim.notify("nagare: " .. e.name .. " is not waiting", vim.log.levels.INFO)
  end
end

-- One table drives the buffer maps, the hint line and the g? help, so the
-- three cannot drift apart. `hint` is the short label; entries without one
-- are listed only in the help. Order is the hint line's drop order.
M.keys = {
  { "<CR>", "jump", "Jump to agent / open project", after_close(function(it)
    require("nagare").jump(it.entry or { root = it.root })
  end) },
  { "p", "peek", "Peek agent in a float", after_close(function(it)
    if it.kind == "agent" then
      require("nagare").peek(it.entry)
    else
      projects.open(it.root, { explicit = true })
    end
  end) },
  { "a", "new", "New agent in this project", after_close(function(it)
    require("nagare").new({ cwd = it.root })
  end) },
  { "y", "approve", "Approve a waiting agent", function()
    approve(false)
  end },
  { "d", "review", "Review what the agent changed", after_close(function(it)
    if it.kind == "agent" and it.entry.source == "nvim" then
      require("nagare.review").open(it.entry)
    end
  end) },
  { "n", "next", "Jump to the next waiting agent", function()
    M.close()
    require("nagare").next_waiting()
  end },
  { "w", "worktree", "New worktree + agent", after_close(function(it)
    projects.open(it.root)
    require("nagare").worktree()
  end) },
  { "c", "resume", "Resume a dead or saved agent", function()
    local e = selected_agent()
    if e and e.source == "nvim" then
      local ok, err = agents.resume(e)
      if not ok then
        vim.notify("nagare: " .. err, vim.log.levels.ERROR)
      end
      M.render(e.key)
    end
  end },
  { "x", "kill", "Kill agent; on a dead or saved one, forget it", function()
    local e = selected_agent()
    if not e then
      return
    end
    if e.source == "tmux" then
      vim.notify("nagare: tmux agents are managed from tmux", vim.log.levels.INFO)
    elseif not agents.alive(e) then
      agents.remove(e)
      M.render()
    else
      vim.ui.select({ "Kill", "Cancel" }, { prompt = "Kill " .. agents.label(e) .. "?" }, function(choice)
        if choice == "Kill" then
          agents.kill(e)
        end
      end)
    end
  end },
  { "o", "project", "Open another project", function()
    M.close()
    projects.pick()
  end },
  { "A", "agent…", "New agent, choosing which", after_close(function(it)
    require("nagare").choose(it.root)
  end) },
  { "r", "rename", "Rename agent", function()
    local e = selected_agent()
    if not e or e.source ~= "nvim" then
      return
    end
    vim.ui.input({ prompt = "Name: ", default = e.name }, function(name)
      if name and name ~= "" then
        agents.rename(e, name)
        M.render(e.key)
      end
    end)
  end },
  { "Y", nil, "Approve always (the option below Yes)", function()
    approve(true)
  end },
  { "R", nil, "Refresh", function()
    util.forget_repos()
    require("nagare.tmux").refresh()
    M.render()
  end },
  { "1-9", nil, "Jump to the agent in that slot", nil },
  { "g?", nil, "This help", function()
    M.help()
  end },
  { "q", nil, "Close", function()
    M.close()
  end },
}

--- The hint line, trimmed in drop order to fit. "q close" is reserved:
--- trimming from the end would take the way out with it.
function M.hints(width)
  local out = " "
  local tail = "g? help  q close"
  if vim.fn.strdisplaywidth(" " .. tail) > width then
    tail = "q close"
  end
  for _, k in ipairs(M.keys) do
    if k[2] then
      local candidate = out .. (k[1] == "<CR>" and "⏎" or k[1]) .. " " .. k[2] .. "  "
      if vim.fn.strdisplaywidth(candidate .. tail) > width then
        break
      end
      out = candidate
    end
  end
  return out .. tail
end

-- Layout --------------------------------------------------------------------

--- Lays the board out at `width` cells. Returns lines (each with text and
--- highlights) and rows, which map a line number to the entry or project
--- on it. Pure apart from reading editor state, so tests call it directly.
function M.build(width)
  local nagare = require("nagare")
  local groups = M.groups(nagare.entries())
  local slots = {}
  for i, a in ipairs(nagare.slots()) do
    if i <= 9 then
      slots[a] = i
    end
  end
  local lines, rows = {}, {}

  local counts = nagare.summary()
  local head = line():add(" nagare ", "NagareTitle")
  for _, s in ipairs({ "waiting_input", "review", "running", "idle", "saved" }) do
    if counts[s] > 0 then
      local icon = s == "review" and nagare.review_icon or nagare.status_icon[s]
      local word = s == "review" and "to review" or status_word[s]
      head:add("  "):add(("%s %d %s"):format(icon, counts[s], word), s == "review" and "NagareReview" or nagare.status_hl[s])
    end
  end
  if #groups == 0 then
    head:add("  no agents yet — a starts one here, o opens a project", "NagareDim")
  end
  table.insert(lines, head)
  table.insert(lines, line())

  -- Columns: slot, icon, sigil, name, status, age, message, branch.
  local name_w = math.max(math.min(24, math.floor(width * 0.26)), 10)
  local branch_w = math.max(math.min(28, math.floor(width * 0.24)), 8)
  local msg_w = width - (6 + 2 + 2 + name_w + 1 + 8 + 5 + branch_w + 1)

  for gi, g in ipairs(groups) do
    local st = g.entries[1] and g.entries[1].status or nil
    local l = line():add("  ")
    if g.entries[1] and require("nagare.review").needs_review(g.entries[1]) then
      l:add(nagare.review_icon, "NagareReview")
    elseif st then
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
    local r = table.concat(right, " · ")
    local pad = width - vim.fn.strdisplaywidth(l.text) - vim.fn.strdisplaywidth(r) - 1
    l:add(string.rep(" ", math.max(pad, 1))):add(r, "NagareDim")
    table.insert(lines, l)
    rows[#lines] = { kind = "project", root = g.root, key = "project:" .. g.root }

    if #g.entries == 0 then
      table.insert(lines, line():add("      no agents — a starts one", "NagareDim"))
      rows[#lines] = { kind = "project", root = g.root, key = "project:" .. g.root .. ":empty" }
    end
    for _, e in ipairs(g.entries) do
      local a = line():add("  ")
      a:add(slots[e] and tostring(slots[e]) or " ", "NagareSlot"):add(" ")
      local pending = require("nagare.review").needs_review(e)
      local icon = pending and nagare.review_icon or (nagare.status_icon[e.status] or "?")
      local hl = pending and "NagareReview" or nagare.status_hl[e.status]
      a:add(icon, hl)
      a:add(" "):add(nagare.sigil(e.kind), "NagareAgent_" .. (e.kind or ""))
      a:add(" "):add(util.fit(e.name, name_w), e.source == "tmux" and "NagareTmux" or nil)
      a:add(" "):add(util.fit(pending and "review" or (status_word[e.status] or e.status), 8), hl)
      a:add(util.fit(e.changed and util.ago(e.changed) or "", 5), "NagareDim")
      if msg_w > 8 then
        -- What it last said, or the task it was given before it said anything.
        -- A waiting agent's row says what it is asking for.
        local said = e.status == "waiting_input" and e.last_tool or e.last_message or e.task
        a:add(util.fit(util.oneline(said, msg_w), msg_w), e.status == "waiting_input" and e.last_tool and "NagareWaiting" or "NagareDim")
      end
      -- The header already names the main checkout's branch; repeating it on
      -- every child is noise. A branch that differs is worth showing.
      local tag = e.worktree and ("⎇ " .. e.worktree) or (e.branch ~= repo.branch and e.branch) or ""
      local changes = e.source == "nvim" and require("nagare.review").label(e) or ""
      -- The project's checks at its last stop: ✓ passed, ✗ failed (gave up).
      if e.verify == "pass" then
        changes = changes .. " ✓"
      elseif e.verify == "fail" then
        changes = changes .. " ✗"
      end
      changes = vim.trim(changes)
      if changes ~= "" then
        tag = changes .. (tag ~= "" and ("  " .. tag) or "")
      end
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

local function border()
  local b = config.board.border
  if b == nil and vim.fn.exists("+winborder") == 1 and vim.o.winborder ~= "" then
    return nil -- follow 'winborder'
  end
  return b or "rounded"
end

function M.render(focus_key)
  if not (state and api.nvim_win_is_valid(state.win)) then
    return
  end
  local keep = focus_key or (item() or {}).key
  local width = math.max(math.min(vim.o.columns - 8, config.board.width), 40)
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
      api.nvim_buf_set_extmark(state.buf, ns, i - 1, h[2], { end_col = h[3], hl_group = h[1] })
    end
  end

  local height = math.max(math.min(#lines, vim.o.lines - 6), 3)
  api.nvim_win_set_config(state.win, {
    relative = "editor",
    width = width,
    height = height,
    row = math.max(math.floor((vim.o.lines - height) / 2) - 1, 0),
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
      if rows[lnum] and rows[lnum].kind == "agent" then
        target = lnum
        break
      end
    end
  end
  if not target and next(rows) then
    target = math.min(unpack(vim.tbl_keys(rows)))
  end
  if target then
    api.nvim_win_set_cursor(state.win, { target, 0 })
  end
end

function M.close()
  if not state then
    return
  end
  local s = state
  state = nil
  if s.timer then
    s.timer:stop()
    s.timer:close()
  end
  pcall(api.nvim_del_augroup_by_name, "nagare_board")
  if api.nvim_win_is_valid(s.win) then
    pcall(api.nvim_win_close, s.win, true)
  end
  if api.nvim_buf_is_valid(s.buf) then
    pcall(api.nvim_buf_delete, s.buf, { force = true })
  end
end

function M.is_open()
  return state ~= nil
end

--- A help window listing every key, from the same table as the maps.
function M.help()
  if not state then
    return
  end
  local lines = { "" }
  for _, k in ipairs(M.keys) do
    table.insert(lines, ("  %-5s %s"):format(k[1], k[3]))
  end
  table.insert(lines, "")
  local buf = api.nvim_create_buf(false, true)
  api.nvim_buf_set_lines(buf, 0, -1, false, lines)
  vim.bo[buf].modifiable = false
  vim.bo[buf].bufhidden = "wipe"
  for i = 2, #lines - 1 do
    api.nvim_buf_set_extmark(buf, ns, i - 1, 2, { end_col = 7, hl_group = "NagareKey" })
  end
  local width = 0
  for _, l in ipairs(lines) do
    width = math.max(width, vim.fn.strdisplaywidth(l) + 2)
  end
  local win = api.nvim_open_win(buf, true, {
    relative = "editor", style = "minimal", border = border(), zindex = 60,
    width = width, height = #lines,
    row = math.floor((vim.o.lines - #lines) / 2), col = math.floor((vim.o.columns - width) / 2),
    title = " board keys ", title_pos = "center",
  })
  vim.wo[win].winhighlight = "NormalFloat:NagareNormal,FloatBorder:NagareBorder,FloatTitle:NagareTitle"
  for _, lhs in ipairs({ "q", "<Esc>", "g?" }) do
    vim.keymap.set("n", lhs, function()
      pcall(api.nvim_win_close, win, true)
    end, { buffer = buf, nowait = true })
  end
end

function M.open()
  if state then
    M.render()
    return
  end
  local buf = api.nvim_create_buf(false, true)
  vim.bo[buf].bufhidden = "wipe"
  vim.bo[buf].filetype = "nagare"
  local win = api.nvim_open_win(buf, true, {
    relative = "editor", width = 60, height = 3, row = 2, col = 2,
    style = "minimal", border = border(), zindex = 50,
    title = " agents ", title_pos = "center",
  })
  vim.wo[win].cursorline = true
  vim.wo[win].wrap = false
  vim.wo[win].winfixbuf = true
  vim.wo[win].winhighlight = "NormalFloat:NagareNormal,FloatBorder:NagareBorder,FloatTitle:NagareTitle"
  state = { buf = buf, win = win, rows = {} }

  local opts = { buffer = buf, nowait = true, silent = true }
  for _, k in ipairs(M.keys) do
    if k[4] then
      vim.keymap.set("n", k[1], k[4], vim.tbl_extend("force", opts, { desc = k[3] }))
    end
  end
  vim.keymap.set("n", "<Esc>", M.close, vim.tbl_extend("force", opts, { desc = "Close" }))
  for _, pair in ipairs({ { "j", 1 }, { "<Down>", 1 }, { "k", -1 }, { "<Up>", -1 } }) do
    vim.keymap.set("n", pair[1], function()
      move(pair[2])
    end, opts)
  end
  for n = 1, 9 do
    vim.keymap.set("n", tostring(n), function()
      M.close()
      require("nagare").slot(n)
    end, vim.tbl_extend("force", opts, { desc = "Agent in slot " .. n }))
  end

  local group = api.nvim_create_augroup("nagare_board", { clear = true })
  -- Change counts come in the background, per agent.
  for _, a in ipairs(agents.list) do
    if agents.alive(a) or a.status == "dead" then
      require("nagare.review").refresh(a)
    end
  end
  api.nvim_create_autocmd("User", {
    group = group,
    pattern = { "NagareStatus", "NagareReview" },
    callback = function()
      M.render()
    end,
  })
  api.nvim_create_autocmd("WinLeave", {
    group = group,
    buffer = buf,
    callback = function()
      vim.schedule(function()
        if not state or state.win ~= win then
          return
        end
        -- A float over the board (help, a vim.ui.select or input) is part
        -- of using it; only leaving for a real window closes it.
        local cur = api.nvim_get_current_win()
        if cur ~= win and api.nvim_win_get_config(cur).relative == "" and vim.fn.getcmdwintype() == "" then
          M.close()
        end
      end)
    end,
  })
  -- The age column goes stale while the board sits open.
  state.timer = util.uv.new_timer()
  state.timer:start(5000, 5000, vim.schedule_wrap(function()
    M.render()
  end))

  M.render()
end

return M
