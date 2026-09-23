local board = require("nagare.board")
local nagare = require("nagare")
local tmux = require("nagare.tmux")
local projects = require("nagare.projects")
local util = require("nagare.util")

local api = vim.api

local function texts(lines)
  local out = {}
  for i, l in ipairs(lines) do
    out[i] = l.text
  end
  return out
end

return {
  { "groups: a waiting agent lifts its whole project", function()
    fake_agent({ root = "/src/alpha", project = "alpha", name = "a1", status = "idle" })
    fake_agent({ root = "/src/zeta", project = "zeta", name = "z1", status = "idle" })
    fake_agent({ root = "/src/zeta", project = "zeta", name = "z2", status = "waiting_input" })
    local g = board.groups(nagare.entries())
    eq(g[1].name, "zeta")
    eq(g[1].entries[1].name, "z2", "waiting first inside the group")
    eq(g[2].name, "alpha")
  end },

  { "groups: tmux agents join the editor's projects", function()
    fake_agent({ root = "/src/api", project = "api", name = "local" })
    tmux.list = tmux.parse(vim.json.encode({
      { name = "api/fix", session = "api", target = "api:2.0", pane_id = "%9", path = "/src/api/.worktrees/fix",
        root = "/src/api", agent = "codex", status = "running", worktree = "fix" },
    }))
    local g = board.groups(nagare.entries())
    eq(#g, 1, "one project")
    eq(#g[1].entries, 2)
    eq(g[1].entries[1].source, "tmux", "running sorts above idle")
    eq(g[1].entries[1].name, "fix", "worktree name, not the tmux display name")
  end },

  { "build: every line fits the board width", function()
    for _, w in ipairs({ 50, 80, 118 }) do
      fake_agent({ root = "/src/p", project = "p", name = "an-extremely-long-agent-name-that-overflows",
        status = "waiting_input", last_message = string.rep("lorem ipsum ", 30), worktree = "some-long-worktree-name" })
      local lines, rows = board.build(w)
      for i, l in ipairs(lines) do
        local dw = vim.fn.strdisplaywidth(l.text)
        if dw > w then
          error(("width %d: line %d is %d cells: %q"):format(w, i, dw, l.text))
        end
      end
      truthy(next(rows), "rows")
      require("nagare.agents")._reset()
    end
  end },

  { "hints: exit key survives any width", function()
    for w = 12, 130, 7 do
      local h = board.hints(w)
      truthy(h:find("q close", 1, true), "q close at width " .. w)
      truthy(vim.fn.strdisplaywidth(h) <= math.max(w, 8), ("width %d: %q"):format(w, h))
    end
  end },

  { "build: rows map lines to entries; highlights stay in bounds", function()
    local a = fake_agent({ root = "/src/p", project = "p", name = "one", status = "running" })
    local lines, rows = board.build(100)
    local found
    for lnum, r in pairs(rows) do
      if r.kind == "agent" then
        found = r
        truthy(lines[lnum].text:find("one", 1, true), "agent row shows its name")
      end
      for _, h in ipairs(lines[lnum].hls) do
        truthy(h[2] >= 0 and h[3] <= #lines[lnum].text, "highlight in bounds")
      end
    end
    eq(found.entry, a)
    truthy(texts(lines)[#lines]:find("jump", 1, true), "hint line last")
  end },

  { "build: project tabs show with no agents", function()
    local repo = git_repo("tabonly")
    projects.open(repo)
    local lines = texts((board.build(100)))
    local joined = table.concat(lines, "\n")
    truthy(joined:find("tabonly", 1, true), "project listed")
    truthy(joined:find("no agents", 1, true), "empty hint")
    truthy(joined:find("tab 1", 1, true), "tab number")
  end },

  { "open: live board re-renders on status change and closes", function()
    local a = fake_agent({ root = "/src/p", project = "p", name = "live", status = "idle" })
    board.open()
    truthy(board.is_open(), "open")
    local buf = api.nvim_get_current_buf()
    eq(vim.bo[buf].filetype, "nagare")
    require("nagare.agents").set_status(a, "waiting_input")
    local text = table.concat(api.nvim_buf_get_lines(buf, 0, -1, false), "\n")
    truthy(text:find("1 waiting", 1, true), "header updated: " .. text)
    local row = api.nvim_win_get_cursor(0)[1]
    truthy(api.nvim_buf_get_lines(buf, row - 1, row, false)[1]:find("live", 1, true), "cursor on the agent")
    board.close()
    eq(board.is_open(), false)
  end },

  { "next_waiting: walks the queue forward and wraps", function()
    local seen = {}
    local orig = nagare.jump
    nagare.jump = function(e)
      table.insert(seen, e.name)
    end
    local ok, err = pcall(function()
      fake_agent({ root = "/src/b", project = "b", name = "b1", status = "waiting_input" })
      fake_agent({ root = "/src/a", project = "a", name = "a1", status = "waiting_input" })
      fake_agent({ root = "/src/a", project = "a", name = "a2", status = "running" })
      fake_agent({ root = "/src/c", project = "c", name = "c1", status = "waiting_input" })
      nagare._last_jump = nil
      for _ = 1, 4 do
        nagare.next_waiting()
      end
    end)
    nagare.jump = orig
    assert(ok, err)
    eq(seen, { "a1", "b1", "c1", "a1" })
  end },

  { "most_urgent: waiting beats the current project's idle", function()
    local repo = git_repo("urgent")
    projects.open(repo)
    fake_agent({ root = repo, project = "urgent", name = "here", status = "idle" })
    fake_agent({ root = "/src/far", project = "far", name = "there", status = "waiting_input" })
    eq(nagare.most_urgent().name, "there")
    fake_agent({ root = repo, project = "urgent", name = "here2", status = "waiting_input", changed = 0 })
    eq(nagare.most_urgent().name, "here2", "same rank: current project wins")
  end },

  { "statusline and tabline summarise live agents", function()
    local repo = git_repo("tabline")
    projects.open(repo)
    fake_agent({ root = repo, project = "tabline", status = "waiting_input" })
    fake_agent({ status = "running" })
    fake_agent({ status = "running" })
    local sl = nagare.statusline()
    truthy(sl:find("● 1", 1, true) and sl:find("◐ 2", 1, true), sl)
    local tl = nagare.tabline()
    truthy(tl:find("tabline", 1, true) and tl:find("NagareWaiting", 1, true), tl)
  end },
}
