-- The nagare mark, for the editor: five agent streams flowing into
-- `[nagare]❯`, drawn in text so it shows in any terminal. It greets you on an
-- empty board and titles the board window; `:Nagare logo` shows the real
-- image (images/nagare-logo-glowing.jpg) through snacks.image when the
-- terminal speaks the kitty graphics protocol, and this text version when it
-- does not. Colours are the logo's own — teal streams fading to violet, a
-- magenta chevron — as `default` groups, so a colorscheme can restyle them.
local api = vim.api

local M = {}

-- Sampled from the logo, with darker twins so it stays legible on a light
-- background.
local palette = {
  dark = {
    streams = { "#6ef2e4", "#6fd0f0", "#78aef2", "#7c8ff0", "#8672ea" },
    bracket = "#8b5cf6",
    word = "#6ef7e8",
    chevron = "#e75ad8",
  },
  light = {
    streams = { "#0f9488", "#0e7fa8", "#2563c9", "#4f52d8", "#6d3fd6" },
    bracket = "#7c3aed",
    word = "#0d8078",
    chevron = "#c026b3",
  },
}

function M.highlights()
  local p = palette[vim.o.background == "light" and "light" or "dark"]
  for i, c in ipairs(p.streams) do
    api.nvim_set_hl(0, "NagareLogoStream" .. i, { fg = c, default = true })
  end
  api.nvim_set_hl(0, "NagareLogoBracket", { fg = p.bracket, bold = true, default = true })
  api.nvim_set_hl(0, "NagareLogoWord", { fg = p.word, bold = true, default = true })
  api.nvim_set_hl(0, "NagareLogoChevron", { fg = p.chevron, bold = true, default = true })
end

-- Each row is the stream (entering from the left: the outer two curve into
-- the inner ones, as in the image), then the bracket column, then the chevron.
local streams = {
  "   ○──╮    ",
  " ●────╰────",
  "   ○───────",
  " ●────╭────",
  "   ○──╯    ",
}
local inner = 17
local brackets = {
  { "┏━", (" "):rep(inner - 2), "━┓" },
  { "┃", (" "):rep(inner), "┃" },
  { "┃", "   n a g a r e   ", "┃" },
  { "┃", (" "):rep(inner), "┃" },
  { "┗━", (" "):rep(inner - 2), "━┛" },
}

--- The logo as rows of { text, hl_group } chunks, all the same width.
---@return {[1]: string, [2]: string?}[][]
function M.rows()
  local out = {}
  for i = 1, #streams do
    local b = brackets[i]
    table.insert(out, {
      { streams[i], "NagareLogoStream" .. i },
      { " " },
      { b[1], "NagareLogoBracket" },
      { b[2], i == 3 and "NagareLogoWord" or nil },
      { b[3], "NagareLogoBracket" },
      { i == 3 and "  ❯" or "   ", "NagareLogoChevron" },
    })
  end
  return out
end

--- The logo as plain lines, e.g. for a dashboard header:
---   opts.dashboard.preset.header = require("nagare.logo").header()
---@return string
function M.header()
  local lines = {}
  for _, row in ipairs(M.rows()) do
    local text = ""
    for _, chunk in ipairs(row) do
      text = text .. chunk[1]
    end
    table.insert(lines, (text:gsub("%s+$", "")))
  end
  return table.concat(lines, "\n")
end

M.width = vim.fn.strdisplaywidth(streams[1]) + 1 + inner + 2 + 3

--- The one-line mark, as float title chunks.
function M.title()
  return {
    { " [", "NagareLogoBracket" },
    { "nagare", "NagareLogoWord" },
    { "]", "NagareLogoBracket" },
    { "❯ ", "NagareLogoChevron" },
  }
end

--- The logo image shipped with the plugin.
function M.path()
  local src = debug.getinfo(1, "S").source:sub(2)
  return vim.fn.fnamemodify(src, ":p:h:h:h") .. "/images/nagare-logo-glowing.jpg"
end

local function image_supported(path)
  local snacks = rawget(_G, "Snacks")
  if not (snacks and snacks.image and vim.fn.filereadable(path) == 1) then
    return false
  end
  local ok, yes = pcall(snacks.image.supports, path)
  return ok and yes
end

--- Shows the logo in a centred float: the real image where the terminal can
--- draw it, the text mark otherwise. q or <Esc> closes it.
function M.show()
  local path = M.path()
  local image = image_supported(path)
  -- The image is 1376x768; terminal cells are about twice as tall as wide.
  local width = image and math.min(vim.o.columns - 8, 72) or M.width + 4
  local height = image and math.floor(width * 768 / 1376 / 2) + 1 or #streams + 2
  local buf = api.nvim_create_buf(false, true)
  vim.bo[buf].bufhidden = "wipe"
  local win = api.nvim_open_win(buf, true, {
    relative = "editor", style = "minimal", border = "rounded", zindex = 60,
    width = width, height = height,
    row = math.max(math.floor((vim.o.lines - height) / 2) - 1, 0),
    col = math.floor((vim.o.columns - width) / 2),
    title = M.title(), title_pos = "center",
  })
  vim.wo[win].winhighlight = "NormalFloat:NagareNormal,FloatBorder:NagareBorder"
  local drawn = false
  if image then
    drawn = pcall(function()
      Snacks.image.placement.new(buf, path, { pos = { 1, 0 }, width = width, max_height = height })
    end)
  end
  if not drawn then
    M.render(buf, 1, 2, height)
  end
  for _, lhs in ipairs({ "q", "<Esc>" }) do
    vim.keymap.set("n", lhs, function()
      pcall(api.nvim_win_close, win, true)
    end, { buffer = buf, nowait = true })
  end
  return win, buf
end

--- Writes the text logo into `buf` starting at `row` (0-based), indented by
--- `col` cells, padding the buffer to `height` lines.
function M.render(buf, row, col, height)
  local ns = api.nvim_create_namespace("nagare_logo")
  local rows = M.rows()
  local text = {}
  for i = 1, height or (#rows + row) do
    text[i] = ""
  end
  for i, r in ipairs(rows) do
    local s = (" "):rep(col)
    for _, chunk in ipairs(r) do
      s = s .. chunk[1]
    end
    text[row + i] = s
  end
  vim.bo[buf].modifiable = true
  api.nvim_buf_set_lines(buf, 0, -1, false, text)
  vim.bo[buf].modifiable = false
  for i, r in ipairs(rows) do
    local at = col
    for _, chunk in ipairs(r) do
      if chunk[2] then
        api.nvim_buf_set_extmark(buf, ns, row + i - 1, at, { end_col = at + #chunk[1], hl_group = chunk[2] })
      end
      at = at + #chunk[1]
    end
  end
end

return M
