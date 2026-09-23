-- :checkhealth nagare
local config = require("nagare.config")

local M = {}

local health = vim.health
local start = health.start or health.report_start
local ok = health.ok or health.report_ok
local warn = health.warn or health.report_warn
local info = health.info or health.report_info
local error = health.error or health.report_error

function M.check()
  start("nagare")
  if vim.fn.has("nvim-0.9") == 1 then
    ok("Neovim " .. tostring(vim.version()))
  else
    error("Neovim 0.9 or newer is required")
  end

  local opts = config.options
  local found = {}
  for name, spec in pairs(opts.agents) do
    if vim.fn.executable(spec.cmd[1]) == 1 then
      table.insert(found, name)
    end
  end
  table.sort(found)
  if #found > 0 then
    ok("agents installed: " .. table.concat(found, ", "))
  else
    warn("no configured agent CLI is in PATH")
  end
  if not opts.agents[opts.default_agent] then
    error(("default_agent %q is not configured"):format(opts.default_agent))
  end

  local bin = opts.tmux.bin
  if vim.fn.executable(bin) == 1 then
    ok(bin .. " found")
    local settings = vim.fn.expand("~/.claude/settings.json")
    local data = vim.fn.filereadable(settings) == 1 and table.concat(vim.fn.readfile(settings), "\n") or ""
    if data:find("hook-state", 1, true) then
      ok("status hooks installed (instant status via " .. opts.states_dir .. ")")
    else
      warn("status hooks not found; run `" .. bin .. " setup` — until then status comes from screen scraping")
    end
  else
    warn(bin .. " not in PATH: no hook status (scraping only) and no tmux agents on the board")
  end

  if require("nagare.status").watching() then
    ok("watching " .. opts.states_dir)
  else
    info("state directory is polled (watcher not running; is setup() called?)")
  end

  if vim.fn.executable("tmux") == 1 then
    ok("tmux found; tmux agents " .. (opts.tmux.enabled and "shown" or "hidden") .. " on the board")
  else
    info("tmux not found; only editor agents are shown")
  end

  local remote = false
  for _, ui in ipairs(vim.api.nvim_list_uis()) do
    if ui.chan and ui.chan > 0 then
      remote = true
    end
  end
  if remote then
    ok("attached to a persistent runtime; :Nagare detach keeps agents running")
  else
    info("not a persistent runtime: agents end when this Neovim exits (start with `nagare-go nvim` to keep them)")
  end
end

return M
