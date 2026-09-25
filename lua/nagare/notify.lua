-- Notifications, designed around LazyVim's toasts (snacks.nvim notifier) and
-- degrading to plain vim.notify everywhere else.
--
-- One toast per agent, keyed by its id, so an agent never stacks toasts: a
-- "needs you" toast is replaced by "finished" rather than joined by it. A
-- waiting toast has no timeout — it stays until the agent is answered, from
-- anywhere (the board, a peek, the agent's own terminal), and then goes away
-- by itself. Everything lands in the notifier's history (<leader>n in
-- LazyVim), which doubles as a log of what your agents did while you were
-- elsewhere.
local config = require("nagare.config")
local util = require("nagare.util")

local M = {}

local shown = {} -- agent key -> notification id currently on screen

local function snacks()
  local s = rawget(_G, "Snacks")
  return s and s.notifier and s.notifier.hide and s or nil
end

local function send(agent, msg, level, opts)
  opts = vim.tbl_extend("force", { title = "nagare", id = "nagare:" .. agent.key }, opts or {})
  if not snacks() then
    -- Only snacks renders the markdown; elsewhere it would show as asterisks.
    msg = msg:gsub("%*%*", "")
  end
  -- Remember our own id, not the return value: noice (LazyVim routes
  -- vim.notify through it) returns its own table but hands `id` on to the
  -- snacks notifier, which is where the toast has to be hidden from.
  if pcall(vim.notify, msg, level, opts) then
    shown[agent.key] = opts.id
  end
end

--- Clears the toast for an agent, when the notifier supports it.
function M.clear(agent)
  local id = shown[agent.key]
  shown[agent.key] = nil
  local s = snacks()
  if id and s then
    pcall(s.notifier.hide, id)
  end
end

local function jump_hint()
  local keys = config.keys
  local key = type(keys) == "table" and keys.next_waiting
  return key and ("  %s to jump"):format(key:gsub("<leader>", vim.g.mapleader == " " and "␣" or "<leader>")) or ""
end

--- Announces a status transition. Called by agents.set_status.
---@param agent table
---@param prev string?
---@param held integer seconds spent in prev
---@param visible boolean the agent is already on screen
function M.transition(agent, prev, held, visible)
  if vim.v.exiting ~= vim.NIL then
    return
  end
  local opts = config.notify
  local label = agent.project .. "/" .. agent.name
  local status = agent.status

  if status == "waiting_input" then
    if opts.waiting and not visible then
      -- What it is asking for: the tool call itself when a hook reported it
      -- ("Bash: rm -rf build"), else the kind of prompt.
      local detail = agent.last_tool and util.oneline(agent.last_tool, 80)
        or (agent.notification_type and agent.notification_type ~= "" and agent.notification_type:gsub("_", " "))
        or util.oneline(agent.last_message, 60)
      send(agent, ("**%s** needs you%s\n%s"):format(label, jump_hint(), detail ~= "" and detail or "waiting for input"),
        vim.log.levels.WARN, { timeout = false, icon = "● ", ft = "markdown" })
    end
    return
  end

  -- Leaving waiting answers the toast, whatever comes next.
  if prev == "waiting_input" then
    M.clear(agent)
  end

  if status == "idle" and prev == "running" and opts.finished and held >= opts.min_seconds and not visible then
    local msg = util.oneline(agent.last_message, 120)
    local ok, s = pcall(require("nagare.review").summary, agent)
    local changes = ""
    if ok and s and s.files > 0 then
      local keys = config.keys
      local key = type(keys) == "table" and keys.review
      changes = ("\n%d file%s +%d −%d%s"):format(s.files, s.files == 1 and "" or "s", s.added, s.removed,
        key and ("  " .. key:gsub("<leader>", vim.g.mapleader == " " and "␣" or "<leader>") .. " to review") or "")
    end
    send(agent, ("**%s** finished after %s%s%s"):format(label, util.ago(os.time() - held), changes, msg ~= "" and ("\n" .. msg) or ""),
      vim.log.levels.INFO, { icon = "✓ ", ft = "markdown" })
  elseif status == "dead" and prev ~= nil and prev ~= "saved" and agent.exit_code and agent.exit_code ~= 0 then
    send(agent, ("**%s** exited with code %d"):format(label, agent.exit_code), vim.log.levels.ERROR, { icon = "✕ ", ft = "markdown" })
  end
end

-- Test hook.
function M._shown()
  return shown
end

return M
