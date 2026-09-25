local config = require("nagare.config")
local memory = require("nagare.memory")

local api = vim.api

-- Runs fn with nagare-go pointed at a private HOME, so memories written by
-- the test never touch the user's.
local function with_bin(fn)
  local bin = vim.env.NAGARE_BIN
  if not bin or vim.fn.executable(bin) ~= 1 then
    io.stdout:write("     (skipped: set NAGARE_BIN to a built nagare-go)\n")
    return
  end
  local home = SANDBOX .. "/memhome-" .. vim.loop.hrtime()
  vim.fn.mkdir(home, "p")
  local old_home, old_tmux = vim.env.HOME, vim.deepcopy(config.tmux)
  vim.env.HOME = home
  config.set("tmux", { enabled = false, bin = bin, poll_ms = 4000 })
  local ok, err = pcall(fn, bin, home)
  vim.env.HOME = old_home
  config.set("tmux", old_tmux)
  memory.stop()
  assert(ok, err)
end

return {
  { "memory: write one in a buffer, find it, archive it", function()
    with_bin(function()
      local repo = git_repo("mem-roundtrip")
      require("nagare.projects").open(repo)
      eq(#memory.list(repo), 0, "empty at first")

      memory.new(repo)
      api.nvim_buf_set_lines(0, 0, -1, false, {
        "kind: convention",
        "scope: project",
        "",
        "Always run make lint before pushing",
        "",
        "CI rejects unformatted code and the error is cryptic.",
      })
      vim.cmd("write")
      local items = memory.list(repo)
      eq(#items, 1)
      eq(items[1].title, "Always run make lint before pushing")
      eq(items[1].kind, "convention")
      eq(api.nvim_buf_get_name(0), items[1].file, "saving opens the stored file")

      local hits = memory.list(repo, "lint pushing")
      eq(#hits, 1, "search")
      truthy(memory.archive(items[1]))
      eq(#memory.list(repo), 0, "archived memories leave the list")
    end)
  end },

  { "memory: a near-duplicate is refused with the existing one named", function()
    with_bin(function()
      local repo = git_repo("mem-dup")
      local seen = {}
      local orig = vim.notify
      vim.notify = function(msg)
        table.insert(seen, msg)
      end
      local ok, err = pcall(function()
        for _ = 1, 2 do
          memory.new(repo)
          api.nvim_buf_set_lines(0, 0, -1, false, { "kind: gotcha", "", "The staging database resets every night at midnight" })
          vim.cmd("write")
        end
      end)
      vim.notify = orig
      assert(ok, err)
      truthy(seen[#seen]:find("similar memory exists", 1, true), vim.inspect(seen))
      eq(#memory.list(repo), 1)
      vim.cmd("bwipeout!")
    end)
  end },

  { "memory: an agent's new memory is announced once", function()
    with_bin(function()
      local repo = git_repo("mem-watch")
      config.set("notify", { waiting = true, finished = true, min_seconds = 0, memory = true })
      memory.watch(repo)
      local dir = memory.dir(repo)
      local seen = {}
      local orig = vim.notify
      vim.notify = function(msg, _, opts)
        table.insert(seen, { msg = msg, id = opts and opts.id })
      end
      local ok, err = pcall(function()
        vim.fn.writefile({ "---", "id: mabcde", "kind: gotcha", "author: claude@api", "---",
          "Flaky test: TestLogin depends on wall clock" }, dir .. "/mabcde-flaky.md")
        wait_for(2000, function()
          return #seen > 0
        end, "announcement")
        vim.wait(200)
      end)
      vim.notify = orig
      assert(ok, err)
      eq(#seen, 1, "once")
      truthy(seen[1].msg:find("claude@api remembered", 1, true) and seen[1].msg:find("TestLogin", 1, true), seen[1].msg)
      eq(seen[1].id, "nagare:memory")
    end)
  end },

  { "memory: an agent's lesson is proposed, announced, and approved from the editor", function()
    with_bin(function(bin, home)
      local repo = git_repo("mem-propose")
      config.set("notify", { waiting = true, finished = true, min_seconds = 0, memory = true })
      memory.watch(repo)
      local seen = {}
      local orig = vim.notify
      vim.notify = function(msg)
        table.insert(seen, msg)
      end
      local ok, err = pcall(function()
        local event = vim.json.encode({ hook_event_name = "Stop", session_id = "p1", cwd = repo,
          last_assistant_message = "Done. It turns out the dev server caches env vars at boot, so restart it after editing .env files." })
        vim.fn.system({ "sh", "-c", "HOME='" .. home .. "' '" .. bin .. "' hook-state" }, event)
        wait_for(3000, function()
          return #memory.pending(repo) == 1
        end, "a pending proposal")
        wait_for(2000, function()
          return #seen > 0
        end, "announced")
        truthy(seen[1]:find("proposed a memory", 1, true), seen[1])
        eq(#memory.list(repo), 0, "not active yet")
        local p = memory.pending(repo)[1]
        truthy(memory.decide(repo, p.id, true))
        eq(#memory.pending(repo), 0)
        eq(memory.list(repo)[1].id, p.id, "approved into the list")
      end)
      vim.notify = orig
      assert(ok, err)
    end)
  end },
}
