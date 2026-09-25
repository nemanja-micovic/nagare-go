-- A snacks.nvim picker source: `:lua Snacks.picker.nagare()`, or
-- :Nagare pick / <leader>jf. The board is the at-a-glance view; this is the
-- fuzzy one — type part of a project or agent name, see the agent's live
-- screen in the preview, jump or approve without leaving the picker.
local M = {}

local function nagare()
  return require("nagare")
end

---@return snacks.picker.finder.Item[]
function M.finder()
  local items = {}
  for i, e in ipairs(nagare().entries()) do
    table.insert(items, {
      idx = i,
      entry = e,
      text = table.concat({ e.project, e.name, e.kind or "", e.status, e.branch or "", e.worktree or "" }, " "),
    })
  end
  -- Waiting first, the same order the board uses.
  local rank = require("nagare.agents").rank
  table.sort(items, function(a, b)
    local ra, rb = rank[a.entry.status] or 9, rank[b.entry.status] or 9
    if ra ~= rb then
      return ra < rb
    end
    return a.text < b.text
  end)
  return items
end

function M.format(item)
  local e = item.entry
  local n = nagare()
  local util = require("nagare.util")
  return {
    { n.status_icon[e.status] or "?", n.status_hl[e.status] },
    { " " },
    { n.sigil(e.kind), "NagareAgent_" .. (e.kind or "") },
    { " " },
    { e.project, "NagareProject" },
    { "/", "NagareDim" },
    { e.name },
    { "  " },
    { e.status:gsub("_input", ""), n.status_hl[e.status] },
    { e.source == "tmux" and "  tmux" or "", "NagareTmux" },
    { e.worktree and ("  ⎇ " .. e.worktree) or "", "NagareDim" },
    { "  " .. util.oneline(e.last_message, 60), "NagareDim" },
  }
end

--- The agent's screen: the tail of its terminal buffer, or for a tmux
--- agent a capture of its pane.
function M.preview(ctx)
  local e = ctx.item.entry
  local lines
  if e.source == "tmux" then
    lines = vim.fn.systemlist({ "tmux", "capture-pane", "-p", "-t", e.target })
  elseif e.buf and vim.api.nvim_buf_is_valid(e.buf) then
    local n = vim.api.nvim_buf_line_count(e.buf)
    lines = vim.api.nvim_buf_get_lines(e.buf, math.max(0, n - 200), n, false)
    while #lines > 0 and lines[#lines]:match("^%s*$") do
      table.remove(lines)
    end
  else
    lines = { "", ("  %s — %s"):format(e.status == "saved" and "saved from the last session" or e.status,
      e.status == "saved" and "confirm to resume it" or "no screen"), "" }
  end
  ctx.preview:reset()
  ctx.preview:set_lines(lines)
  ctx.preview:set_title(e.project .. "/" .. e.name)
  -- Show the bottom: that is where an agent asks its question.
  pcall(vim.api.nvim_win_set_cursor, ctx.win, { math.max(#lines, 1), 0 })
end

M.source = {
  source = "nagare",
  title = "Agents",
  finder = M.finder,
  format = M.format,
  preview = M.preview,
  confirm = function(picker, item)
    picker:close()
    if item then
      nagare().jump(item.entry)
    end
  end,
  actions = {
    nagare_approve = function(picker, item)
      local e = item and item.entry
      if not e then
        return
      end
      local ok = e.source == "tmux" and require("nagare.tmux").approve(e) or require("nagare.agents").approve(e)
      if ok then
        picker:find() -- refresh statuses
      end
    end,
    nagare_kill = function(picker, item)
      local e = item and item.entry
      if e and e.source == "nvim" then
        local agents = require("nagare.agents")
        if agents.alive(e) then
          agents.kill(e)
        else
          agents.remove(e)
        end
        vim.defer_fn(function()
          picker:find()
        end, 100)
      end
    end,
    nagare_peek = function(picker, item)
      picker:close()
      if item then
        nagare().peek(item.entry)
      end
    end,
  },
  win = {
    input = {
      keys = {
        -- Keys snacks leaves free; <c-x> matches its buffers picker's delete.
        ["<c-y>"] = { "nagare_approve", mode = { "n", "i" }, desc = "Approve" },
        ["<c-o>"] = { "nagare_peek", mode = { "n", "i" }, desc = "Peek" },
        ["<c-x>"] = { "nagare_kill", mode = { "n", "i" }, desc = "Kill / forget" },
      },
    },
  },
}

--- Registers the source when snacks' picker is present. Idempotent.
function M.register()
  local snacks = rawget(_G, "Snacks")
  if not (snacks and pcall(require, "snacks.picker")) then
    return false
  end
  snacks.picker.sources.nagare = M.source
  snacks.picker.sources.nagare_memory = require("nagare.memory").source
  return true
end

return M
