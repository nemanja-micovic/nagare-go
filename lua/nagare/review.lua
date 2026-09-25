-- Reviewing what an agent changed — the other half of running agents in
-- parallel. Every agent's work is measured against a base:
--
--   * an agent in a worktree: everything on its branch since it split from
--     the main checkout's branch, committed or not (merge-base..working tree)
--   * an agent in the main checkout: its uncommitted changes (HEAD..working)
--
-- `open` lays that out as a review tab: the changed files on the left, a
-- side-by-side diff on the right (the agent's side is the real file, so you
-- can fix things yourself). Comments collect like a pull request review and
-- go back to the agent as one message; a worktree can then be merged into
-- your branch or discarded.
local util = require("nagare.util")

local api = vim.api

local M = {}

local ns = api.nvim_create_namespace("nagare_review")

---@class nagare.Change
---@field path string relative to the agent's directory
---@field status "M"|"A"|"D"|"R"|"?"
---@field added integer
---@field removed integer

---@class nagare.Summary
---@field files integer
---@field added integer
---@field removed integer
---@field base string
---@field changes nagare.Change[]

M.cache = {} ---@type table<string, nagare.Summary> agent key -> summary

local function git(cwd, args)
  local out = vim.fn.systemlist(vim.list_extend({ "git", "-C", cwd }, args))
  return out, vim.v.shell_error == 0
end

--- The commit an agent's work is measured from, and a label for it.
function M.base(agent)
  if agent.worktree then
    local main = util.describe(agent.root).branch
    if main then
      local out, ok = git(agent.cwd, { "merge-base", "HEAD", main })
      if ok and out[1] then
        return out[1], main
      end
    end
  end
  return "HEAD", "HEAD"
end

--- Parses `git diff --numstat` and `--name-status` output plus untracked
--- files into changes. Pure, so tests feed it text.
function M.parse(numstat, namestatus, untracked, count_lines)
  local status = {}
  for _, l in ipairs(namestatus) do
    local s, path = l:match("^(%a)%d*\t(.+)$")
    if s then
      -- A rename lists "old\tnew"; the new path is the one on disk.
      path = path:match("\t(.+)$") or path
      status[path] = s
    end
  end
  local changes = {}
  for _, l in ipairs(numstat) do
    local a, r, path = l:match("^(%S+)\t(%S+)\t(.+)$")
    if path then
      path = path:match("=> (.+)}?$") and path:gsub("{(.-) => (.-)}", "%2"):gsub(".* => ", "") or path
      table.insert(changes, {
        path = path,
        status = status[path] or "M",
        added = tonumber(a) or 0,
        removed = tonumber(r) or 0,
      })
    end
  end
  for _, path in ipairs(untracked) do
    if path ~= "" then
      table.insert(changes, { path = path, status = "?", added = count_lines and count_lines(path) or 0, removed = 0 })
    end
  end
  table.sort(changes, function(x, y)
    return x.path < y.path
  end)
  return changes
end

local function summarize(changes, base)
  local s = { files = #changes, added = 0, removed = 0, base = base, changes = changes }
  for _, c in ipairs(changes) do
    s.added, s.removed = s.added + c.added, s.removed + c.removed
  end
  return s
end

--- Computes an agent's changes now (synchronously) and caches them.
---@return nagare.Summary?
function M.summary(agent)
  if not (agent.cwd and vim.fn.isdirectory(agent.cwd) == 1) then
    return nil
  end
  local base = M.base(agent)
  local numstat = git(agent.cwd, { "diff", "--numstat", "-M", base })
  local names = git(agent.cwd, { "diff", "--name-status", "-M", base })
  local untracked = git(agent.cwd, { "ls-files", "--others", "--exclude-standard" })
  local changes = M.parse(numstat, names, untracked, function(path)
    local ok, lines = pcall(vim.fn.readfile, agent.cwd .. "/" .. path)
    return ok and #lines or 0
  end)
  -- Counts alone miss an edit that keeps the line count; the file's mtime
  -- does not.
  for _, c in ipairs(changes) do
    c.mtime = vim.fn.getftime(agent.cwd .. "/" .. c.path)
  end
  local s = summarize(changes, base)
  M.cache[agent.key] = s
  return s
end

--- Refreshes an agent's cached summary in the background, then fires
--- NagareStatus so the board redraws. Never blocks the editor.
function M.refresh(agent)
  if not (vim.system and agent.cwd and vim.fn.isdirectory(agent.cwd) == 1) then
    return
  end
  local key = agent.key
  vim.schedule(function()
    local ok, s = pcall(M.summary, agent)
    if ok and s then
      M.cache[key] = s
      pcall(api.nvim_exec_autocmds, "User", { pattern = "NagareReview", modeline = false, data = { key = key } })
    end
  end)
end

-- Keyed by directory, not agent: the changes belong to the worktree, and a
-- resumed agent (new pane id) has not produced anything new to look at.
M.seen = {} ---@type table<string, string> agent cwd -> fingerprint of the changes last reviewed

local function fingerprint(s)
  local parts = {}
  for _, c in ipairs(s.changes) do
    table.insert(parts, ("%s:%d:%d:%d"):format(c.path, c.added, c.removed, c.mtime or 0))
  end
  return table.concat(parts, "|")
end

--- True when an agent has settled with changes you have not looked at:
--- idle or exited, something changed, and not what you last reviewed.
function M.needs_review(agent)
  if agent.source ~= "nvim" or (agent.status ~= "idle" and agent.status ~= "dead") then
    return false
  end
  local s = M.cache[agent.key]
  return s ~= nil and s.files > 0 and M.seen[agent.cwd] ~= fingerprint(s)
end

function M.mark_reviewed(agent)
  local s = M.cache[agent.key]
  if s then
    M.seen[agent.cwd] = fingerprint(s)
  end
end

--- Agents waiting for review, in the same stable order next_waiting uses.
function M.queue()
  local q = vim.tbl_filter(M.needs_review, require("nagare.agents").list)
  table.sort(q, function(a, b)
    return (a.project .. "\0" .. a.name) < (b.project .. "\0" .. b.name)
  end)
  return q
end

--- "+42 −7 · 3 files", or "" when there is nothing to review.
function M.label(agent)
  local s = M.cache[agent.key]
  if not s or s.files == 0 then
    return ""
  end
  return ("+%d −%d"):format(s.added, s.removed)
end

-- The review tab ------------------------------------------------------------

local state = {} -- tabpage -> review state

local function current()
  return state[api.nvim_get_current_tabpage()]
end

M.current = current

local function scratch(lines, name, ft)
  local buf = api.nvim_create_buf(false, true)
  api.nvim_buf_set_lines(buf, 0, -1, false, lines)
  vim.bo[buf].modifiable = false
  vim.bo[buf].bufhidden = "wipe"
  pcall(api.nvim_buf_set_name, buf, name)
  if ft and ft ~= "" then
    vim.bo[buf].filetype = ft
  end
  return buf
end

local function render_list(r)
  local s = r.summary
  local lines = {
    (" %s  %s"):format(require("nagare.agents").label(r.agent), r.agent.worktree and ("⎇ " .. r.agent.worktree) or ""),
    (" vs %s · %d files  +%d −%d"):format(r.base_label, s.files, s.added, s.removed),
    "",
  }
  r.rows = {}
  for i, c in ipairs(s.changes) do
    local mark = i == r.index and "▶" or " "
    table.insert(lines, ("%s %s %-3s %s"):format(mark, c.status, ("+" .. c.added), c.path))
    r.rows[#lines] = i
  end
  if #s.changes == 0 then
    table.insert(lines, "   nothing changed")
  end
  table.insert(lines, "")
  table.insert(lines, (" %d comment%s"):format(#r.comments, #r.comments == 1 and "" or "s"))
  for _, c in ipairs(r.comments) do
    table.insert(lines, ("  · %s:%d  %s"):format(c.path, c.from, util.oneline(c.text, 40)))
  end
  table.insert(lines, "")
  local keys = { "⏎ diff", "c comment", "S send review" }
  if r.agent.worktree then
    vim.list_extend(keys, { "m verify+merge", "P pull request", "X discard" })
  end
  if r.verify_output then
    table.insert(keys, "F send failures")
  end
  vim.list_extend(keys, { "R refresh", "q close" })
  for _, k in ipairs(keys) do
    table.insert(lines, " " .. k)
  end
  -- The diff's right side is the real file, where bare letters edit it; the
  -- review keys there are these.
  local ck = type(require("nagare.config").keys) == "table" and require("nagare.config").keys.comment
  table.insert(lines, "")
  table.insert(lines, " in the diff:")
  if ck then
    table.insert(lines, "  " .. ck:gsub("<leader>", vim.g.mapleader == " " and "␣" or "<leader>") .. " comment (or a selection)")
  end
  table.insert(lines, "  Tab / S-Tab next / prev file")

  local buf = r.list_buf
  vim.bo[buf].modifiable = true
  api.nvim_buf_set_lines(buf, 0, -1, false, lines)
  vim.bo[buf].modifiable = false
  api.nvim_buf_clear_namespace(buf, ns, 0, -1)
  api.nvim_buf_set_extmark(buf, ns, 0, 0, { end_row = 1, hl_group = "NagareProject" })
  api.nvim_buf_set_extmark(buf, ns, 1, 0, { end_row = 2, hl_group = "NagareDim" })
  local colors = { A = "DiagnosticOk", ["?"] = "DiagnosticOk", D = "DiagnosticError", M = "DiagnosticWarn", R = "DiagnosticInfo" }
  for lnum, i in pairs(r.rows) do
    local c = s.changes[i]
    api.nvim_buf_set_extmark(buf, ns, lnum - 1, 2, { end_col = 3, hl_group = colors[c.status] or "Normal" })
  end
end

local function show_comments(r)
  if not (r.file_buf and api.nvim_buf_is_valid(r.file_buf)) then
    return
  end
  api.nvim_buf_clear_namespace(r.file_buf, ns, 0, -1)
  local path = r.summary.changes[r.index] and r.summary.changes[r.index].path
  for _, c in ipairs(r.comments) do
    if c.path == path then
      pcall(api.nvim_buf_set_extmark, r.file_buf, ns, math.max(c.to - 1, 0), 0, {
        virt_lines = { { { "  💬 " .. c.text, "DiagnosticVirtualTextInfo" } } },
      })
    end
  end
end

local diff_keys = {}

--- Opens the diff for change i: the base version on the left, the agent's
--- file (a real, editable buffer) on the right.
function M.show(i, r)
  r = r or current()
  if not r then
    return
  end
  local c = r.summary.changes[i]
  if not c then
    return
  end
  r.index = i
  for _, win in ipairs({ r.left_win, r.right_win }) do
    if api.nvim_win_is_valid(win) then
      api.nvim_win_call(win, function()
        vim.cmd("diffoff")
      end)
    end
  end
  local ft = vim.filetype.match({ filename = c.path }) or ""
  local old = {}
  if c.status ~= "A" and c.status ~= "?" then
    old = git(r.agent.cwd, { "show", r.base .. ":" .. c.path })
  end
  local left = scratch(old, ("nagare://review/%d/%s@%s"):format(r.agent.id, c.path, r.base_label), ft)
  api.nvim_win_set_buf(r.left_win, left)

  local file = r.agent.cwd .. "/" .. c.path
  local right
  if c.status == "D" then
    right = scratch({}, ("nagare://review/%d/%s (deleted)"):format(r.agent.id, c.path), ft)
  else
    right = vim.fn.bufadd(file)
    vim.fn.bufload(right)
    vim.bo[right].buflisted = true
  end
  api.nvim_win_set_buf(r.right_win, right)
  r.file_buf = right

  for _, win in ipairs({ r.left_win, r.right_win }) do
    api.nvim_win_call(win, function()
      vim.cmd("diffthis")
      vim.wo.foldlevel = 0
    end)
  end
  -- Review keys on the diff buffers, removed again when the review closes.
  for _, b in ipairs({ left, right }) do
    if not diff_keys[b] then
      diff_keys[b] = true
      local o = { buffer = b, nowait = true, desc = "nagare review" }
      vim.keymap.set("n", "<Tab>", function() M.step(1) end, vim.tbl_extend("force", o, { desc = "Next changed file" }))
      vim.keymap.set("n", "<S-Tab>", function() M.step(-1) end, vim.tbl_extend("force", o, { desc = "Previous changed file" }))
    end
  end
  show_comments(r)
  render_list(r)
  api.nvim_set_current_win(r.right_win)
  pcall(vim.cmd, "normal! gg]c")
end

function M.step(delta)
  local r = current()
  if not r or #r.summary.changes == 0 then
    return
  end
  local n = #r.summary.changes
  M.show(((r.index - 1 + delta) % n) + 1, r)
end

--- Adds a review comment on lines from..to of the file being reviewed.
function M.comment(from, to, text)
  local r = current()
  if not r then
    vim.notify("nagare: open a review first (d on the board, or :Nagare review)", vim.log.levels.WARN)
    return
  end
  local c = r.summary.changes[r.index]
  if not c then
    return
  end
  local function add(t)
    if not t or t == "" then
      return
    end
    table.insert(r.comments, { path = c.path, from = from, to = to, text = t })
    show_comments(r)
    render_list(r)
  end
  if text then
    add(text)
  else
    vim.ui.input({ prompt = ("Comment on %s:%d-%d: "):format(c.path, from, to) }, add)
  end
end

--- The comments as one message, in the reference format agents understand.
function M.compose(comments)
  local lines = { "Review feedback — please address each point:" }
  for i, c in ipairs(comments) do
    local ref = c.from == c.to and ("@%s#L%d"):format(c.path, c.from) or ("@%s#L%d-%d"):format(c.path, c.from, c.to)
    table.insert(lines, ("%d. %s %s"):format(i, ref, c.text))
  end
  return table.concat(lines, " ")
end

--- Sends every comment to the agent as one message and submits it.
function M.submit(r)
  r = r or current()
  if not r or #r.comments == 0 then
    vim.notify("nagare: no comments to send", vim.log.levels.INFO)
    return false
  end
  local agents = require("nagare.agents")
  if not agents.alive(r.agent) then
    local ok, err = agents.resume(r.agent)
    if not ok then
      vim.notify("nagare: " .. err, vim.log.levels.ERROR)
      return false
    end
  end
  agents.send(r.agent, M.compose(r.comments) .. "\r")
  vim.notify(("nagare: sent %d comments to %s"):format(#r.comments, agents.label(r.agent)), vim.log.levels.INFO)
  r.comments = {}
  show_comments(r)
  render_list(r)
  return true
end

--- Merges a worktree agent's branch into the main checkout's branch.
--- Uncommitted work in the worktree is committed first, with your message.
function M.merge(agent, message)
  if not agent.worktree then
    return false, "only a worktree agent's branch can be merged"
  end
  local branch = util.describe(agent.cwd).branch
  local dirty = git(agent.cwd, { "status", "--porcelain" })
  if #dirty > 0 then
    git(agent.cwd, { "add", "-A" })
    local _, ok = git(agent.cwd, { "commit", "-q", "-m", message or ("nagare: " .. agent.name) })
    if not ok then
      return false, "could not commit the worktree's changes"
    end
  end
  local out, ok = git(agent.root, { "merge", "--no-ff", "-m", ("Merge %s (nagare agent %s)"):format(branch, agent.name), branch })
  util.forget_repos()
  if not ok then
    return false, "merge stopped: " .. table.concat(out, " "):sub(1, 200) .. " — resolve in " .. agent.root
  end
  return true
end

--- The project's verify command (.nagare/verify, comments dropped), looked
--- up in the agent's worktree and then the main checkout — the same file the
--- Stop hook runs.
function M.verify_command(agent)
  for _, dir in ipairs({ agent.cwd, agent.root }) do
    local path = dir .. "/.nagare/verify"
    if vim.fn.filereadable(path) == 1 then
      local lines = vim.tbl_filter(function(l)
        return vim.trim(l) ~= "" and not vim.startswith(vim.trim(l), "#")
      end, vim.fn.readfile(path))
      if #lines > 0 then
        return table.concat(lines, "\n")
      end
    end
  end
end

--- Runs the verify command in a terminal at the bottom of the review, then
--- calls done(ok, output_lines). The editor stays usable while it runs.
function M.run_verify(r, done)
  local cmd = M.verify_command(r.agent)
  if not cmd then
    done(true, {})
    return
  end
  api.nvim_set_current_win(r.right_win)
  vim.cmd("botright 12split")
  local win = api.nvim_get_current_win()
  local buf = api.nvim_create_buf(false, true)
  api.nvim_win_set_buf(win, buf)
  local opts = {
    cwd = r.agent.cwd,
    on_exit = function(_, code)
      vim.schedule(function()
        local lines = api.nvim_buf_is_valid(buf) and api.nvim_buf_get_lines(buf, 0, -1, false) or {}
        while #lines > 0 and vim.trim(lines[#lines]) == "" do
          table.remove(lines)
        end
        done(code == 0, vim.list_slice(lines, math.max(1, #lines - 80)))
      end)
    end,
  }
  if vim.fn.has("nvim-0.11") == 1 then
    opts.term = true
    vim.fn.jobstart({ "sh", "-c", cmd }, opts)
  else
    vim.fn.termopen({ "sh", "-c", cmd }, opts)
  end
  pcall(api.nvim_buf_set_name, buf, "nagare://verify/" .. r.agent.name)
end

--- Sends failing check output back to the agent to fix.
function M.send_failures(r)
  if not r.verify_output or #r.verify_output == 0 then
    vim.notify("nagare: no failing checks to send", vim.log.levels.INFO)
    return false
  end
  local agents = require("nagare.agents")
  if not agents.alive(r.agent) then
    local ok, err = agents.resume(r.agent)
    if not ok then
      vim.notify("nagare: " .. err, vim.log.levels.ERROR)
      return false
    end
  end
  local text = "The project's checks fail — please fix: " .. table.concat(r.verify_output, " | ")
  agents.send(r.agent, text:sub(1, 6000) .. "\r")
  vim.notify("nagare: sent the failures to " .. agents.label(r.agent), vim.log.levels.INFO)
  return true
end

--- Removes a worktree agent's worktree and branch, killing the agent.
function M.discard(agent)
  if not agent.worktree then
    return false, "only a worktree can be discarded"
  end
  local branch = util.describe(agent.cwd).branch
  local agents = require("nagare.agents")
  agents.kill(agent)
  git(agent.root, { "worktree", "unlock", agent.cwd })
  local out, ok = git(agent.root, { "worktree", "remove", "--force", agent.cwd })
  if not ok then
    return false, table.concat(out, " ")
  end
  if branch then
    git(agent.root, { "branch", "-D", branch })
  end
  agents.remove(agent)
  util.forget_repos()
  return true
end

function M.close(tab)
  tab = tab or api.nvim_get_current_tabpage()
  local r = state[tab]
  if not r then
    return
  end
  state[tab] = nil
  for b in pairs(diff_keys) do
    if api.nvim_buf_is_valid(b) then
      pcall(vim.keymap.del, "n", "<Tab>", { buffer = b })
      pcall(vim.keymap.del, "n", "<S-Tab>", { buffer = b })
      api.nvim_buf_clear_namespace(b, ns, 0, -1)
    end
    diff_keys[b] = nil
  end
  if api.nvim_tabpage_is_valid(tab) and #api.nvim_list_tabpages() > 1 then
    api.nvim_set_current_tabpage(tab)
    vim.cmd("diffoff!")
    vim.cmd("tabclose")
  end
end

local function confirm(prompt, yes, fn)
  vim.ui.select({ yes, "Cancel" }, { prompt = prompt }, function(choice)
    if choice == yes then
      fn()
    end
  end)
end

--- Opens a review tab for an agent.
function M.open(agent)
  local s = M.summary(agent)
  if not s then
    vim.notify("nagare: " .. agent.name .. " has no directory to review", vim.log.levels.WARN)
    return
  end
  local base, label = M.base(agent)
  M.mark_reviewed(agent)
  pcall(api.nvim_exec_autocmds, "User", { pattern = "NagareReview", modeline = false, data = { key = agent.key } })
  vim.cmd("tabnew")
  local tab = api.nvim_get_current_tabpage()
  api.nvim_tabpage_set_var(tab, "nagare_review", agent.id)
  vim.cmd("tcd " .. vim.fn.fnameescape(agent.cwd))

  local list_buf = api.nvim_create_buf(false, true)
  vim.bo[list_buf].bufhidden = "wipe"
  vim.bo[list_buf].filetype = "nagare_review"
  pcall(api.nvim_buf_set_name, list_buf, ("nagare://review/%d"):format(agent.id))
  local list_win = api.nvim_get_current_win()
  api.nvim_win_set_buf(list_win, list_buf)
  vim.cmd("rightbelow vsplit")
  local left_win = api.nvim_get_current_win()
  vim.cmd("rightbelow vsplit")
  local right_win = api.nvim_get_current_win()
  api.nvim_win_set_width(list_win, 42)
  vim.wo[list_win].winfixwidth = true
  vim.wo[list_win].number, vim.wo[list_win].relativenumber, vim.wo[list_win].cursorline = false, false, true

  local r = {
    agent = agent, summary = s, base = base, base_label = label, index = 1, comments = {},
    list_buf = list_buf, list_win = list_win, left_win = left_win, right_win = right_win, rows = {},
  }
  state[tab] = r

  local o = { buffer = list_buf, nowait = true, silent = true }
  local function at_cursor()
    return r.rows[api.nvim_win_get_cursor(list_win)[1]]
  end
  local function map(lhs, fn, desc)
    vim.keymap.set("n", lhs, fn, vim.tbl_extend("force", o, { desc = desc }))
  end
  map("<CR>", function()
    local i = at_cursor()
    if i then
      M.show(i, r)
    end
  end, "Open diff")
  map("c", function()
    api.nvim_set_current_win(r.right_win)
    local l = api.nvim_win_get_cursor(r.right_win)[1]
    M.comment(l, l)
  end, "Comment on the current line of the diff")
  map("S", function()
    M.submit(r)
  end, "Send the review to the agent")
  map("R", function()
    r.summary = M.summary(agent)
    render_list(r)
  end, "Refresh")
  map("q", function()
    M.close(tab)
  end, "Close review")
  local function do_merge()
    local f = r.summary.files
    confirm(("Merge %s (%d files) into %s?"):format(agent.name, f, label), "Merge", function()
      vim.ui.input({ prompt = "Commit message for uncommitted work: ", default = "nagare: " .. agent.name }, function(msg)
        local ok, err = M.merge(agent, msg)
        vim.notify(ok and ("nagare: merged " .. agent.name .. " into " .. label) or ("nagare: " .. err),
          ok and vim.log.levels.INFO or vim.log.levels.ERROR)
        if ok then
          M.close(tab)
        end
      end)
    end)
  end
  map("m", function()
    if not agent.worktree then
      vim.notify("nagare: this agent works in your checkout — its changes are already on your branch", vim.log.levels.INFO)
      return
    end
    -- Checks first: nothing lands on your branch that fails them.
    M.run_verify(r, function(ok, output)
      r.verify_output = ok and nil or output
      if ok then
        do_merge()
      else
        vim.notify("nagare: checks fail — not merged. F sends the failures to " .. agent.name, vim.log.levels.WARN)
      end
    end)
  end, "Verify, then merge the worktree branch")
  map("F", function()
    M.send_failures(r)
  end, "Send failing checks to the agent")
  map("P", function()
    if vim.fn.executable("gh") ~= 1 then
      vim.notify("nagare: P needs the GitHub CLI (gh)", vim.log.levels.WARN)
      return
    end
    local branch = util.describe(agent.cwd).branch
    if not branch or branch == label then
      vim.notify("nagare: open a PR from a worktree agent's own branch", vim.log.levels.WARN)
      return
    end
    require("nagare.peek").command({ "sh", "-c",
      ("git push -u origin %s && gh pr create --fill --head %s; echo; echo '(press a key)'; read -r _")
        :format(vim.fn.shellescape(branch), vim.fn.shellescape(branch)) }, "PR · " .. branch)
  end, "Push the branch and open a pull request")
  map("X", function()
    confirm(("Discard %s: delete its worktree and branch (%d files of changes)?"):format(agent.name, r.summary.files),
      "Discard", function()
        local ok, err = M.discard(agent)
        vim.notify(ok and ("nagare: discarded " .. agent.name) or ("nagare: " .. err),
          ok and vim.log.levels.INFO or vim.log.levels.ERROR)
        if ok then
          M.close(tab)
        end
      end)
  end, "Discard the worktree")

  api.nvim_create_autocmd("TabClosed", {
    group = api.nvim_create_augroup("nagare_review_" .. agent.id, { clear = true }),
    callback = function()
      for t in pairs(state) do
        if not api.nvim_tabpage_is_valid(t) then
          state[t] = nil
        end
      end
    end,
  })

  render_list(r)
  if #s.changes > 0 then
    M.show(1, r)
  else
    api.nvim_set_current_win(list_win)
  end
  return r
end

return M
