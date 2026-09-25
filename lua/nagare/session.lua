-- Session restore. `:mksession` (persistence.nvim — LazyVim's "Restore
-- Session" — mini.sessions, auto-session) silently drops terminal windows, so
-- a restored session came back with its project tabs but without a single
-- agent split. nagare keeps the layout it cares about in g:NagareLayout — a
-- String global, which sessions save when 'sessionoptions' has "globals"
-- (LazyVim's does) — and rebuilds it on SessionLoadPost: each agent split
-- returns holding a placeholder that resumes the agent on Enter. Nothing
-- starts until you ask. resession users get the same through
-- lua/resession/extensions/nagare.lua.
local api = vim.api

local M = {}

local function agents()
  return require("nagare.agents")
end

--- The agent splits on screen, per tab: what a session should bring back.
function M.snapshot()
  local out = {}
  for _, tab in ipairs(api.nvim_list_tabpages()) do
    for _, win in ipairs(api.nvim_tabpage_list_wins(tab)) do
      if api.nvim_win_get_config(win).relative == "" then
        local buf = api.nvim_win_get_buf(win)
        local a = agents().from_buf(buf)
        if a then
          table.insert(out, {
            tab = api.nvim_tabpage_get_number(tab),
            cwd = a.cwd, name = a.name, kind = a.kind, root = a.root,
            width = api.nvim_win_get_width(win),
          })
        end
      end
    end
  end
  return out
end

local pending_save = false

--- Records the snapshot in g:NagareLayout (debounced; called on window
--- changes), so any session plugin saves it without knowing about nagare.
function M.record()
  if pending_save then
    return
  end
  pending_save = true
  vim.schedule(function()
    pending_save = false
    local ok, json = pcall(vim.json.encode, M.snapshot())
    if ok then
      vim.g.NagareLayout = json
    end
  end)
end

local function find_agent(entry)
  for _, a in ipairs(agents().list) do
    if a.cwd == entry.cwd and a.name == entry.name then
      return a
    end
  end
end

--- A buffer standing in for an agent until it is resumed.
function M.placeholder(agent)
  local buf = api.nvim_create_buf(false, true)
  vim.bo[buf].bufhidden = "wipe"
  vim.bo[buf].filetype = "nagare_saved"
  vim.b[buf].nagare_agent = agent.id
  pcall(api.nvim_buf_set_name, buf, ("nagare://%s/%s (saved)"):format(agent.project, agent.name))
  api.nvim_buf_set_lines(buf, 0, -1, false, {
    "",
    ("  ◌ %s"):format(agents().label(agent)),
    "",
    "  saved from your last session" .. (agent.session_id and " — resumes its conversation" or ""),
    "",
    "  Enter  resume here",
    "  q      close this split",
  })
  vim.bo[buf].modifiable = false
  local function resume()
    local win = api.nvim_get_current_win()
    local ok, err = agents().resume(agent)
    if not ok then
      vim.notify("nagare: " .. err, vim.log.levels.ERROR)
      return
    end
    api.nvim_win_set_buf(win, agent.buf)
    if require("nagare.config").insert_on_jump then
      vim.cmd("startinsert")
    end
  end
  vim.keymap.set("n", "<CR>", resume, { buffer = buf, desc = "Resume agent" })
  vim.keymap.set("n", "q", "<Cmd>close<CR>", { buffer = buf, desc = "Close" })
  return buf
end

--- Rebuilds agent splits from a snapshot: into each project's tab, beside
--- its code, holding the live agent's buffer or a placeholder for a saved
--- one. Returns how many splits came back.
function M.restore(snapshot)
  if type(snapshot) ~= "table" then
    return 0
  end
  local projects = require("nagare.projects")
  local tabs = api.nvim_list_tabpages()
  local n = 0
  for _, entry in ipairs(snapshot) do
    local agent = find_agent(entry)
    local tab = entry.root and projects.tab_for(entry.root) or tabs[entry.tab]
    if agent and tab and api.nvim_tabpage_is_valid(tab) then
      api.nvim_set_current_tabpage(tab)
      local exists = false
      for _, w in ipairs(api.nvim_tabpage_list_wins(tab)) do
        if agents().from_buf(api.nvim_win_get_buf(w)) == agent then
          exists = true
        end
      end
      if not exists then
        vim.cmd(("botright %dvsplit"):format(math.max(entry.width or 60, 20)))
        vim.wo.winfixwidth = true
        vim.wo.number, vim.wo.relativenumber, vim.wo.signcolumn = false, false, "no"
        local live = agents().alive(agent) and agent.buf and api.nvim_buf_is_valid(agent.buf)
        api.nvim_win_set_buf(0, live and agent.buf or M.placeholder(agent))
        vim.cmd("wincmd p")
        n = n + 1
      end
    end
  end
  return n
end

--- Hooks into session loading and window changes.
function M.setup(group)
  api.nvim_create_autocmd({ "WinNew", "WinClosed", "BufWinEnter", "TabClosed" }, {
    group = group,
    callback = M.record,
  })
  api.nvim_create_autocmd("SessionLoadPost", {
    group = group,
    callback = function()
      local raw = vim.g.NagareLayout
      if type(raw) ~= "string" or raw == "" then
        return
      end
      -- Saved agents are loaded at startup; a session sourced later (the
      -- dashboard's "Restore Session") finds them already there.
      if #agents().list == 0 and require("nagare.config").restore then
        agents().restore()
      end
      local ok, snap = pcall(vim.json.decode, raw)
      if ok then
        vim.schedule(function()
          M.restore(snap)
        end)
      end
    end,
  })
end

return M
