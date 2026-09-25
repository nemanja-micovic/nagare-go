-- What each agent is consuming: context fill, tokens and API-equivalent cost,
-- read by `nagare-go usage` from the agent's own transcript (whose path
-- arrives with every hook). Refreshed in the background after status
-- changes, never from render.
local config = require("nagare.config")

local M = {}

M.by_key = {} -- agent key -> usage table from nagare-go
local pending = {}
local warned = {}

--- Parses `nagare-go usage --json` output for one transcript.
function M.parse(raw, path)
  local data = require("nagare.util").json_decode(raw)
  return type(data) == "table" and data[path] or nil
end

--- Refreshes an agent's usage in the background, at most once per 3s.
function M.refresh(agent)
  local path = agent.transcript
  local bin = config.tmux.bin
  if not path or pending[agent.key] or vim.fn.executable(bin) ~= 1 then
    return
  end
  pending[agent.key] = true
  local key = agent.key
  local chunks = {}
  vim.defer_fn(function()
    vim.fn.jobstart({ bin, "usage", "--json", path }, {
      stdout_buffered = true,
      on_stdout = function(_, data)
        chunks = data
      end,
      on_exit = function()
        vim.schedule(function()
          pending[key] = nil
          local u = M.parse(table.concat(chunks, "\n"), path)
          if not u then
            return
          end
          M.by_key[key] = u
          if (u.context_pct or 0) >= 85 and not warned[key] then
            warned[key] = true
            vim.notify(("nagare: %s is at %d%% of its context — it will compact soon; consider a fresh session for the next task")
              :format(require("nagare.agents").label(agent), u.context_pct), vim.log.levels.WARN,
              { title = "nagare", id = "nagare:context:" .. key })
          end
          pcall(vim.api.nvim_exec_autocmds, "User", { pattern = "NagareReview", modeline = false, data = { key = key } })
        end)
      end,
    })
  end, 3000)
end

function M.get(agent)
  return M.by_key[agent.key]
end

--- "$0.42 38%" for a board row, or "".
function M.label(agent)
  local u = M.by_key[agent.key]
  if not u or (u.turns or 0) == 0 then
    return ""
  end
  local cost = u.known_pricing and ("$%.2f"):format(u.cost_usd) or "$?"
  return ("%s %d%%"):format(cost, u.context_pct or 0)
end

--- Total API-equivalent cost of every agent this editor knows about.
function M.total()
  local sum = 0
  for _, a in ipairs(require("nagare.agents").list) do
    local u = M.by_key[a.key]
    if u and u.known_pricing then
      sum = sum + (u.cost_usd or 0)
    end
  end
  return sum
end

return M
