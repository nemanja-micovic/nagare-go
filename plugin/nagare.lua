-- Loaded at startup. Defines :Nagare so the plugin works without an explicit
-- setup() call; the first use runs setup() with defaults. Calling
-- require("nagare").setup({...}) yourself replaces this command.
if vim.g.loaded_nagare then
  return
end
vim.g.loaded_nagare = true

if vim.fn.has("nvim-0.9") ~= 1 then
  vim.api.nvim_err_writeln("nagare requires Neovim 0.9 or newer")
  return
end

-- init.lua runs before plugin/: a user who already called setup() has the
-- real command, and replacing it with this stub would make it call itself.
if vim.fn.exists(":Nagare") == 2 then
  return
end

vim.api.nvim_create_user_command("Nagare", function(cmd)
  local nagare = require("nagare")
  if not nagare._setup_done then
    nagare.setup()
  end
  local range = cmd.range > 0 and (cmd.line1 .. "," .. cmd.line2) or ""
  vim.cmd(range .. "Nagare " .. cmd.args)
end, { nargs = "*", range = true, desc = "nagare: agents across projects" })
