-- :checkhealth nagare
local M = {}

function M.check()
  local h = vim.health
  local config = require("nagare.config")

  h.start("nagare: editor")
  if vim.fn.has("nvim-0.10") ~= 1 then
    h.error("Neovim 0.10 or newer is required")
    return
  end
  h.ok("Neovim " .. tostring(vim.version()))
  if #config.problems == 0 then
    h.ok("configuration is valid")
  else
    for _, p in ipairs(config.problems) do
      h.error("config: " .. p)
    end
  end
  local lazy = package.loaded["lazy.core.config"]
  local spec = lazy and lazy.plugins and (lazy.plugins["nagare"] or lazy.plugins["nagare-go"])
  if spec and spec.lazy and not spec.event and spec.cmd then
    h.warn("lazy.nvim loads nagare only on a command: status and notifications start late. Use event = \"VeryLazy\"")
  end

  h.start("nagare: agents")
  local found, missing = {}, {}
  for name, spec_ in pairs(config.agents) do
    table.insert(vim.fn.executable(spec_.cmd[1]) == 1 and found or missing, name)
  end
  table.sort(found)
  table.sort(missing)
  if #found > 0 then
    h.ok("installed: " .. table.concat(found, ", "))
  else
    h.warn("no configured agent CLI is in PATH")
  end
  if #missing > 0 then
    h.info("not installed: " .. table.concat(missing, ", "))
  end

  h.start("nagare: status")
  local bin = config.tmux.bin
  if vim.fn.executable(bin) == 1 then
    h.ok(bin .. " found")
    local settings = vim.fn.expand("~/.claude/settings.json")
    local data = vim.fn.filereadable(settings) == 1 and table.concat(vim.fn.readfile(settings), "\n") or ""
    if data:find("hook-state", 1, true) then
      h.ok("agent hooks installed: status is instant")
    else
      h.warn("agent hooks not found; run `" .. bin .. " setup`. Until then status comes from reading the screen")
    end
  else
    h.warn(bin .. " not in PATH: status by screen scraping only, and no tmux agents on the board")
  end
  local status = require("nagare.status")
  if status.watching() then
    h.ok("watching " .. config.states_dir)
  else
    h.info("state watcher starts with the first agent")
  end
  if rawget(_G, "Snacks") and Snacks.notifier then
    h.ok("notifications: snacks notifier — one toast per agent, sticky while waiting; history with <leader>n in LazyVim")
  else
    h.info("notifications: vim.notify (install snacks.nvim for persistent, replaceable toasts)")
  end

  h.start("nagare: runtime")
  if vim.fn.executable("tmux") == 1 then
    h.ok("tmux found; tmux agents " .. (config.tmux.enabled and "shown" or "hidden") .. " on the board")
  else
    h.info("tmux not found; only editor agents are shown")
  end
  if require("nagare").persistent() then
    h.ok("persistent runtime (" .. vim.env.NAGARE_RUNTIME .. "): :Nagare detach keeps agents running")
  else
    h.info("agents end when this Neovim exits; `nagare-go nvim` starts one that survives detaching")
  end
end

return M
