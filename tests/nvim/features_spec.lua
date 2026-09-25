local agents = require("nagare.agents")
local config = require("nagare.config")
local nagare = require("nagare")
local notify = require("nagare.notify")

local api = vim.api

local function use_fake(script)
  config.agents.fake = { cmd = { "bash", "-c", script, "fake" }, sigil = "F", resume = { "--resume", "{id}" }, continue = { "--continue" } }
end

local function screen(a)
  return table.concat(api.nvim_buf_get_lines(a.buf, 0, -1, false), "\n")
end

-- Captures vim.notify calls (message, level, opts) while fn runs.
local function capture(fn)
  local seen = {}
  local orig = vim.notify
  vim.notify = function(msg, level, opts)
    table.insert(seen, { msg = msg, level = level, opts = opts or {} })
    return (opts or {}).id
  end
  local ok, err = pcall(fn)
  vim.notify = orig
  assert(ok, err)
  return seen
end

-- A stand-in for snacks.nvim's notifier, recording hides.
local function fake_snacks()
  local hidden = {}
  _G.Snacks = { notifier = { hide = function(id) table.insert(hidden, id) end } }
  return hidden
end

return {
  -- Config ------------------------------------------------------------------
  { "config: validation names the problem and its path", function()
    local problems = config.validate(vim.tbl_extend("force", vim.deepcopy(config.defaults), {
      layout = "sideways", nonsense = 1, default_agent = "nobody", size = "big",
    }))
    local text = table.concat(problems, "\n")
    truthy(text:find("layout", 1, true), text)
    truthy(text:find('unknown option "nonsense"', 1, true), text)
    truthy(text:find("default_agent", 1, true), text)
    truthy(text:find("size: expected number", 1, true), text)
    eq(#config.validate(vim.deepcopy(config.defaults)), 0, "defaults are valid")
  end },

  { "config: agents merge, and false drops one", function()
    local saved = vim.g.nagare
    vim.g.nagare = { agents = { gemini = false, claude = { cmd = { "claude", "--model", "opus" } } } }
    local ok, err = pcall(function()
      local opts = config.setup({ keys = false })
      eq(opts.agents.gemini, nil, "dropped")
      eq(opts.agents.claude.cmd, { "claude", "--model", "opus" })
      eq(opts.agents.claude.sigil, "C", "sigil kept from defaults")
      truthy(opts.agents.codex, "others kept")
    end)
    vim.g.nagare = saved
    -- Back to the runner's options.
    config.setup({ keys = false, tmux = { enabled = false }, states_dir = SANDBOX .. "/states", poll_ms = 100,
      on_project_open = false, restore = false, notify = { min_seconds = 0 } })
    assert(ok, err)
  end },

  -- Notifications -------------------------------------------------------------
  { "notify: one sticky toast per agent, cleared when answered", function()
    local hidden = fake_snacks()
    local seen = capture(function()
      local a = fake_agent({ key = "nvim:n:1", status = "running" })
      agents.set_status(a, "waiting_input", { notification_type = "permission_prompt" })
      agents.set_status(a, "running")
    end)
    _G.Snacks = nil
    eq(#seen, 1)
    eq(seen[1].opts.id, "nagare:nvim:n:1", "keyed by agent")
    eq(seen[1].opts.timeout, false, "stays until answered")
    truthy(seen[1].msg:find("permission prompt", 1, true), seen[1].msg)
    eq(hidden, { "nagare:nvim:n:1" }, "answered toast hidden")
  end },

  { "notify: noice's table return value does not stop the hide", function()
    local hidden = fake_snacks()
    local orig = vim.notify
    vim.notify = function()
      return { id = 3 } -- what noice returns
    end
    local ok, err = pcall(function()
      local a = fake_agent({ key = "nvim:n:3", status = "running" })
      agents.set_status(a, "waiting_input")
      agents.set_status(a, "idle")
    end)
    vim.notify = orig
    _G.Snacks = nil
    assert(ok, err)
    eq(hidden, { "nagare:nvim:n:3" })
  end },

  { "notify: finished replaces the waiting toast instead of stacking", function()
    fake_snacks()
    local seen = capture(function()
      local a = fake_agent({ key = "nvim:n:2", status = "running", changed = os.time() - 90 })
      agents.set_status(a, "idle", { last_message = "Refactor done." })
    end)
    _G.Snacks = nil
    eq(#seen, 1)
    eq(seen[1].opts.id, "nagare:nvim:n:2")
    truthy(seen[1].msg:find("Refactor done", 1, true), seen[1].msg)
  end },

  { "notify: plain vim.notify gets no markdown", function()
    _G.Snacks = nil
    local seen = capture(function()
      agents.set_status(fake_agent({ status = "running" }), "waiting_input")
    end)
    eq(seen[1].msg:find("**", 1, true), nil, seen[1].msg)
  end },

  { "notify: a visible agent does not toast", function()
    use_fake("sleep 30")
    local a = assert(agents.spawn({ kind = "fake", cwd = git_repo("visible") }))
    nagare.show(a)
    vim.cmd("stopinsert")
    local seen = capture(function()
      agents.set_status(a, "waiting_input")
    end)
    eq(#seen, 0)
  end },

  { "notify: a crash is reported, a clean exit is not", function()
    local seen = capture(function()
      local a = fake_agent({ status = "running" })
      a.exit_code = 1
      agents.set_status(a, "dead")
      local b = fake_agent({ status = "running" })
      b.exit_code = 0
      agents.set_status(b, "dead")
    end)
    eq(#seen, 1)
    truthy(seen[1].msg:find("exited with code 1", 1, true), seen[1].msg)
  end },

  -- Resume and restore --------------------------------------------------------
  { "resume: a hook's session id picks the session back up", function()
    local a = fake_agent({ kind = "claude" })
    eq(agents.resume_args(a), { "--continue" }, "no id yet: continue latest")
    require("nagare.status").apply({ pane_id = a.key, state = "idle", session_id = "abc-123", timestamp = "2026-01-01T00:00:00Z" })
    eq(a.session_id, "abc-123")
    eq(agents.resume_args(a), { "--resume", "abc-123" })
  end },

  { "resume: a dead agent restarts in place with its session", function()
    use_fake('echo "ARGS:$*"; sleep 30')
    local a = assert(agents.spawn({ kind = "fake", cwd = git_repo("resume") }))
    a.session_id = "s-42"
    agents.kill(a)
    wait_for(3000, function()
      return a.status == "dead"
    end, "dead")
    local old_buf, id = a.buf, a.id
    assert(agents.resume(a))
    eq(a.id, id, "same agent")
    truthy(a.buf ~= old_buf, "fresh terminal")
    eq(api.nvim_buf_is_valid(old_buf), false, "old buffer gone")
    wait_for(3000, function()
      return screen(a):find("ARGS:--resume s-42", 1, true) ~= nil
    end, "resumed with session")
    eq(a.status, "idle")
  end },

  { "restore: last session's agents come back saved, and resume on jump", function()
    use_fake('echo "ARGS:$*"; sleep 30')
    local repo = git_repo("restore")
    local a = assert(agents.spawn({ kind = "fake", cwd = repo, name = "keeper" }))
    a.session_id = "sess-9"
    agents.save()
    agents.kill(a)
    agents._reset()

    eq(agents.restore(), 1)
    local r = agents.list[1]
    eq(r.status, "saved")
    eq(r.name, "keeper")
    eq(r.buf, nil, "nothing started")
    nagare.show(r)
    vim.cmd("stopinsert")
    wait_for(3000, function()
      return r.buf and screen(r):find("ARGS:--resume sess-9", 1, true) ~= nil
    end, "resumed on jump")
  end },

  { "remove: forgets the agent on disk too", function()
    use_fake("sleep 30")
    local a = assert(agents.spawn({ kind = "fake", cwd = git_repo("forget") }))
    agents.remove(a)
    agents._reset()
    eq(agents.restore(), 0)
  end },

  -- Tasks ---------------------------------------------------------------------
  { "tasks: an agent starts on its task through the CLI's prompt argument", function()
    config.agents.fake = { cmd = { "bash", "-c", 'echo "TASK:$1"; sleep 30', "fake" }, sigil = "F", prompt = { "{prompt}" } }
    local a = assert(agents.spawn({ kind = "fake", cwd = git_repo("task"), prompt = "fix the flaky test" }))
    wait_for(3000, function()
      return screen(a):find("TASK:fix the flaky test", 1, true) ~= nil
    end, "task passed as argument")
    eq(a.task, "fix the flaky test")
    eq(agents.prompt_args("claude", "x"), { "x" })
    eq(agents.prompt_args("opencode", "x"), { "--prompt", "x" })
    eq(agents.prompt_args("crush", "x"), {}, "no template: typed in instead")
  end },

  { "tasks: a sentence names its worktree", function()
    eq(nagare.slug("Fix the login redirect on Safari"), "fix-login-redirect-safari")
    eq(nagare.slug("  "):match("^task%-%d+$") ~= nil, true)
    config.agents.fake = { cmd = { "bash", "-c", 'echo "TASK:$1"; sleep 30', "fake" }, sigil = "F", prompt = { "{prompt}" } }
    local repo = git_repo("task-wt")
    require("nagare.projects").open(repo)
    local a = assert(nagare.worktree(nil, "fake", "Add dark mode toggle"))
    eq(a.name, "add-dark-mode-toggle")
    eq(a.worktree, "add-dark-mode-toggle")
    wait_for(3000, function()
      return screen(a):find("TASK:Add dark mode toggle", 1, true) ~= nil
    end, "task reached the worktree agent")
    vim.cmd("stopinsert")
  end },

  -- Terminal ------------------------------------------------------------------
  { "terminal: agents get a clean environment and their own filetype", function()
    use_fake('echo "RT=[${VIMRUNTIME}] NV=[${NVIM}] PANE=[${NAGARE_PANE}]"; sleep 30')
    vim.env.VIMRUNTIME = vim.env.VIMRUNTIME or "/usr/share/nvim/runtime"
    local a = assert(agents.spawn({ kind = "fake", cwd = git_repo("env") }))
    wait_for(3000, function()
      return screen(a):find("RT=[]", 1, true) ~= nil
    end, "VIMRUNTIME removed: " .. screen(a))
    truthy(screen(a):find("NV=[" .. vim.v.servername .. "]", 1, true), "NVIM points back here")
    truthy(screen(a):find("PANE=[" .. a.key .. "]", 1, true), "pane id")
    eq(vim.bo[a.buf].filetype, "nagare_terminal")
  end },

  { "mode: an agent left in normal mode comes back in normal mode", function()
    local a = fake_agent({ buf = api.nvim_get_current_buf() })
    vim.b.nagare_agent = a.id
    local orig = vim.v.event
    agents.track_mode({ buf = a.buf })
    truthy(a.mode == "normal" or a.mode == "terminal", "mode recorded")
    a.leaving = true
    a.mode = "terminal"
    agents.track_mode({ buf = a.buf })
    eq(a.mode, "terminal", "leaving via <C-q> is not reading")
    vim.b.nagare_agent = nil
    local _ = orig
  end },

  -- Commands ------------------------------------------------------------------
  { "commands: completion is sorted and prefix-filtered", function()
    local c = require("nagare.commands")
    local all = c.complete("", "Nagare ")
    local sorted = vim.deepcopy(all)
    table.sort(sorted)
    eq(all, sorted)
    eq(c.complete("p", "Nagare p"), { "peek", "pick", "project" })
    eq(c.complete("co", "Nagare new co"), { "codex" })
    eq(c.complete("", "'<,'>Nagare "), all, "works after a range")
  end },

  { "commands: an unknown subcommand lists the valid ones", function()
    local seen = capture(function()
      vim.cmd("Nagare frobnicate")
    end)
    truthy(seen[1] and seen[1].msg:find("one of: board, ", 1, true), vim.inspect(seen))
  end },

  -- Board ---------------------------------------------------------------------
  { "board: every buffer map has a description; g? lists them", function()
    fake_agent({ status = "idle" })
    local board = require("nagare.board")
    board.open()
    local maps = api.nvim_buf_get_keymap(0, "n")
    local undocumented = {}
    for _, m in ipairs(maps) do
      if not m.desc and not vim.tbl_contains({ "j", "k", "<Down>", "<Up>" }, m.lhs) then
        table.insert(undocumented, m.lhs)
      end
    end
    eq(undocumented, {}, "maps without desc")
    board.help()
    local text = table.concat(api.nvim_buf_get_lines(0, 0, -1, false), "\n")
    truthy(text:find("Resume a dead or saved agent", 1, true), text)
    eq(board.is_open(), true, "a float over the board keeps it open")
    vim.cmd("close")
    board.close()
  end },

  { "board: leaving for a real window closes it", function()
    local board = require("nagare.board")
    vim.cmd("vsplit")
    local other = api.nvim_get_current_win()
    board.open()
    api.nvim_set_current_win(other)
    wait_for(1000, function()
      return not board.is_open()
    end, "closed")
  end },

  { "board: slots number the agents in creation order", function()
    local a = fake_agent({ root = "/src/z", project = "z", name = "first", status = "idle" })
    fake_agent({ root = "/src/a", project = "a", name = "second", status = "waiting_input" })
    local lines = require("nagare.board").build(100)
    local text = {}
    for _, l in ipairs(lines) do
      table.insert(text, l.text)
    end
    local joined = table.concat(text, "\n")
    truthy(joined:find("1 ○ C first", 1, true), joined)
    truthy(joined:find("2 ● C second", 1, true), joined)
    local jumped
    local orig = nagare.jump
    nagare.jump = function(e)
      jumped = e
    end
    nagare.slot(1)
    nagare.jump = orig
    eq(jumped, a)
  end },

  -- Integrations --------------------------------------------------------------
  { "snacks source: items sort waiting first and format with highlights", function()
    local s = require("nagare.snacks")
    fake_agent({ name = "calm", status = "idle" })
    fake_agent({ name = "urgent", status = "waiting_input" })
    local items = s.finder()
    eq(items[1].entry.name, "urgent")
    truthy(items[1].text:find("urgent", 1, true), "searchable text")
    local chunks = s.format(items[1])
    eq(chunks[1][2], "NagareWaiting")
    eq(s.register(), false, "no snacks, no registration")
  end },

  { "keymaps: a <Plug> the user bound is left alone", function()
    vim.keymap.set("n", "<F9>", "<Plug>(nagare-board)")
    nagare.setup({ keys = { board = "<leader>jj" }, tmux = { enabled = false }, states_dir = SANDBOX .. "/states",
      on_project_open = false, restore = false })
    eq(vim.fn.maparg("<leader>jj", "n"), "", "default not added over the user's own mapping")
    vim.keymap.del("n", "<F9>")
    nagare.setup({ keys = { board = "<leader>jj" }, tmux = { enabled = false }, states_dir = SANDBOX .. "/states",
      on_project_open = false, restore = false })
    eq(vim.fn.maparg("<leader>jj", "n"), "<Plug>(nagare-board)")
    pcall(vim.keymap.del, "n", "<leader>jj")
    nagare.setup({ keys = false, tmux = { enabled = false }, states_dir = SANDBOX .. "/states", poll_ms = 100,
      on_project_open = false, restore = false, notify = { min_seconds = 0 } })
  end },
}
