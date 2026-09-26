local board = require("nagare.board")
local logo = require("nagare.logo")

local api = vim.api

local function row_text(row)
  local s = ""
  for _, chunk in ipairs(row) do
    s = s .. chunk[1]
  end
  return s
end

return {
  { "rows: every row is the same width, and it is logo.width", function()
    for i, row in ipairs(logo.rows()) do
      eq(vim.fn.strdisplaywidth(row_text(row)), logo.width, "row " .. i)
    end
    truthy(row_text(logo.rows()[3]):find("n a g a r e", 1, true), "the name on the middle row")
  end },

  { "header: plain lines for a dashboard", function()
    local h = logo.header()
    eq(#vim.split(h, "\n"), #logo.rows())
    truthy(not h:find("%s\n"), "no trailing spaces")
  end },

  { "highlights: groups exist and follow the background", function()
    logo.highlights()
    truthy(api.nvim_get_hl(0, { name = "NagareLogoChevron" }).fg, "chevron coloured")
    truthy(api.nvim_get_hl(0, { name = "NagareLogoStream5" }).fg, "streams coloured")
  end },

  { "path: the shipped image", function()
    eq(vim.fn.filereadable(logo.path()), 1, logo.path())
  end },

  { "board: an empty board greets with the logo; agents replace it", function()
    local lines = board.build(100)
    local joined = {}
    for _, l in ipairs(lines) do
      table.insert(joined, l.text)
    end
    truthy(table.concat(joined, "\n"):find("n a g a r e", 1, true), "logo on an empty board")
    fake_agent({ root = "/src/p", project = "p", name = "a1", status = "idle" })
    joined = {}
    for _, l in ipairs((board.build(100))) do
      table.insert(joined, l.text)
    end
    truthy(not table.concat(joined, "\n"):find("n a g a r e", 1, true), "no logo once there is work")
  end },

  { "show: falls back to the text logo without snacks.image", function()
    local win, buf = logo.show()
    truthy(api.nvim_win_is_valid(win), "float open")
    local text = table.concat(api.nvim_buf_get_lines(buf, 0, -1, false), "\n")
    truthy(text:find("n a g a r e", 1, true), "text logo drawn")
    api.nvim_win_close(win, true)
  end },
}
