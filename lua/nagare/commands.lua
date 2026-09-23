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
