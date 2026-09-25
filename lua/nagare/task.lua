-- Task buffers: write what you want done in a real buffer — with your
-- editor, your snippets, your spell check — and save it to hand it to an
-- agent in a fresh worktree. The first line is the title (and names the
-- worktree); the rest is the brief. Options go on the header lines:
--
--   agent: claude        which agent (default: default_agent)
--   worktree: yes        a fresh worktree (default yes); no = this checkout
--   plan: yes            start in plan mode, so it proposes before it edits
--   count: 3             best-of-N: that many attempts, cycling `agent`s
--
-- Plan mode is the gate the orchestration tools converged on: read the
-- plan, comment on it like a review, then let it build.
local config = require("nagare.config")

local api = vim.api

local M = {}

-- How each agent is started in plan mode.
M.plan_args = {
  claude = { "--permission-mode", "plan" },
}

--- Parses a task buffer into options and the prompt text.
function M.parse(lines)
  local opts = { agent = nil, worktree = true, plan = false, count = 1 }
  local body = {}
  local header = true
  for _, l in ipairs(lines) do
    local k, v = l:match("^(%a+):%s*(.-)%s*$")
    if header and k and ({ agent = 1, worktree = 1, plan = 1, count = 1 })[k] then
      if k == "agent" then
        opts.agent = v
      elseif k == "worktree" then
        opts.worktree = not v:match("^[nf0]")
      elseif k == "plan" then
        opts.plan = v:match("^[yt1]") ~= nil
      elseif k == "count" then
        opts.count = math.max(tonumber(v) or 1, 1)
      end
    elseif header and vim.trim(l) == "" then
      -- blank lines between the header and the title are fine
    else
      header = false
      table.insert(body, l)
    end
  end
  while #body > 0 and vim.trim(body[#body]) == "" do
    table.remove(body)
  end
  local title = body[1] and vim.trim(body[1]:gsub("^#+%s*", "")) or ""
  return opts, title, vim.trim(table.concat(body, "\n"))
end

--- Launches what a task buffer describes. Returns the agents started.
function M.launch(lines, root)
  local nagare = require("nagare")
  local opts, title, prompt = M.parse(lines)
  if prompt == "" then
    return nil, "the task is empty"
  end
  local kinds = opts.agent and vim.split(opts.agent, "%s*,%s*", { trimempty = true }) or { config.default_agent }
  for _, k in ipairs(kinds) do
    if not config.agents[k] then
      return nil, ("unknown agent %q"):format(k)
    end
  end
  local started = {}
  local base = nagare.slug(title ~= "" and title or prompt)
  for i = 1, opts.count do
    local kind = kinds[((i - 1) % #kinds) + 1]
    local args = opts.plan and M.plan_args[kind] or nil
    local a
    if opts.worktree then
      local name = opts.count > 1 and ("%s-%d"):format(base, i) or base
      local path, err = require("nagare.projects").add_worktree(root, name)
      if not path then
        return started, err
      end
      a = nagare.new({ cwd = path, kind = kind, name = name, prompt = prompt, args = args, show = i == 1 })
    else
      a = nagare.new({ cwd = root, kind = kind, prompt = prompt, args = args, show = i == 1 })
    end
    if a then
      a.group = opts.count > 1 and base or nil
      table.insert(started, a)
    end
  end
  return started
end

--- Opens a new task buffer for the current project.
function M.new(root)
  root = root or require("nagare.projects").current_root()
  local buf = api.nvim_create_buf(true, true)
  api.nvim_buf_set_name(buf, ("nagare://task/%s/%d"):format(vim.fn.fnamemodify(root, ":t"), vim.loop.hrtime()))
  vim.bo[buf].buftype = "acwrite"
  vim.bo[buf].filetype = "markdown"
  api.nvim_buf_set_lines(buf, 0, -1, false, {
    "agent: " .. config.default_agent,
    "worktree: yes",
    "plan: no",
    "count: 1",
    "",
    "# Title: what should be done",
    "",
    "Context, constraints, and how you'll know it is done.",
  })
  api.nvim_create_autocmd("BufWriteCmd", {
    buffer = buf,
    callback = function()
      local started, err = M.launch(api.nvim_buf_get_lines(buf, 0, -1, false), root)
      if err then
        vim.notify("nagare: " .. err, vim.log.levels.ERROR)
      end
      if started and #started > 0 then
        vim.bo[buf].modified = false
        vim.notify(("nagare: started %d agent%s on the task"):format(#started, #started == 1 and "" or "s"),
          vim.log.levels.INFO)
        vim.schedule(function()
          pcall(api.nvim_buf_delete, buf, { force = true })
        end)
      end
    end,
  })
  api.nvim_set_current_buf(buf)
  api.nvim_win_set_cursor(0, { 6, 2 })
  return buf
end

return M
