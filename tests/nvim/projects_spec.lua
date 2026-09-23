local projects = require("nagare.projects")
local util = require("nagare.util")

local api = vim.api

return {
  { "describe: worktrees share their main checkout as root", function()
    local repo = git_repo("desc")
    local wt = assert(projects.add_worktree(repo, "topic"))
    local main, linked = util.describe(repo), util.describe(wt)
    eq(main.root, repo)
    eq(main.worktree, nil)
    eq(main.branch, "main")
    eq(linked.root, repo)
    eq(linked.worktree, "topic")
    eq(linked.branch, "topic")
    local plain = SANDBOX .. "/plain"
    vim.fn.mkdir(plain, "p")
    eq(util.describe(plain).root, util.normalize(plain), "non-repo is its own project")
  end },

  { "add_worktree: rejects names git or the shell would mangle", function()
    local repo = git_repo("badnames")
    for _, n in ipairs({ "../escape", "-rf", "a b", "" }) do
      local path, err = projects.add_worktree(repo, n)
      eq(path, nil, n)
      truthy(err, "error for " .. n)
    end
  end },

  { "open: one tab per project, reused on return", function()
    local a, b = git_repo("p-a"), git_repo("p-b")
    local ta, created = projects.open(a)
    truthy(created, "first open creates")
    eq(#api.nvim_list_tabpages(), 1, "untouched start tab reused")
    local tb = projects.open(b)
    eq(#api.nvim_list_tabpages(), 2)
    eq(vim.fn.getcwd(), b)
    local again, created2 = projects.open(a)
    eq(again, ta)
    eq(created2, false)
    eq(vim.fn.getcwd(), a, "tab-local cwd restored")
    truthy(tb ~= ta, "distinct tabs")
  end },

  { "open: runs on_project_open once, in the new tab, only when asked", function()
    local repo, other = git_repo("hook-open"), git_repo("hook-quiet")
    local calls = {}
    require("nagare.config").set("on_project_open", function(root)
      table.insert(calls, { root = root, cwd = vim.fn.getcwd() })
    end)
    projects.open(repo, { explicit = true })
    projects.open(repo, { explicit = true })
    projects.open(other) -- entering a project to show an agent: no picker
    require("nagare.config").set("on_project_open", false)
    eq(calls, { { root = repo, cwd = repo } })
  end },

  { "open: a start screen (LazyVim dashboard) is replaced, not kept", function()
    local repo = git_repo("dash")
    local buf = api.nvim_create_buf(false, true)
    api.nvim_win_set_buf(0, buf)
    vim.bo[buf].filetype = "snacks_dashboard"
    vim.api.nvim_buf_set_lines(buf, 0, -1, false, { "LAZYVIM", "", "f find file" })
    projects.open(repo)
    eq(#api.nvim_list_tabpages(), 1, "no leftover dashboard tab")
    truthy(vim.bo.filetype ~= "snacks_dashboard", "dashboard gone from the window")
    eq(vim.fn.getcwd(), repo)
  end },

  { "lualine component reflects live agents", function()
    local comp = require("nagare").lualine()
    eq(comp.cond(), false, "hidden with nothing live")
    fake_agent({ status = "waiting_input" })
    fake_agent({ status = "running" })
    eq(comp.cond(), true)
    eq(comp[1](), "● 1 ◐ 1")
    eq(comp.color(), "NagareWaiting", "waiting colours the component")
  end },

  { "tab_for: a tab cd'd into by hand is adopted", function()
    local repo = git_repo("manual")
    vim.cmd("tabnew")
    vim.cmd("tcd " .. repo)
    local tab = api.nvim_get_current_tabpage()
    vim.cmd("tabfirst")
    eq(projects.tab_for(repo), tab)
  end },

  { "known + remember: recent projects persist, newest first", function()
    local a, b = git_repo("r-a"), git_repo("r-b")
    projects.remember(a)
    projects.remember(b)
    projects.remember(a)
    projects._reset() -- reload from disk
    local roots = {}
    for _, p in ipairs(projects.known()) do
      if p.recent then
        table.insert(roots, p.root)
      end
    end
    truthy(vim.tbl_contains(roots, a) and vim.tbl_contains(roots, b), vim.inspect(roots))
    local data = vim.json.decode(table.concat(vim.fn.readfile(vim.fn.stdpath("data") .. "/nagare/projects.json"), ""))
    eq(data[1].root, a, "most recent first")
  end },

  { "tmux.parse: tolerates junk and maps saved to idle", function()
    local tmux = require("nagare.tmux")
    eq(tmux.parse("not json"), {})
    eq(tmux.parse(""), {})
    local l = tmux.parse(vim.json.encode({ { name = "x", session = "s", target = "s:0.0", path = "/p", root = "",
      agent = "claude", status = "saved", last_activity = "2026-01-01T00:00:00Z" } }))
    eq(l[1].status, "idle")
    eq(l[1].root, "/p", "empty root falls back to the path")
    eq(l[1].source, "tmux")
    truthy(l[1].changed, "timestamp parsed")
  end },

  { "headless: tab churn with no UI attached does not crash", function()
    -- A detached `nagare-go nvim` runtime has no UI. Neovim 0.11 segfaults on
    -- a later buffer creation once :redrawtabline has run in that state, so
    -- util.redraw must skip it. This sequence crashed the editor before.
    eq(#api.nvim_list_uis(), 0, "tests run headless")
    local a, b = git_repo("churn-a"), git_repo("churn-b")
    projects.open(a)
    projects.open(b)
    projects.open(a)
    vim.cmd("silent! tabonly!")
    vim.cmd("enew!")
    vim.cmd("enew!")
  end },

  { "util.parse_time reads the Go side's UTC timestamps", function()
    eq(util.parse_time("1970-01-02T00:00:00Z"), 86400)
    eq(util.parse_time("garbage"), nil)
  end },
}
