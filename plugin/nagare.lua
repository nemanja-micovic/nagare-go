-- Kept cheap: a command, <Plug> maps, and a scheduled init. Every require
-- happens inside a callback, so startup loads nothing but this file.
-- setup() is optional; configure with it or with vim.g.nagare.
if vim.g.loaded_nagare then
  return
end
vim.g.loaded_nagare = true

if vim.fn.has("nvim-0.10") ~= 1 then
  vim.notify("nagare requires Neovim 0.10 or newer", vim.log.levels.ERROR)
  return
end

vim.api.nvim_create_user_command("Nagare", function(cmd)
  require("nagare.commands").run(cmd)
end, {
  nargs = "*",
  range = true,
  desc = "nagare: agents across projects",
  complete = function(lead, line)
    return require("nagare.commands").complete(lead, line)
  end,
})

local plugs = {
  board = function() require("nagare").board() end,
  toggle = function() require("nagare").toggle() end,
  next = function() require("nagare").next_waiting() end,
  peek = function() require("nagare").peek() end,
  new = function() require("nagare").choose() end,
  worktree = function() require("nagare").worktree() end,
  project = function() require("nagare.projects").pick() end,
  pick = function() require("nagare").pick() end,
  review = function() require("nagare").review() end,
  memory = function() require("nagare.memory").pick() end,
  task = function() require("nagare.task").new() end,
  ["next-review"] = function() require("nagare").next_review() end,
}
for name, fn in pairs(plugs) do
  vim.keymap.set("n", "<Plug>(nagare-" .. name .. ")", fn)
end
for n = 1, 9 do
  vim.keymap.set("n", ("<Plug>(nagare-slot-%d)"):format(n), function()
    require("nagare").slot(n)
  end)
end
vim.keymap.set("n", "<Plug>(nagare-send)", "<Cmd>Nagare send<CR>")
vim.keymap.set("n", "<Plug>(nagare-comment)", "<Cmd>Nagare comment<CR>")
vim.keymap.set("x", "<Plug>(nagare-comment)", ":Nagare comment<CR>", { silent = true })
vim.keymap.set("x", "<Plug>(nagare-send)", ":Nagare send<CR>", { silent = true })

-- After startup (and after a setup() call made while loading, as lazy.nvim
-- does with `opts`), so keymaps and highlights see the final options.
vim.schedule(function()
  require("nagare")._init()
end)
