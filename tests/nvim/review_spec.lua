local agents = require("nagare.agents")
local config = require("nagare.config")
local review = require("nagare.review")
local projects = require("nagare.projects")

local api = vim.api

local function sh(dir, ...)
  local out = vim.fn.system(vim.list_extend({ "git", "-C", dir, "-c", "user.email=t@t", "-c", "user.name=t" }, { ... }))
  assert(vim.v.shell_error == 0, out)
  return out
end

-- A repo with a file, and a worktree agent that changed things: one
-- committed edit, one uncommitted edit, one new file.
local function worktree_with_work(name)
  local repo = git_repo(name)
  vim.fn.writefile({ "one", "two", "three" }, repo .. "/app.txt")
  sh(repo, "add", ".")
  sh(repo, "commit", "-q", "-m", "base")
  local wt = assert(projects.add_worktree(repo, "feat"))
  vim.fn.writefile({ "one", "TWO", "three", "four" }, wt .. "/app.txt")
  sh(wt, "commit", "-q", "-am", "agent work")
  vim.fn.writefile({ "ONE", "TWO", "three", "four" }, wt .. "/app.txt")
  vim.fn.writefile({ "new file" }, wt .. "/notes.md")
  config.agents.fake = { cmd = { "bash", "-c", 'while read -r l; do echo "GOT:$l"; done', "fake" }, sigil = "F" }
  local a = assert(agents.spawn({ kind = "fake", cwd = wt, name = "feat" }))
  return repo, wt, a
end

return {
  { "parse: numstat, statuses, renames and untracked files", function()
    local changes = review.parse(
      { "3\t1\tsrc/a.go", "0\t5\told.txt", "2\t0\tlib/{x.lua => y.lua}", "-\t-\tlogo.png" },
      { "M\tsrc/a.go", "D\told.txt", "R087\tlib/x.lua\tlib/y.lua", "A\tlogo.png" },
      { "notes.md" },
      function()
        return 7
      end
    )
    local by = {}
    for _, c in ipairs(changes) do
      by[c.path] = c
    end
    eq(by["src/a.go"].added, 3)
    eq(by["old.txt"].status, "D")
    eq(by["lib/y.lua"].status, "R", "rename resolved to the new path")
    eq(by["logo.png"].added, 0, "binary counts as zero")
    eq(by["notes.md"].status, "?")
    eq(by["notes.md"].added, 7)
  end },

  { "summary: a worktree agent is measured from where it branched", function()
    local _, _, a = worktree_with_work("rv-sum")
    local s = assert(review.summary(a))
    eq(s.files, 2, vim.inspect(s.changes))
    local by = {}
    for _, c in ipairs(s.changes) do
      by[c.path] = c
    end
    eq(by["app.txt"].added, 3, "committed and uncommitted edits together")
    eq(by["notes.md"].status, "?")
    eq(review.label(a), ("+%d −%d"):format(s.added, s.removed))
  end },

  { "summary: a main-checkout agent is measured from HEAD", function()
    local repo = git_repo("rv-main")
    vim.fn.writefile({ "a" }, repo .. "/f.txt")
    sh(repo, "add", ".")
    sh(repo, "commit", "-q", "-m", "f")
    local a = fake_agent({ cwd = repo, root = repo, key = "nvim:rv:main" })
    eq(review.summary(a).files, 0, "clean")
    vim.fn.writefile({ "a", "b" }, repo .. "/f.txt")
    local s = review.summary(a)
    eq(s.files, 1)
    eq(s.base, "HEAD")
  end },

  { "open: review tab with a diff of the first file", function()
    local _, wt, a = worktree_with_work("rv-open")
    local r = assert(review.open(a))
    eq(api.nvim_tabpage_get_var(0, "nagare_review"), a.id)
    eq(vim.wo[r.left_win].diff, true, "left in diff mode")
    eq(vim.wo[r.right_win].diff, true, "right in diff mode")
    eq(api.nvim_buf_get_name(api.nvim_win_get_buf(r.right_win)), wt .. "/app.txt", "agent side is the real file")
    local base_side = api.nvim_buf_get_lines(api.nvim_win_get_buf(r.left_win), 0, -1, false)
    eq(base_side, { "one", "two", "three" }, "base side is the branch point")
    review.step(1)
    eq(r.index, 2)
    eq(api.nvim_buf_get_lines(api.nvim_win_get_buf(r.left_win), 0, -1, false), { "" }, "new file: empty base")
    truthy(not projects.tab_for(a.root) or projects.tab_for(a.root) ~= api.nvim_get_current_tabpage(),
      "a review tab is never taken for the project tab")
    review.close()
    eq(review.current(), nil)
  end },

  { "comments: collected, shown, and sent to the agent as one message", function()
    local _, _, a = worktree_with_work("rv-comment")
    review.open(a)
    review.comment(2, 2, "why uppercase?")
    review.comment(1, 4, "add a test")
    local r = review.current()
    eq(#r.comments, 2)
    local marks = api.nvim_buf_get_extmarks(r.file_buf, -1, 0, -1, { details = true })
    truthy(#marks >= 2, "comments shown inline")
    eq(review.compose(r.comments),
      "Review feedback — please address each point: 1. @app.txt#L2 why uppercase? 2. @app.txt#L1-4 add a test")
    truthy(review.submit(r))
    wait_for(3000, function()
      return table.concat(api.nvim_buf_get_lines(a.buf, 0, -1, false), "\n"):find("GOT:Review feedback", 1, true) ~= nil
    end, "agent received the review")
    eq(#r.comments, 0, "cleared once sent")
    review.close()
  end },

  { "merge: the worktree branch lands on the main branch, uncommitted work included", function()
    local repo, _, a = worktree_with_work("rv-merge")
    local ok, err = review.merge(a, "agent: final touches")
    assert(ok, err)
    eq(vim.fn.readfile(repo .. "/app.txt"), { "ONE", "TWO", "three", "four" })
    eq(vim.fn.filereadable(repo .. "/notes.md"), 1)
    truthy(sh(repo, "log", "--oneline", "-3"):find("Merge feat", 1, true), "merge commit")
  end },

  { "discard: worktree and branch removed, agent forgotten", function()
    local repo, wt, a = worktree_with_work("rv-discard")
    assert(review.discard(a))
    eq(vim.fn.isdirectory(wt), 0)
    eq(vim.fn.system({ "git", "-C", repo, "branch", "--list", "feat" }), "")
    eq(#agents.list, 0)
    eq(vim.fn.readfile(repo .. "/app.txt"), { "one", "two", "three" }, "main untouched")
  end },

  { "merge and discard refuse a main-checkout agent", function()
    local a = fake_agent({ cwd = "/tmp", root = "/tmp" })
    eq(select(1, review.merge(a)), false)
    eq(select(1, review.discard(a)), false)
  end },

  { "board: change counts beside the agent, d reviews", function()
    local _, _, a = worktree_with_work("rv-board")
    review.summary(a)
    local lines = require("nagare.board").build(120)
    local text = {}
    for _, l in ipairs(lines) do
      table.insert(text, l.text)
    end
    local joined = table.concat(text, "\n")
    truthy(joined:find(review.label(a), 1, true), joined)
    local found = false
    for _, k in ipairs(require("nagare.board").keys) do
      found = found or k[1] == "d"
    end
    truthy(found, "d key on the board")
  end },

  { "finished toast says what changed", function()
    local _, _, a = worktree_with_work("rv-toast")
    local seen = {}
    local orig = vim.notify
    vim.notify = function(msg)
      table.insert(seen, msg)
    end
    a.status, a.changed = "running", os.time() - 60
    local ok, err = pcall(agents.set_status, a, "idle")
    vim.notify = orig
    assert(ok, err)
    truthy(seen[1] and seen[1]:find("2 files", 1, true), vim.inspect(seen))
  end },

  { "queue: a settled agent with unseen changes is to review, until reviewed", function()
    local _, _, a = worktree_with_work("rv-queue")
    a.status = "idle"
    review.summary(a)
    truthy(review.needs_review(a), "idle with changes")
    eq(require("nagare").summary().review, 1)
    truthy(require("nagare").statusline():find("◆ 1", 1, true), "statusline shows it")
    local lines = require("nagare.board").build(120)
    local text = {}
    for _, l in ipairs(lines) do
      table.insert(text, l.text)
    end
    truthy(table.concat(text, "\n"):find("review", 1, true), "board marks it")
    local r = require("nagare").next_review()
    eq(r.agent, a, "next_review opens it")
    eq(review.needs_review(a), false, "reviewed")
    review.close()
    vim.fn.writefile({ "changed again" }, a.cwd .. "/notes.md")
    vim.fn.system({ "touch", "-d", "+2 seconds", a.cwd .. "/notes.md" }) -- mtime has 1s resolution
    review.summary(a)
    truthy(review.needs_review(a), "new changes need a new look")
  end },

  { "queue: a running agent is not to review yet", function()
    local _, _, a = worktree_with_work("rv-running")
    a.status = "running"
    review.summary(a)
    eq(review.needs_review(a), false)
  end },

  { "broadcast: every live agent in the project gets the message", function()
    local repo = git_repo("broadcast")
    config.agents.fake = { cmd = { "bash", "-c", 'while read -r l; do echo "GOT:$l"; done', "fake" }, sigil = "F" }
    local a = assert(agents.spawn({ kind = "fake", cwd = repo }))
    local b = assert(agents.spawn({ kind = "fake", cwd = repo }))
    local other = assert(agents.spawn({ kind = "fake", cwd = git_repo("elsewhere") }))
    eq(require("nagare").broadcast("rebase on main", repo), 2)
    for _, x in ipairs({ a, b }) do
      wait_for(3000, function()
        return table.concat(api.nvim_buf_get_lines(x.buf, 0, -1, false), "\n"):find("GOT:rebase on main", 1, true) ~= nil
      end, "broadcast received")
    end
    vim.wait(200)
    eq(table.concat(api.nvim_buf_get_lines(other.buf, 0, -1, false), "\n"):find("GOT:", 1, true), nil, "other project untouched")
  end },

  { "fanout: one task, N worktrees, cycling agents", function()
    local repo = git_repo("fanout")
    config.agents.fake = { cmd = { "bash", "-c", 'echo "TASK:$1"; sleep 30', "fake" }, sigil = "F", prompt = { "{prompt}" } }
    config.agents.fake2 = { cmd = { "bash", "-c", 'echo "TASK2:$1"; sleep 30', "fake2" }, sigil = "G", prompt = { "{prompt}" } }
    projects.open(repo)
    local out = require("nagare").fanout(3, { "fake", "fake2" }, "Speed up the search index")
    eq(#out, 3)
    eq(out[1].name, "speed-up-search-index-1")
    eq(out[2].kind, "fake2")
    eq(out[3].kind, "fake")
    eq(out[1].group, "speed-up-search-index")
    for _, a in ipairs(out) do
      eq(a.root, repo, "all under the same repo")
    end
    vim.cmd("stopinsert")
  end },
}
