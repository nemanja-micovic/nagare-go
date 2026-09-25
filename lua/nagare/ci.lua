-- The CI loop: once an agent's branch has a pull request, watch its checks
-- and bring failures back to the agent that wrote the code. Polls `gh` only
-- while some agent has an open PR, and only as often as CI changes.
local config = require("nagare.config")

local M = {}

M.state = {} -- agent key -> { branch, number, url, checks = {...}, summary = "pass"|"fail"|"pending"|"merged"|"closed" }
local timer

local function gh()
  return config.gh or "gh"
end

local function available()
  return vim.fn.executable(gh()) == 1
end

--- Runs gh in the agent's directory (gh finds the repository from there)
--- and decodes its JSON output; nil on failure.
local function run_gh(args, cwd)
  local cmd = { "sh", "-c", 'cd "$1" && shift && exec "$@"', "sh", cwd, gh() }
  vim.list_extend(cmd, args)
  local out = vim.fn.systemlist(cmd)
  if vim.v.shell_error ~= 0 then
    return nil
  end
  return require("nagare.util").json_decode(table.concat(out, "\n"))
end

--- Summarises `gh pr checks --json name,bucket,link`: fail beats pending
--- beats pass.
function M.summarize(checks)
  local failing, pending = {}, 0
  for _, c in ipairs(checks or {}) do
    if c.bucket == "fail" or c.bucket == "cancel" then
      table.insert(failing, c)
    elseif c.bucket == "pending" then
      pending = pending + 1
    end
  end
  if #failing > 0 then
    return "fail", failing
  end
  if pending > 0 then
    return "pending", failing
  end
  return (#(checks or {}) > 0) and "pass" or "pending", failing
end

--- Starts tracking an agent's PR (called after P, or when one is found).
function M.track(agent)
  local branch = require("nagare.util").describe(agent.cwd).branch
  if not branch or not available() then
    return false
  end
  local pr = run_gh({ "pr", "view", branch, "--json", "number,url,state" }, agent.cwd)
  if not pr or not pr.number then
    return false
  end
  M.state[agent.key] = { branch = branch, number = pr.number, url = pr.url, summary = "pending", checks = {} }
  M.start()
  return true
end

--- One poll of one agent's PR.
function M.poll(agent)
  local s = M.state[agent.key]
  if not s then
    return
  end
  local pr = run_gh({ "pr", "view", s.branch, "--json", "number,url,state" }, agent.cwd)
  if pr and (pr.state == "MERGED" or pr.state == "CLOSED") then
    s.summary = pr.state:lower()
    vim.notify(("nagare: PR #%d for %s was %s"):format(s.number, require("nagare.agents").label(agent), s.summary),
      vim.log.levels.INFO, { title = "nagare", id = "nagare:ci:" .. agent.key })
    M.state[agent.key] = s.summary == "merged" and s or nil
    return
  end
  local checks = run_gh({ "pr", "checks", s.branch, "--json", "name,bucket,link" }, agent.cwd) or {}
  local before = s.summary
  s.checks = checks
  local summary, failing = M.summarize(checks)
  s.summary, s.failing = summary, failing
  if summary ~= before then
    if summary == "fail" then
      local names = vim.tbl_map(function(c)
        return c.name
      end, failing)
      vim.notify(("✗ CI failed for %s: %s — F on the board sends the logs to the agent")
        :format(require("nagare.agents").label(agent), table.concat(names, ", ")), vim.log.levels.ERROR,
        { title = "nagare", id = "nagare:ci:" .. agent.key })
    elseif summary == "pass" then
      vim.notify(("✓ CI passed for %s (PR #%d)"):format(require("nagare.agents").label(agent), s.number),
        vim.log.levels.INFO, { title = "nagare", id = "nagare:ci:" .. agent.key })
    end
    pcall(vim.api.nvim_exec_autocmds, "User", { pattern = "NagareReview", modeline = false, data = { key = agent.key } })
  end
end

function M.poll_all()
  local agents = require("nagare.agents")
  local any = false
  for key, s in pairs(M.state) do
    local agent = agents.by_key[key]
    if agent and s.summary ~= "merged" then
      any = true
      M.poll(agent)
    end
  end
  if not any then
    M.stop()
  end
end

function M.start()
  if timer then
    return
  end
  timer = vim.uv.new_timer()
  local every = (config.ci_poll_ms or 60000)
  timer:start(every, every, vim.schedule_wrap(M.poll_all))
end

function M.stop()
  if timer then
    timer:stop()
    timer:close()
    timer = nil
  end
end

--- "CI✓", "CI✗", "CI…", "merged" or "" for a board row.
function M.label(agent)
  local s = M.state[agent.key]
  if not s then
    return ""
  end
  return ({ pass = "CI✓", fail = "CI✗", pending = "CI…", merged = "merged" })[s.summary] or ""
end

--- The run id in a check's link (…/actions/runs/<id>/job/<job>).
function M.run_id(link)
  return link and link:match("/actions/runs/(%d+)") or nil
end

--- Sends the failing checks' logs to the agent to fix and push.
function M.send_failures(agent)
  local s = M.state[agent.key]
  if not s or s.summary ~= "fail" then
    vim.notify("nagare: no failing CI for " .. agent.name, vim.log.levels.INFO)
    return false
  end
  local parts = {}
  for _, c in ipairs(s.failing or {}) do
    local log = ""
    local run = M.run_id(c.link)
    if run then
      local out = vim.fn.systemlist({ gh(), "run", "view", run, "--log-failed" })
      if vim.v.shell_error == 0 then
        log = table.concat(vim.list_slice(out, math.max(1, #out - 60)), " | ")
      end
    end
    table.insert(parts, ("%s failed%s"):format(c.name, log ~= "" and (": " .. log) or (" (" .. (c.link or "") .. ")")))
  end
  local agents = require("nagare.agents")
  if not agents.alive(agent) then
    local ok, err = agents.resume(agent)
    if not ok then
      vim.notify("nagare: " .. err, vim.log.levels.ERROR)
      return false
    end
  end
  local text = ("CI failed on PR #%d. %s — please fix and push."):format(s.number, table.concat(parts, " / "))
  agents.send(agent, text:sub(1, 8000) .. "\r")
  vim.notify("nagare: sent CI failures to " .. agents.label(agent), vim.log.levels.INFO)
  return true
end

return M
