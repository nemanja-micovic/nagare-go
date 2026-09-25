-- Memory, from the editor: what agents learned on this repository, as the
-- Markdown files they are. The store and search live in nagare-go (so every
-- agent reaches the same memory through MCP); here you browse it, fix a
-- wrong note by editing its file, write your own, and see — as it happens —
-- what an agent decided was worth remembering.
local config = require("nagare.config")
local util = require("nagare.util")

local api = vim.api

local M = {}

local function bin()
  return config.tmux.bin
end

local function available()
  return vim.fn.executable(bin()) == 1
end

local function run(args, root)
  local cmd = vim.list_extend({ bin(), "memory" }, args)
  vim.list_extend(cmd, { "--cwd", root })
  local out = vim.fn.systemlist(cmd)
  return out, vim.v.shell_error == 0
end

--- The repository's memories (and the global ones), newest first.
---@return table[] entries as `nagare-go memory ls --json` prints them
function M.list(root, query)
  if not available() then
    return {}
  end
  local args = query and query ~= "" and { "search", "--json", query } or { "ls", "--json" }
  local out, ok = run(args, root)
  if not ok then
    return {}
  end
  return util.json_decode(table.concat(out, "\n")) or {}
end

function M.dir(root)
  local out, ok = run({ "path" }, root)
  return ok and out[1] or nil
end

local kind_icon = { gotcha = "⚠", decision = "◇", convention = "§", fact = "·", preference = "♥" }

function M.format_line(m)
  return ("%s %s  %s%s"):format(kind_icon[m.kind] or "·", m.title, m.kind,
    m.scope == "global" and " · global" or "")
end

--- Opens a memory's file for reading or editing.
function M.open(m)
  vim.cmd("edit " .. vim.fn.fnameescape(m.file))
end

--- Marks a memory archived by editing its front matter — the same thing an
--- agent's update_memory(status=archived) does.
function M.archive(m)
  local lines = vim.fn.readfile(m.file)
  for i, l in ipairs(lines) do
    if l:match("^status:") then
      lines[i] = "status: archived"
      vim.fn.writefile(lines, m.file)
      return true
    end
    if l == "---" and i > 1 then
      table.insert(lines, i, "status: archived")
      vim.fn.writefile(lines, m.file)
      return true
    end
  end
  return false
end

--- A buffer to write a new memory in. Saving it stores the memory through
--- nagare-go (so the duplicate and secret checks apply) and opens the file.
function M.new(root)
  root = root or require("nagare.projects").current_root()
  local buf = api.nvim_create_buf(true, true)
  api.nvim_buf_set_name(buf, "nagare://memory/new/" .. util.basename(root) .. "/" .. os.time())
  vim.bo[buf].buftype = "acwrite"
  vim.bo[buf].filetype = "markdown"
  api.nvim_buf_set_lines(buf, 0, -1, false, {
    "kind: gotcha",
    "scope: project",
    "",
    "Title on this line — what future agents should know",
    "",
    "Why it matters, and how to handle it.",
  })
  api.nvim_create_autocmd("BufWriteCmd", {
    buffer = buf,
    callback = function()
      local lines = api.nvim_buf_get_lines(buf, 0, -1, false)
      local kind, scope, body = "fact", "project", {}
      for i, l in ipairs(lines) do
        local k = l:match("^kind:%s*(%S+)")
        local s = l:match("^scope:%s*(%S+)")
        if k and i <= 3 then
          kind = k
        elseif s and i <= 3 then
          scope = s
        elseif not (i <= 3 and l == "") then
          table.insert(body, l)
        end
      end
      local text = vim.trim(table.concat(body, "\n"))
      if text == "" then
        vim.notify("nagare: empty memory not saved", vim.log.levels.WARN)
        return
      end
      local out, ok = run({ "add", "--json", "--kind", kind, "--scope", scope, text }, root)
      if not ok then
        vim.notify("nagare: " .. table.concat(out, " "):gsub("^Error: ", ""), vim.log.levels.WARN)
        return
      end
      local saved = (util.json_decode(table.concat(out, "\n")) or {})[1]
      vim.bo[buf].modified = false
      if saved then
        vim.notify("nagare: remembered [" .. saved.id .. "] " .. saved.title, vim.log.levels.INFO)
        vim.cmd("edit " .. vim.fn.fnameescape(saved.file))
        pcall(api.nvim_buf_delete, buf, { force = true })
      end
    end,
  })
  api.nvim_set_current_buf(buf)
  api.nvim_win_set_cursor(0, { 4, 0 })
end

--- Browses memories: snacks' picker with a file preview when available,
--- else vim.ui.select.
function M.pick(root)
  root = root or require("nagare.projects").current_root()
  if not available() then
    vim.notify("nagare: memory needs nagare-go in PATH", vim.log.levels.WARN)
    return
  end
  local snacks = rawget(_G, "Snacks")
  if snacks and snacks.picker and snacks.picker.sources and snacks.picker.sources.nagare_memory then
    return snacks.picker.nagare_memory({ root = root })
  end
  local items = M.list(root)
  if #items == 0 then
    vim.notify("nagare: no memories yet for " .. util.basename(root) .. " — agents save them with remember; :Nagare memory new to write one", vim.log.levels.INFO)
    return
  end
  vim.ui.select(items, { prompt = "Memory", kind = "nagare_memory", format_item = M.format_line }, function(m)
    if m then
      M.open(m)
    end
  end)
end

-- Seeing agents remember -----------------------------------------------------

local watchers = {} -- dir -> fs_event

local function announce(dir, name)
  local path = dir .. "/" .. name
  local ok, lines = pcall(vim.fn.readfile, path, "", 40)
  if not ok then
    return
  end
  local author, kind, title, in_head = "an agent", "fact", nil, false
  for i, l in ipairs(lines) do
    if i == 1 and l == "---" then
      in_head = true
    elseif in_head and l == "---" then
      in_head = false
    elseif in_head then
      author = l:match("^author:%s*(.+)") or author
      kind = l:match("^kind:%s*(.+)") or kind
    elseif not title and vim.trim(l) ~= "" then
      title = vim.trim(l:gsub("^#+%s*", ""))
    end
  end
  if title and author ~= "user" then
    vim.notify(("🧠 %s remembered (%s): %s"):format(author, kind, title), vim.log.levels.INFO,
      { title = "nagare", id = "nagare:memory", timeout = 4000 })
  end
end

--- Watches a repository's memory directory so a note an agent saves is
--- announced (once, when the file first appears).
function M.watch(root)
  if not available() or not config.notify.memory then
    return
  end
  local dir = M.dir(root)
  if not dir or watchers[dir] then
    return
  end
  vim.fn.mkdir(dir, "p")
  local seen = {}
  for _, f in ipairs(vim.fn.readdir(dir)) do
    seen[f] = true
  end
  local w = util.uv.new_fs_event()
  if not w then
    return
  end
  watchers[dir] = w
  w:start(dir, {}, function(err, name)
    if err or not name or not name:match("%.md$") or seen[name] then
      return
    end
    seen[name] = true
    -- Let the atomic rename settle before reading.
    vim.defer_fn(function()
      announce(dir, name)
    end, 50)
  end)
end

function M.stop()
  for dir, w in pairs(watchers) do
    pcall(w.stop, w)
    if not w:is_closing() then
      w:close()
    end
    watchers[dir] = nil
  end
end

-- Snacks picker source --------------------------------------------------------

M.source = {
  source = "nagare_memory",
  title = "Memory",
  finder = function(opts)
    local items = {}
    for i, m in ipairs(M.list(opts.root or require("nagare.projects").current_root())) do
      table.insert(items, {
        idx = i,
        memory = m,
        file = m.file,
        text = table.concat({ m.title, m.kind, m.scope, table.concat(m.tags or {}, " "), table.concat(m.paths or {}, " ") }, " "),
      })
    end
    return items
  end,
  format = function(item)
    local m = item.memory
    return {
      { (kind_icon[m.kind] or "·") .. " ", "NagareKey" },
      { m.title },
      { "  " .. m.kind, "NagareDim" },
      { m.scope == "global" and "  global" or "", "NagareDim" },
      { m.pinned and "  pinned" or "", "NagareSlot" },
      { m.author and ("  " .. m.author) or "", "NagareDim" },
    }
  end,
  preview = "file",
  confirm = function(picker, item)
    picker:close()
    if item then
      M.open(item.memory)
    end
  end,
  actions = {
    nagare_memory_archive = function(picker, item)
      if item and M.archive(item.memory) then
        vim.notify("nagare: archived " .. item.memory.title, vim.log.levels.INFO)
        picker:find()
      end
    end,
    nagare_memory_new = function(picker)
      picker:close()
      M.new()
    end,
  },
  win = {
    input = {
      keys = {
        ["<c-x>"] = { "nagare_memory_archive", mode = { "n", "i" }, desc = "Archive" },
        -- Keys snacks leaves free (<c-n> is its list-down).
        ["<c-e>"] = { "nagare_memory_new", mode = { "n", "i" }, desc = "New memory" },
      },
    },
  },
}

return M
