-- :Nagare subcommands. Each is { impl = fn(args, cmd), complete = fn(lead) }.
local M = {}

local function nagare()
  return require("nagare")
end

local function filter(items, lead)
  local out = vim.tbl_filter(function(s)
    return s:sub(1, #lead) == lead
  end, items)
  table.sort(out)
  return out
end

local function agent_kinds(lead)
  return filter(vim.tbl_keys(require("nagare.config").agents), lead)
end

local function dirs(lead)
  return vim.fn.getcompletion(lead, "dir")
end

local function here()
  local agent = require("nagare.agents").from_buf(0)
  if not agent then
    vim.notify("nagare: run this from an agent's terminal", vim.log.levels.WARN)
  end
  return agent
end

---@type table<string, { impl: fun(args: string[], cmd: table), complete?: fun(lead: string, args: string[]): string[] }>
M.subcommands = {
  board = { impl = function() nagare().board() end },
  pick = { impl = function() nagare().pick() end },
  next = { impl = function() nagare().next_waiting() end },
  peek = { impl = function() nagare().peek() end },
  toggle = { impl = function() nagare().toggle() end },
  new = {
    impl = function(args)
      local util = require("nagare.util")
      nagare().new({ kind = args[1], cwd = args[2] and util.normalize(args[2]) or nil })
    end,
    complete = function(lead, args)
      if #args <= 1 then
        return agent_kinds(lead)
      end
      return dirs(lead)
    end,
  },
  worktree = {
    impl = function(args)
      nagare().worktree(args[1], args[2])
    end,
    complete = function(lead, args)
      return #args == 2 and agent_kinds(lead) or {}
    end,
  },
  project = {
    impl = function(args)
      local util = require("nagare.util")
      if args[1] then
        require("nagare.projects").open(util.describe(util.normalize(args[1])).root, { explicit = true })
      else
        require("nagare.projects").pick()
      end
    end,
    complete = dirs,
  },
  send = {
    impl = function(args, cmd)
      nagare().send(cmd.line1, cmd.line2, #args > 0 and table.concat(args, " ") or nil)
    end,
  },
  review = { impl = function() nagare().review() end },
  ["next-review"] = { impl = function() nagare().next_review() end },
  task = { impl = function() require("nagare.task").new() end },
  memory = {
    -- :Nagare memory [new | <query>]
    impl = function(args)
      local mem = require("nagare.memory")
      if args[1] == "new" then
        mem.new()
      elseif #args > 0 then
        local root = require("nagare.projects").current_root()
        local items = mem.list(root, table.concat(args, " "))
        vim.ui.select(items, { prompt = "Memory", kind = "nagare_memory", format_item = mem.format_line }, function(m)
          if m then
            mem.open(m)
          end
        end)
      else
        mem.pick()
      end
    end,
    complete = function(lead)
      return vim.startswith("new", lead) and { "new" } or {}
    end,
  },
  verify = {
    -- :Nagare verify [command] — create or edit the project's .nagare/verify
    impl = function(args)
      local root = require("nagare.projects").current_root()
      local path = root .. "/.nagare/verify"
      if #args > 0 then
        vim.fn.mkdir(root .. "/.nagare", "p")
        vim.fn.writefile({ table.concat(args, " ") }, path)
        vim.notify("nagare: agents in " .. vim.fn.fnamemodify(root, ":t") .. " must pass `" .. table.concat(args, " ")
          .. "` before they stop", vim.log.levels.INFO)
        return
      end
      if vim.fn.filereadable(path) == 0 then
        vim.fn.mkdir(root .. "/.nagare", "p")
        vim.fn.writefile({ "# The command agents must pass before they can stop, e.g.:", "# go test ./..." }, path)
      end
      vim.cmd("edit " .. vim.fn.fnameescape(path))
    end,
  },
  broadcast = {
    impl = function(args)
      if #args == 0 then
        vim.notify("nagare: :Nagare broadcast <text>", vim.log.levels.WARN)
        return
      end
      nagare().broadcast(table.concat(args, " "))
    end,
  },
  fanout = {
    -- :Nagare fanout 3 claude,codex Add rate limiting to the API
    impl = function(args)
      local n = tonumber(args[1])
      if not n then
        vim.notify("nagare: :Nagare fanout <n> [agent,agent] <task>", vim.log.levels.WARN)
        return
      end
      local kinds = {}
      local rest = 2
      if args[2] and args[2]:match("^[%w,_-]+$") then
        local cfg = require("nagare.config").agents
        local candidate = vim.split(args[2], ",", { trimempty = true })
        if #vim.tbl_filter(function(k) return cfg[k] ~= nil end, candidate) == #candidate then
          kinds, rest = candidate, 3
        end
      end
      nagare().fanout(n, kinds, table.concat(vim.list_slice(args, rest), " "))
    end,
  },
  comment = {
    impl = function(args, cmd)
      require("nagare.review").comment(cmd.line1, cmd.line2, #args > 0 and table.concat(args, " ") or nil)
    end,
  },
  slot = {
    impl = function(args)
      nagare().slot(tonumber(args[1]) or 1)
    end,
  },
  rename = {
    impl = function(args)
      local agent = here()
      if agent and args[1] then
        require("nagare.agents").rename(agent, args[1])
      end
    end,
  },
  resume = {
    impl = function()
      local agent = here()
      if agent then
        local ok, err = require("nagare.agents").resume(agent)
        if not ok then
          vim.notify("nagare: " .. err, vim.log.levels.ERROR)
        end
      end
    end,
  },
  detach = { impl = function() nagare().detach() end },
}

function M.run(cmd)
  local args = vim.split(cmd.args, "%s+", { trimempty = true })
  local name = table.remove(args, 1) or "board"
  local sub = M.subcommands[name]
  if not sub then
    local names = vim.tbl_keys(M.subcommands)
    table.sort(names)
    vim.notify(("nagare: unknown subcommand %q; one of: %s"):format(name, table.concat(names, ", ")), vim.log.levels.ERROR)
    return
  end
  sub.impl(args, cmd)
end

function M.complete(lead, line)
  local words = vim.split(line:gsub("^%S*Nagare!?%s*", ""), "%s+", { trimempty = false })
  if #words <= 1 then
    return filter(vim.tbl_keys(M.subcommands), lead)
  end
  local sub = M.subcommands[words[1]]
  if sub and sub.complete then
    return sub.complete(lead, vim.list_slice(words, 2))
  end
  return {}
end

return M
