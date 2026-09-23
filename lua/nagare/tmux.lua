-- Agents that live in tmux, read from `nagare-go ls` (internal/nvim on the
-- Go side). They join the board under the same projects as the editor's
-- agents, so one view covers agents wherever they were started.
local config = require("nagare.config")
local util = require("nagare.util")

local M = {}

M.list = {}
local last_raw
local timer

local function in_tmux()
  return vim.env.TMUX ~= nil and vim.env.TMUX ~= ""
end

function M.available()
  local opts = config.options.tmux
  return opts.enabled and vim.fn.executable(opts.bin) == 1 and vim.fn.executable("tmux") == 1
end

--- Turns `nagare-go ls` JSON into board entries.
function M.parse(raw)
  local data = util.json_decode(raw)
  if type(data) ~= "table" then
    return {}
  end
  local out = {}
  for _, e in ipairs(data) do
    local root = e.root ~= "" and e.root or e.path
    table.insert(out, {
      source = "tmux",
      key = "tmux:" .. (e.pane_id or e.target),
      name = e.worktree and e.worktree ~= "" and e.worktree or e.name,
      kind = e.agent,
      status = e.status == "saved" and "idle" or e.status,
      root = root,
      project = util.basename(root),
      cwd = e.path,
      target = e.target,
      session = e.session,
      worktree = e.worktree,
      branch = e.branch,
      last_message = e.last_message,
      changed = util.parse_time(e.last_activity),
    })
  end
  return out
end

function M.refresh()
  if not M.available() then
    return
  end
  local chunks = {}
  vim.fn.jobstart({ config.options.tmux.bin, "ls" }, {
    stdout_buffered = true,
    on_stdout = function(_, data)
      chunks = data
    end,
    on_exit = function(_, code)
      if code ~= 0 then
        return
      end
      vim.schedule(function()
        local raw = table.concat(chunks, "\n")
        if raw == last_raw then
          return
        end
        last_raw = raw
        M.list = M.parse(raw)
        vim.cmd("redrawtabline")
        pcall(vim.api.nvim_exec_autocmds, "User", { pattern = "NagareStatus", modeline = false, data = { source = "tmux" } })
      end)
    end,
  })
end

function M.start()
  M.stop()
  if not M.available() then
    return
  end
  M.refresh()
  timer = util.uv.new_timer()
  timer:start(config.options.tmux.poll_ms, config.options.tmux.poll_ms, vim.schedule_wrap(M.refresh))
end

function M.stop()
  if timer then
    timer:stop()
    timer:close()
    timer = nil
  end
end

--- Takes the user to a tmux agent: switches the client when Neovim is itself
--- inside tmux, otherwise attaches to it in a floating terminal (detach with
--- the tmux prefix + d to come back).
function M.jump(entry)
  if in_tmux() then
    vim.fn.system({ "tmux", "switch-client", "-t", entry.target })
    return
  end
  vim.fn.system({ "tmux", "select-window", "-t", entry.target })
  vim.fn.system({ "tmux", "select-pane", "-t", entry.target })
  require("nagare.peek").command({ "tmux", "attach-session", "-t", entry.session }, entry.project .. "/" .. entry.name)
end

function M.approve(entry, always)
  if entry.status ~= "waiting_input" then
    return false
  end
  local keys = always and { "Down", "Enter" } or { "Enter" }
  vim.fn.system(vim.list_extend({ "tmux", "send-keys", "-t", entry.target }, keys))
  return vim.v.shell_error == 0
end

return M
