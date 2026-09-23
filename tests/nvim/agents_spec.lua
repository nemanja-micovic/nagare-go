local agents = require("nagare.agents")
local config = require("nagare.config")
local nagare = require("nagare")

local api = vim.api

-- A stand-in agent CLI: a shell script that behaves like an agent at a
-- permission prompt.
local function use_fake(script)
  config.options.agents.fake = { cmd = { "bash", "-c", script }, sigil = "F" }
end

local function agent_windows()
  local n = 0
  for _, w in ipairs(api.nvim_tabpage_list_wins(0)) do
    if agents.from_buf(api.nvim_win_get_buf(w)) then
      n = n + 1
    end
  end
  return n
end

return {
  { "spawn: terminal buffer carries the agent's pane id", function()
    local repo = git_repo("spawn")
    use_fake('echo "pane=$NAGARE_PANE project=$NAGARE_PROJECT"; sleep 30')
    local a = assert(agents.spawn({ kind = "fake", cwd = repo }))
    eq(vim.bo[a.buf].buftype, "terminal")
    eq(a.root, repo)
    eq(a.name, "fake_01")
    wait_for(3000, function()
      return table.concat(api.nvim_buf_get_lines(a.buf, 0, -1, false), "\n"):find("pane=" .. a.key, 1, true) ~= nil
    end, "env in terminal output")
    truthy(table.concat(api.nvim_buf_get_lines(a.buf, 0, -1, false)):find("project=" .. repo, 1, true), "NAGARE_PROJECT")
    truthy(api.nvim_buf_get_name(a.buf):find("nagare://spawn/fake_01", 1, true), "buffer name")
    eq(vim.bo[a.buf].buflisted, false)
  end },

  { "spawn: unknown and missing agents fail with a reason", function()
    local a, err = agents.spawn({ kind = "nope" })
    eq(a, nil)
    truthy(err:find("unknown agent"), err)
    config.options.agents.ghost = { cmd = { "definitely-not-installed-xyz" } }
    a, err = agents.spawn({ kind = "ghost" })
    eq(a, nil)
    truthy(err:find("not installed"), err)
  end },

  { "scrape: an unhooked agent's prompt is seen as waiting", function()
    use_fake('printf "Do you want to proceed?\\n❯ 1. Yes\\n  2. No\\n"; sleep 30')
    local a = assert(agents.spawn({ kind = "fake", cwd = git_repo("scrape") }))
    wait_for(3000, function()
      return a.status == "waiting_input"
    end, "scraped waiting")
  end },

  { "approve: sends Enter to a waiting agent", function()
    use_fake('printf "Do you want to proceed?\\n❯ 1. Yes\\n"; read -r _; echo APPROVED; sleep 30')
    local a = assert(agents.spawn({ kind = "fake", cwd = git_repo("approve") }))
    wait_for(3000, function()
      return a.status == "waiting_input"
    end, "waiting")
    truthy(agents.approve(a))
    wait_for(3000, function()
      return table.concat(api.nvim_buf_get_lines(a.buf, 0, -1, false), "\n"):find("APPROVED", 1, true) ~= nil
    end, "agent read the approval")
  end },

  { "exit: a finished process is dead and keeps its buffer", function()
    use_fake("echo bye")
    local a = assert(agents.spawn({ kind = "fake", cwd = git_repo("exit") }))
    wait_for(3000, function()
      return a.status == "dead"
    end, "dead")
    truthy(api.nvim_buf_is_valid(a.buf), "buffer kept for reading")
    agents.remove(a)
    eq(#agents.list, 0)
    eq(api.nvim_buf_is_valid(a.buf), false)
  end },

  { "hooks end to end: nagare-go hook-state reaches the plugin", function()
    local bin = vim.env.NAGARE_BIN
    if not bin or vim.fn.executable(bin) ~= 1 then
      io.stdout:write("     (skipped: set NAGARE_BIN to a built nagare-go)\n")
      return
    end
    -- The Go side writes under $HOME; point both at the sandbox.
    local home = SANDBOX .. "/home"
    vim.fn.mkdir(home .. "/.local/share/nagare/states", "p")
    local old_home = vim.env.HOME
    vim.env.HOME = home
    config.options.states_dir = home .. "/.local/share/nagare/states"
    require("nagare.status").start()

    local repo = git_repo("hooked")
    local event = vim.json.encode({
      hook_event_name = "Notification",
      notification_type = "permission_prompt",
      session_id = "e2e-session",
      cwd = repo,
    })
    use_fake(("echo '%s' | %s hook-state; sleep 30"):format(event, vim.fn.shellescape(bin)))
    local ok, err = pcall(function()
      local a = assert(agents.spawn({ kind = "fake", cwd = repo }))
      wait_for(5000, function()
        return a.status == "waiting_input" and a.hooked
      end, "hook status via watcher")
      eq(a.notification_type, "permission_prompt")
    end)
    vim.env.HOME = old_home
    config.options.states_dir = SANDBOX .. "/states"
    require("nagare.status").start()
    assert(ok, err)
  end },

  { "show: agent opens beside code in its project tab, toggle hides it", function()
    local repo = git_repo("show")
    use_fake("sleep 30")
    local a = assert(agents.spawn({ kind = "fake", cwd = repo }))
    nagare.show(a)
    eq(require("nagare.projects").tab_root(api.nvim_get_current_tabpage()), repo)
    eq(vim.fn.getcwd(), repo, "tab-local cwd")
    eq(#api.nvim_tabpage_list_wins(0), 2, "code + agent")
    eq(api.nvim_get_current_buf(), a.buf)
    vim.cmd("stopinsert")

    -- A second agent reuses the agent window instead of splitting again.
    local b = assert(agents.spawn({ kind = "fake", cwd = repo }))
    nagare.show(b)
    eq(#api.nvim_tabpage_list_wins(0), 2, "still two windows")
    eq(b.name, "fake_02")

    nagare.toggle()
    eq(agent_windows(), 0, "hidden")
    eq(#api.nvim_tabpage_list_wins(0), 1)
    nagare.toggle()
    eq(api.nvim_get_current_buf(), b.buf, "toggle brings back the last used")
    vim.cmd("stopinsert")
  end },

  { "leave: returns from the agent to the code window", function()
    local repo = git_repo("leave")
    use_fake("sleep 30")
    local a = assert(agents.spawn({ kind = "fake", cwd = repo }))
    nagare.show(a)
    nagare.leave(a)
    truthy(agents.from_buf(0) == nil, "back in code")
  end },

  { "peek: float over the current file, closes on leave", function()
    use_fake("sleep 30")
    local a = assert(agents.spawn({ kind = "fake", cwd = git_repo("peek") }))
    local code = api.nvim_get_current_win()
    nagare.peek(a)
    local peek = require("nagare.peek")
    truthy(peek.is_peek(), "in the float")
    eq(api.nvim_get_current_buf(), a.buf)
    nagare.leave(a)
    eq(api.nvim_get_current_win(), code, "back where we were")
    eq(peek.is_peek(), false)
  end },

  { "worktree: new branch under .worktrees, agent grouped with its repo", function()
    local repo = git_repo("wt")
    use_fake("sleep 30")
    require("nagare.projects").open(repo)
    local a = assert(nagare.worktree("feature-x", "fake"))
    eq(a.cwd, repo .. "/.worktrees/feature-x")
    eq(a.root, repo, "grouped under the main checkout")
    eq(a.worktree, "feature-x")
    eq(a.name, "feature-x")
    eq(a.branch, "feature-x")
    vim.cmd("stopinsert")
  end },

  { "send: file reference goes to the project's agent", function()
    local repo = git_repo("send")
    use_fake('read -r line; echo "GOT:$line"; sleep 30')
    local a = assert(agents.spawn({ kind = "fake", cwd = repo }))
    require("nagare.projects").open(repo)
    vim.fn.writefile({ "a", "b", "c" }, repo .. "/main.go")
    vim.cmd("edit " .. repo .. "/main.go")
    nagare.send(2, 3, "why?\r")
    wait_for(3000, function()
      return table.concat(api.nvim_buf_get_lines(a.buf, 0, -1, false), "\n"):find("GOT:@main.go#L2-3 why?", 1, true) ~= nil
    end, "reference received")
    vim.cmd("stopinsert")
  end },
}
