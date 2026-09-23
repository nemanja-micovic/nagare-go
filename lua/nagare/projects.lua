-- Projects are repositories, and each open project is a tabpage with a
-- tab-local working directory (:tcd). Worktrees of one repo are one project:
-- their agents group under it, the way the tmux picker groups windows.
local config = require("nagare.config")
local util = require("nagare.util")

local api = vim.api

local M = {}

local function recent_path()
  return vim.fn.stdpath("data") .. "/nagare/projects.json"
end

local recent -- list of { root = ..., used = epoch }, newest first

local function load_recent()
  if recent then
    return recent
  end
  recent = util.json_decode(util.read_file(recent_path())) or {}
  return recent
end

--- Records that root was used, keeping the list newest first and bounded.
function M.remember(root)
  if not root or root == "" then
    return
  end
  load_recent()
  for i, r in ipairs(recent) do
    if r.root == root then
      table.remove(recent, i)
      break
    end
  end
  table.insert(recent, 1, { root = root, used = os.time() })
  while #recent > config.options.recent_limit do
    table.remove(recent)
  end
  util.write_file(recent_path(), vim.json.encode(recent))
end

--- The project root a tab is working in, or nil for a tab with no
--- tab-local directory that nagare did not open.
function M.tab_root(tab)
  local ok, root = pcall(api.nvim_tabpage_get_var, tab, "nagare_root")
  if ok then
    return root
  end
end

function M.tab_for(root)
  for _, tab in ipairs(api.nvim_list_tabpages()) do
    if M.tab_root(tab) == root then
      return tab
    end
  end
  -- A tab the user :tcd'd into by hand counts too.
  for _, tab in ipairs(api.nvim_list_tabpages()) do
    local nr = api.nvim_tabpage_get_number(tab)
    if vim.fn.haslocaldir(-1, nr) == 1 then
      local dir = util.normalize(vim.fn.getcwd(-1, nr))
      if util.describe(dir).root == root then
        api.nvim_tabpage_set_var(tab, "nagare_root", root)
        return tab
      end
    end
  end
end

--- The project the cursor is in: the current tab's, else the current
--- buffer's repository, else the working directory's.
function M.current_root()
  local root = M.tab_root(api.nvim_get_current_tabpage())
  if root then
    return root
  end
  local agent = require("nagare.agents").from_buf(0)
  if agent then
    return agent.root
  end
  local file = api.nvim_buf_get_name(0)
  local dir = (file ~= "" and vim.bo.buftype == "") and vim.fn.fnamemodify(file, ":p:h") or vim.fn.getcwd()
  if vim.fn.isdirectory(dir) == 0 then
    dir = vim.fn.getcwd()
  end
  return util.describe(dir).root
end

-- Start screens count as an untouched tab: opening a project replaces them
-- rather than leaving a dashboard tab behind.
local start_screens = {
  snacks_dashboard = true, dashboard = true, alpha = true, starter = true, ministarter = true,
}

local function untouched()
  if #api.nvim_list_tabpages() ~= 1 or M.tab_root(api.nvim_get_current_tabpage()) then
    return false
  end
  local wins = vim.tbl_filter(function(w)
    return api.nvim_win_get_config(w).relative == ""
  end, api.nvim_tabpage_list_wins(0))
  if #wins ~= 1 then
    return false
  end
  if start_screens[vim.bo.filetype] then
    return true
  end
  return api.nvim_buf_get_name(0) == "" and not vim.bo.modified and vim.bo.buftype == ""
    and api.nvim_buf_line_count(0) <= 1
end

--- Opens the user's file picker in a new project tab.
local function auto_open()
  if _G.Snacks and Snacks.picker and Snacks.picker.files then
    Snacks.picker.files()
  elseif pcall(require, "fzf-lua") then
    require("fzf-lua").files()
  elseif pcall(require, "telescope.builtin") then
    require("telescope.builtin").find_files()
  end
end

--- Switches to root's tab, opening one if needed. Returns the tab and
--- whether it was just created. opts.explicit marks a project the user
--- asked to open (rather than one entered to show an agent), which is when
--- on_project_open runs.
function M.open(root, opts)
  root = util.normalize(root)
  local tab = M.tab_for(root)
  local created = false
  if tab then
    api.nvim_set_current_tabpage(tab)
  else
    -- Reuse the only tab when it is an untouched start screen.
    if untouched() then
      if start_screens[vim.bo.filetype] then
        vim.cmd("enew")
      end
    else
      vim.cmd("tabnew")
    end
    tab = api.nvim_get_current_tabpage()
    vim.cmd("tcd " .. vim.fn.fnameescape(root))
    api.nvim_tabpage_set_var(tab, "nagare_root", root)
    created = true
  end
  M.remember(root)
  local hook = config.options.on_project_open
  if created and opts and opts.explicit and hook then
    local ok, err = pcall(hook == "auto" and auto_open or hook, root)
    if not ok then
      vim.notify("nagare: on_project_open: " .. tostring(err), vim.log.levels.ERROR)
    end
  end
  vim.cmd("redrawtabline")
  return tab, created
end

--- Every project nagare knows of: open tabs, running agents, tmux agents,
--- configured globs and recent use. Returns a list of
--- { root, name, tab, recent } sorted by name.
function M.known()
  local seen, out = {}, {}
  local function add(root, fields)
    if not root or root == "" then
      return
    end
    local p = seen[root]
    if not p then
      p = { root = root, name = util.basename(root) }
      seen[root] = p
      table.insert(out, p)
    end
    for k, v in pairs(fields or {}) do
      p[k] = v
    end
  end

  for _, tab in ipairs(api.nvim_list_tabpages()) do
    local root = M.tab_root(tab)
    if root then
      add(root, { tab = tab })
    end
  end
  for _, a in ipairs(require("nagare.agents").list) do
    add(a.root)
  end
  for _, a in ipairs(require("nagare.tmux").list) do
    add(a.root)
  end
  for _, pattern in ipairs(config.options.projects or {}) do
    for _, dir in ipairs(vim.fn.glob(vim.fn.expand(pattern), false, true)) do
      if vim.fn.isdirectory(dir .. "/.git") == 1 or vim.fn.filereadable(dir .. "/.git") == 1 then
        add(util.normalize(dir))
      end
    end
  end
  for _, r in ipairs(load_recent()) do
    if vim.fn.isdirectory(r.root) == 1 then
      add(r.root, { recent = r.used })
    end
  end
  return out
end

--- Lets the user choose a project and opens it.
function M.pick(cb)
  local list = M.known()
  table.sort(list, function(a, b)
    return (a.recent or 0) > (b.recent or 0)
  end)
  vim.ui.select(list, {
    prompt = "Project",
    format_item = function(p)
      return ("%-24s %s"):format(p.name, vim.fn.fnamemodify(p.root, ":~"))
    end,
  }, function(p)
    if p then
      M.open(p.root, { explicit = true })
      if cb then
        cb(p.root)
      end
    end
  end)
end

--- Creates a linked worktree on a new branch of the same name, under
--- <root>/.worktrees like the Go side does for non-Claude agents.
function M.add_worktree(root, name)
  if not name:match("^[%w][%w._/-]*$") or name:find("%.%.") then
    return nil, "invalid worktree name: " .. name
  end
  local path = root .. "/.worktrees/" .. name
  local out = vim.fn.system({ "git", "-C", root, "worktree", "add", path, "-b", name })
  if vim.v.shell_error ~= 0 then
    return nil, vim.trim(out)
  end
  util.forget_repos()
  return path
end

-- Test hook.
function M._reset()
  recent = nil
end

return M
