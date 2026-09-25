-- Approving a repository's .nagare files from the editor. They act only
-- once approved (see internal/trust): a cloned repository must not be able
-- to run commands or approve tool calls on your behalf.
local config = require("nagare.config")

local M = {}

M.files = { ".nagare/verify", ".nagare/policy" }

local function bin()
  return config.tmux.bin
end

function M.allow(file)
  if vim.fn.executable(bin()) ~= 1 then
    return false
  end
  vim.fn.system({ bin(), "trust", "--quiet", file })
  return vim.v.shell_error == 0
end

function M.trusted(file)
  if vim.fn.executable(bin()) ~= 1 then
    return false
  end
  vim.fn.system({ bin(), "trust", "--check", "--quiet", file })
  return vim.v.shell_error == 0
end

--- The repository's .nagare files that are present but not approved.
function M.pending(root)
  local out = {}
  for _, name in ipairs(M.files) do
    local path = root .. "/" .. name
    if vim.fn.filereadable(path) == 1 and not M.trusted(path) then
      table.insert(out, path)
    end
  end
  return out
end

--- Shows each unapproved file's contents and asks, one at a time.
function M.prompt(root)
  local pending = M.pending(root)
  if #pending == 0 then
    vim.notify("nagare: nothing to approve in " .. vim.fn.fnamemodify(root, ":t"), vim.log.levels.INFO)
    return
  end
  local function ask(i)
    local file = pending[i]
    if not file then
      return
    end
    local body = table.concat(vim.fn.readfile(file), "\n")
    vim.ui.select({ "Approve", "Not now" }, {
      prompt = ("%s:\n%s\n\nApprove this?"):format(vim.fn.fnamemodify(file, ":~:."), body),
      kind = "nagare_trust",
    }, function(choice)
      if choice == "Approve" and M.allow(file) then
        vim.notify("nagare: approved " .. file, vim.log.levels.INFO)
      end
      ask(i + 1)
    end)
  end
  ask(1)
end

--- Saving a .nagare file in your own editor approves it: you wrote it.
function M.setup_autocmd(group)
  vim.api.nvim_create_autocmd("BufWritePost", {
    group = group,
    pattern = { "*/.nagare/verify", "*/.nagare/policy" },
    callback = function(ev)
      if M.allow(ev.match) then
        vim.notify("nagare: approved " .. vim.fn.fnamemodify(ev.match, ":~:."), vim.log.levels.INFO)
      end
    end,
  })
end

return M
