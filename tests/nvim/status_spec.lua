local status = require("nagare.status")
local agents = require("nagare.agents")

local function lines(s)
  return vim.split(s, "\n")
end

return {
  { "detect: permission prompt is waiting", function()
    eq(status.detect(lines("Bash(rm -rf build)\n\nDo you want to proceed?\n❯ 1. Yes\n  2. No\n\n\n")), "waiting_input")
  end },
  { "detect: spinner is running", function()
    eq(status.detect(lines("⠹ Thinking… (esc to interrupt)\n")), "running")
  end },
  { "detect: bare prompt is idle", function()
    eq(status.detect(lines("Done.\n\n❯ \n")), "idle")
  end },
  { "detect: an answered prompt above a fresh input prompt is not waiting", function()
    eq(status.detect(lines("Do you want to proceed?\n❯ 1. Yes\n  2. No\nok, running tests\nDone.\n\n❯ \n")), "idle")
  end },
  { "detect: working below an old input prompt is running", function()
    eq(status.detect(lines("❯ \n✻ Working… (esc to interrupt)\n╭──╮\n❯ \n╰──╯")), "running")
  end },
  { "detect: empty screen leaves status alone", function()
    eq(status.detect({ "", "  ", "" }), nil)
  end },
  { "detect: only the last 15 rows count", function()
    local l = { "Do you want to proceed?" }
    for _ = 1, 20 do
      table.insert(l, "output")
    end
    eq(status.detect(l), "idle", "stale prompt far above")
  end },

  { "apply: hook state drives the matching agent only", function()
    local a = fake_agent({ key = "nvim:1:1" })
    local b = fake_agent({ key = "nvim:1:2" })
    truthy(status.apply({ pane_id = "nvim:1:1", state = "waiting_input", event = "Notification", timestamp = "2026-01-01T00:00:01Z" }))
    eq(a.status, "waiting_input")
    eq(b.status, "idle")
    eq(a.hooked, true, "hooked stops scraping")
    eq(status.apply({ pane_id = "%3", state = "working" }), false, "tmux pane is not ours")
  end },
  { "apply: an older write never overrides a newer one", function()
    local a = fake_agent({ key = "nvim:1:1" })
    status.apply({ pane_id = "nvim:1:1", state = "idle", timestamp = "2026-01-01T00:00:05Z", last_message = "done" })
    status.apply({ pane_id = "nvim:1:1", state = "working", timestamp = "2026-01-01T00:00:02Z" })
    eq(a.status, "idle")
    eq(a.last_message, "done")
  end },
  { "apply: a dead process stays dead", function()
    local a = fake_agent({ key = "nvim:1:1", status = "dead", exit_code = 0 })
    status.apply({ pane_id = "nvim:1:1", state = "working", timestamp = "2026-01-01T00:00:09Z" })
    eq(a.status, "dead")
  end },

  { "notify: waiting and long finishes interrupt, others do not", function()
    local seen = {}
    local orig = vim.notify
    vim.notify = function(msg)
      table.insert(seen, msg)
    end
    local ok, err = pcall(function()
      local a = fake_agent({ status = "running", changed = os.time() - 60 })
      agents.set_status(a, "waiting_input")
      agents.set_status(a, "running")
      a.changed = os.time() - 60
      agents.set_status(a, "idle", { last_message = "All tests pass." })
      agents.set_status(a, "running")
    end)
    vim.notify = orig
    assert(ok, err)
    eq(#seen, 2, "notifications " .. vim.inspect(seen))
    truthy(seen[1]:find("needs you"), "waiting message")
    truthy(seen[2]:find("finished") and seen[2]:find("All tests pass"), "finished message")
  end },
}
