-- resession.nvim extension: saves which agent splits were open where, and
-- restores them (placeholders for saved agents; see nagare.session).
--   require("resession").setup({ extensions = { nagare = {} } })
local M = {}

function M.on_save()
  return require("nagare.session").snapshot()
end

function M.on_post_load(data)
  vim.schedule(function()
    require("nagare.session").restore(data)
  end)
end

return M
