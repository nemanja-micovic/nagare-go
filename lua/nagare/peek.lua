-- A floating window over whatever you are editing, showing an agent's
-- terminal: answer its question, then drop straight back into your file.
-- The float closes itself the moment focus leaves it.
local api = vim.api

local M = {}

local current -- { win = , buf = }

local function geometry()
  local cfg = require("nagare.config").peek
  local cols, lines = vim.o.columns, vim.o.lines - vim.o.cmdheight - 1
  local width = math.max(math.min(cols - 4, math.floor(cols * cfg.width)), 20)
  local height = math.max(math.min(lines - 2, math.floor(lines * cfg.height)), 6)
  local border = cfg.border
  if border == nil and not (vim.fn.exists("+winborder") == 1 and vim.o.winborder ~= "") then
    border = "rounded"
  end
  return {
    relative = "editor",
    width = width,
    height = height,
    col = math.floor((cols - width) / 2),
    row = math.floor((lines - height) / 2),
    style = "minimal",
    border = border,
    zindex = 60,
  }
end

function M.is_peek(win)
  return current ~= nil and current.win == (win or api.nvim_get_current_win())
end

function M.close()
  pcall(api.nvim_del_augroup_by_name, "nagare_peek")
  if current and api.nvim_win_is_valid(current.win) then
    pcall(api.nvim_win_close, current.win, true)
  end
  current = nil
end

local function open(buf, title)
  M.close()
  local cfg = geometry()
  cfg.title = " " .. title .. " "
  cfg.title_pos = "center"
  local win = api.nvim_open_win(buf, true, cfg)
  vim.wo[win].winhighlight = "NormalFloat:Normal,FloatBorder:NagareBorder,FloatTitle:NagareTitle"
  vim.wo[win].winfixbuf = true
  current = { win = win, buf = buf }
  api.nvim_create_autocmd("WinLeave", {
    group = api.nvim_create_augroup("nagare_peek", { clear = true }),
    buffer = buf,
    once = true,
    callback = function()
      -- Deferred: closing a window from inside its own WinLeave is refused.
      vim.schedule(function()
        if current and current.win == win then
          M.close()
        end
      end)
    end,
  })
  return win
end

function M.open(agent)
  local agents = require("nagare.agents")
  agents.touch(agent)
  open(agent.buf, ("%s · %s"):format(agents.label(agent), agent.status:gsub("_", " ")))
  if agent.status ~= "dead" and agent.mode ~= "normal" then
    vim.cmd("startinsert")
  end
end

--- Runs cmd in a throwaway terminal float that closes when it exits.
function M.command(cmd, title)
  local buf = api.nvim_create_buf(false, true)
  local win = open(buf, title)
  local opts = {
    on_exit = function()
      vim.schedule(function()
        if api.nvim_win_is_valid(win) then
          pcall(api.nvim_win_close, win, true)
        end
        pcall(api.nvim_buf_delete, buf, { force = true })
      end)
    end,
  }
  if vim.fn.has("nvim-0.11") == 1 then
    opts.term = true
    vim.fn.jobstart(cmd, opts)
  else
    vim.fn.termopen(cmd, opts)
  end
  vim.cmd("startinsert")
end

return M
