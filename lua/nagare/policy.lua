-- The autonomy dial, from the editor: `.nagare/policy` decides which of an
-- agent's tool calls go through without asking (enforced by nagare-go's
-- PreToolUse hook; see internal/policy).
local M = {}

M.modes = {
  ask = "the agent asks as usual (allow/deny rules still apply)",
  auto = "reads and edits inside the project go through; commands ask unless allowed",
  turbo = "everything goes through except the deny rules",
}

local template = {
  "# How much agents in this repository may do without asking (nagare).",
  "#   ask    the agent asks as usual; allow/deny rules below still apply",
  "#   auto   reads and edits inside the project go through; commands ask unless allowed",
  "#   turbo  everything goes through except what deny catches",
  "# Rules: Tool or Tool(pattern), * matches anything. deny never blocks: it makes a human decide.",
  "mode: %s",
  "allow: Bash(go test *) Bash(npm test*) Bash(git status*) Bash(git diff*)",
  "deny: Bash(rm -rf *) Bash(git push*) Bash(*sudo *)",
}

function M.path(root)
  return root .. "/.nagare/policy"
end

--- The mode in effect for a project, or nil when it has no policy.
function M.mode(root)
  local path = M.path(root)
  if vim.fn.filereadable(path) == 0 then
    return nil
  end
  for _, l in ipairs(vim.fn.readfile(path)) do
    local m = l:match("^%s*mode:%s*(%a+)")
    if m and M.modes[m] then
      return m
    end
  end
  return "ask"
end

--- Sets a project's mode (creating the file from a template), approves it —
--- you chose it, here — or opens it for editing when no mode is given.
function M.set(root, mode)
  local path = M.path(root)
  if not mode then
    if vim.fn.filereadable(path) == 0 then
      vim.fn.mkdir(root .. "/.nagare", "p")
      vim.fn.writefile(vim.tbl_map(function(l)
        return (l:gsub("%%s", "ask"))
      end, template), path)
    end
    vim.cmd("edit " .. vim.fn.fnameescape(path))
    return
  end
  if not M.modes[mode] then
    vim.notify("nagare: mode must be ask, auto or turbo", vim.log.levels.ERROR)
    return
  end
  if vim.fn.filereadable(path) == 0 then
    vim.fn.mkdir(root .. "/.nagare", "p")
    vim.fn.writefile(vim.tbl_map(function(l)
      return (l:gsub("%%s", mode))
    end, template), path)
  else
    local lines = vim.fn.readfile(path)
    local found = false
    for i, l in ipairs(lines) do
      if l:match("^%s*mode:") then
        lines[i], found = "mode: " .. mode, true
      end
    end
    if not found then
      table.insert(lines, 1, "mode: " .. mode)
    end
    vim.fn.writefile(lines, path)
  end
  require("nagare.trust").allow(path)
  vim.notify(("nagare: %s is now %s — %s"):format(vim.fn.fnamemodify(root, ":t"), mode, M.modes[mode]),
    vim.log.levels.INFO)
  pcall(vim.api.nvim_exec_autocmds, "User", { pattern = "NagareStatus", modeline = false, data = {} })
end

return M
