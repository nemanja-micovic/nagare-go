-- Status tracking for the editor's own agents, from two sources:
--
--  1. Hook state files. Every agent nagare-go `setup` knows reports through
--     `nagare-go hook-state`, which writes ~/.local/share/nagare/states/*.json
--     keyed by pane — and inside Neovim the pane is the NAGARE_PANE id the
--     plugin gave the agent. A libuv watcher on that directory turns each
--     write into a status change within milliseconds; no polling.
--  2. Scraping the terminal buffer, for an agent that has never reported a
--     hook (not installed, or an agent with no hook support). The same
--     patterns the tmux scanner uses, read straight from the buffer instead
--     of `capture-pane`.
local agents = require("nagare.agents")
local config = require("nagare.config")
local util = require("nagare.util")

local M = {}

local hook_states = {
  working = "running",
  waiting_input = "waiting_input",
  idle = "idle",
  dead = "dead",
}

--- Applies one parsed state file. Returns true if it belonged to an agent.
function M.apply(state)
  if type(state) ~= "table" or not state.pane_id then
    return false
  end
  local agent = agents.by_key[state.pane_id]
  if not agent then
    return false
  end
  local ts = state.timestamp or ""
  -- An older write can arrive after a newer one; hooks run concurrently.
  if agent.hook_ts and ts < agent.hook_ts then
    return true
  end
  agent.hook_ts = ts
  agent.hooked = true
  -- A process that has exited stays dead whatever a late hook says.
  if agent.status == "dead" and agent.exit_code then
    return true
  end
  if state.verify == "untrusted" and agent.verify ~= "untrusted" then
    vim.notify(("nagare: %s has a .nagare/verify you have not approved, so it did not run — :Nagare trust")
      :format(agent.project), vim.log.levels.WARN, { title = "nagare", id = "nagare:trust:" .. agent.root })
  end
  local status = hook_states[state.state] or "idle"
  if state.transcript_path and state.transcript_path ~= "" then
    agent.transcript = state.transcript_path
    require("nagare.usage").refresh(agent)
  end
  agents.set_status(agent, status, {
    session_id = (state.session_id and state.session_id ~= "") and state.session_id or agent.session_id,
    event = state.event,
    last_tool = state.last_tool ~= "" and state.last_tool or nil,
    auto_approved = state.auto_approved,
    transcript = (state.transcript_path and state.transcript_path ~= "") and state.transcript_path or agent.transcript,
    verify = state.verify ~= "" and state.verify or nil,
    notification_type = state.notification_type,
    last_message = (state.last_message and state.last_message ~= "") and state.last_message or agent.last_message,
  })
  return true
end

function M.read(path)
  return util.json_decode(util.read_file(path))
end

--- Reads every state file once. Used at start and whenever the watcher is
--- unavailable.
function M.scan()
  local dir = config.states_dir
  local handle = util.uv.fs_scandir(dir)
  if not handle then
    return
  end
  while true do
    local name, kind = util.uv.fs_scandir_next(handle)
    if not name then
      break
    end
    if kind ~= "directory" and name:sub(-5) == ".json" then
      M.apply(M.read(dir .. "/" .. name))
    end
  end
end

local spinner = { "⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏", "⠐", "⠂" }

--- Classifies the tail of an agent's screen. Mirrors DetectStatus in
--- internal/tmux/status.go; order matters (a prompt beats a spinner that is
--- still on screen above it).
function M.detect(lines)
  -- Drop the blank rows below the cursor that a terminal buffer carries.
  local last = #lines
  while last > 0 and lines[last]:match("^%s*$") do
    last = last - 1
  end
  if last == 0 then
    return nil -- nothing drawn yet; leave the status alone
  end
  local first = math.max(1, last - 14)
  local tail = table.concat(lines, "\n", first, last)

  -- A permission prompt counts only while nothing has been answered below
  -- it: once a fresh input prompt is drawn underneath, the question on screen
  -- is history. (Claude clears its dialog itself; other agents leave it.)
  local waiting_at, prompt_at = 0, 0
  for i = first, last do
    local l = lines[i]
    if l:find("❯%s+%d+%.%s+Yes") or l:find("❯%s+%d+%.%s+No")
      or l:find("Do you want to", 1, true) or l:find("Esc to cancel", 1, true) then
      waiting_at = i
    elseif l:match("^❯%s*$") then
      prompt_at = i
    end
  end
  if waiting_at > prompt_at then
    return "waiting_input"
  end
  if tail:find("(running)", 1, true) or tail:find("esc to interrupt", 1, true) then
    return "running"
  end
  for _, s in ipairs(spinner) do
    if tail:find(s, 1, true) then
      return "running"
    end
  end
  for line in (tail .. "\n"):gmatch("(.-)\n") do
    if line:match("^❯%s*$") then
      return "idle"
    end
  end
  if tail:find("⏵⏵", 1, true) then
    return "running"
  end
  return "idle"
end

function M.scrape()
  for _, agent in ipairs(agents.list) do
    if not agent.hooked and agent.status ~= "dead" and agent.buf and vim.api.nvim_buf_is_valid(agent.buf) then
      local n = vim.api.nvim_buf_line_count(agent.buf)
      local lines = vim.api.nvim_buf_get_lines(agent.buf, math.max(0, n - 40), n, false)
      local status = M.detect(lines)
      if status and status ~= agent.status then
        agents.set_status(agent, status, { event = "scrape" })
      end
    end
  end
end

local watcher, timer
local pending = {} -- file name -> debounce timer

local function close(handle)
  if handle and not handle:is_closing() then
    handle:close()
  end
end

-- One write is several fs events (the temp file, the rename); coalesce them
-- per file. 30ms keeps a status change feeling instant.
local function debounce(dir, name)
  local t = pending[name]
  if t then
    t:stop()
  else
    t = util.uv.new_timer()
    pending[name] = t
  end
  t:start(30, 0, function()
    pending[name] = nil
    close(t)
    vim.schedule(function()
      local ok, err = pcall(M.apply, M.read(dir .. "/" .. name))
      if not ok then
        vim.notify("nagare: bad state file " .. name .. ": " .. tostring(err), vim.log.levels.DEBUG)
      end
    end)
  end)
end

local function stop_watcher()
  if watcher then
    pcall(watcher.stop, watcher)
    close(watcher)
    watcher = nil
  end
end

--- Starts watching (idempotent). Called on the first agent spawn: until an
--- agent exists there is nothing for a state file to change.
function M.start()
  if timer then
    return
  end
  local dir = config.states_dir
  vim.fn.mkdir(dir, "p")

  watcher = util.uv.new_fs_event()
  local ok = watcher and watcher:start(dir, {}, function(err, name)
    if err then
      -- A broken watcher must not keep claiming to watch: drop it and let
      -- the timer poll instead.
      vim.schedule(stop_watcher)
      return
    end
    if not name then
      -- luv sometimes reports a change without a name; read everything.
      vim.schedule(M.scan)
    elseif name:sub(-5) == ".json" then
      debounce(dir, name)
    end
  end)
  if not ok then
    stop_watcher()
  end

  timer = util.uv.new_timer()
  timer:start(config.poll_ms, config.poll_ms, vim.schedule_wrap(function()
    if not watcher then
      M.scan()
    end
    if config.scrape then
      M.scrape()
    end
  end))
end

function M.stop()
  stop_watcher()
  if timer then
    timer:stop()
    close(timer)
    timer = nil
  end
  for name, t in pairs(pending) do
    close(t)
    pending[name] = nil
  end
end

--- Restarts with the current config (tests, or after changing states_dir).
function M.restart()
  M.stop()
  M.start()
end

function M.watching()
  return watcher ~= nil
end

return M
