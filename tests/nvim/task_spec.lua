local agents = require("nagare.agents")
local config = require("nagare.config")
local review = require("nagare.review")
local task = require("nagare.task")

local api = vim.api

local function screen(a)
  return table.concat(api.nvim_buf_get_lines(a.buf, 0, -1, false), "\n")
end

local function sh(dir, ...)
  local out = vim.fn.system(vim.list_extend({ "git", "-C", dir, "-c", "user.email=t@t", "-c", "user.name=t" }, { ... }))
  assert(vim.v.shell_error == 0, out)
end

return {
  { "parse: header options, then title and brief", function()
    local opts, title, prompt = task.parse({
      "agent: claude, codex", "worktree: yes", "plan: yes", "count: 2", "",
      "# Add rate limiting to the API", "", "Use a token bucket per key.", "",
    })
    eq(opts, { agent = "claude, codex", worktree = true, plan = true, count = 2 })
    eq(title, "Add rate limiting to the API")
    eq(prompt, "# Add rate limiting to the API\n\nUse a token bucket per key.")
    local o2 = task.parse({ "worktree: no", "Fix it" })
    eq(o2.worktree, false)
    eq(select(2, task.parse({ "agent: x", "Title only" })), "Title only")
  end },

  { "launch: writing the buffer starts agents in worktrees named after the title", function()
    config.agents.fake = { cmd = { "bash", "-c", 'echo "ARGS:$*"; sleep 30', "fake" }, sigil = "F", prompt = { "{prompt}" } }
    task.plan_args.fake = { "--plan" }
    local repo = git_repo("task-launch")
    require("nagare.projects").open(repo)
    local buf = task.new(repo)
    api.nvim_buf_set_lines(buf, 0, -1, false, {
      "agent: fake", "worktree: yes", "plan: yes", "count: 2", "", "Speed up cold start", "", "Profile first.",
    })
    vim.cmd("write")
    eq(#agents.list, 2)
    local a, b = agents.list[1], agents.list[2]
    eq(a.name, "speed-up-cold-start-1")
    eq(b.name, "speed-up-cold-start-2")
    eq(a.group, "speed-up-cold-start")
    wait_for(3000, function()
      return screen(a):find("ARGS:--plan Speed up cold start", 1, true) ~= nil
    end, "plan flag and prompt passed: " .. screen(a))
    vim.cmd("stopinsert")
    eq(api.nvim_buf_is_valid(buf), false, "task buffer closes once launched")
  end },

  { "launch: an unknown agent is refused before anything starts", function()
    local started, err = task.launch({ "agent: nobody", "Do it" }, git_repo("task-bad"))
    eq(started, nil)
    truthy(err:find("unknown agent", 1, true), err)
    eq(#agents.list, 0)
  end },

  { "verify: merge is gated on the project's checks", function()
    config.agents.fake = { cmd = { "bash", "-c", 'while read -r l; do echo "GOT:$l"; done', "fake" }, sigil = "F" }
    local repo = git_repo("gate")
    vim.fn.mkdir(repo .. "/.nagare", "p")
    vim.fn.writefile({ "# checks", "test -f ok.txt || { echo 'FAIL: ok.txt missing'; exit 1; }" }, repo .. "/.nagare/verify")
    sh(repo, "add", ".")
    sh(repo, "commit", "-q", "-m", "verify")
    local wt = assert(require("nagare.projects").add_worktree(repo, "gated"))
    local a = assert(agents.spawn({ kind = "fake", cwd = wt, name = "gated" }))
    eq(review.verify_command(a), "test -f ok.txt || { echo 'FAIL: ok.txt missing'; exit 1; }")

    local r = assert(review.open(a))
    local result
    review.run_verify(r, function(ok, out)
      result = { ok = ok, out = out }
    end)
    wait_for(5000, function()
      return result ~= nil
    end, "verify finished")
    eq(result.ok, false)
    truthy(table.concat(result.out, "\n"):find("FAIL: ok.txt missing", 1, true), vim.inspect(result.out))

    r.verify_output = result.out
    truthy(review.send_failures(r))
    wait_for(3000, function()
      return screen(a):find("GOT:The project's checks fail", 1, true) ~= nil
    end, "failures reached the agent")

    vim.fn.writefile({ "fixed" }, wt .. "/ok.txt")
    result = nil
    review.run_verify(r, function(ok)
      result = ok
    end)
    wait_for(5000, function()
      return result ~= nil
    end, "second run")
    eq(result, true)
    review.close()
  end },

  { "board: a waiting agent's row names what it asks for", function()
    fake_agent({ status = "waiting_input", name = "asker", last_tool = "Bash: rm -rf build" })
    local lines = require("nagare.board").build(140)
    local text = {}
    for _, l in ipairs(lines) do
      table.insert(text, l.text)
    end
    truthy(table.concat(text, "\n"):find("Bash: rm -rf build", 1, true), table.concat(text, "\n"))
  end },
}
