local config = require("nagare.config")
local usage = require("nagare.usage")

local function transcript(path, ctx)
  vim.fn.writefile({
    vim.json.encode({ type = "assistant", message = { id = "m1", model = "claude-opus-5",
      usage = { input_tokens = 1000, output_tokens = 2000, cache_read_input_tokens = ctx, cache_creation_input_tokens = 0 } } }),
  }, path)
end

return {
  { "usage: a hook's transcript puts cost and context on the board", function()
    local bin = vim.env.NAGARE_BIN
    if not bin or vim.fn.executable(bin) ~= 1 then
      io.stdout:write("     (skipped: set NAGARE_BIN to a built nagare-go)\n")
      return
    end
    local old = vim.deepcopy(config.tmux)
    config.set("tmux", { enabled = false, bin = bin, poll_ms = 4000 })
    local seen = {}
    local orig = vim.notify
    vim.notify = function(msg)
      table.insert(seen, msg)
    end
    local ok, err = pcall(function()
      local path = SANDBOX .. "/t-" .. vim.loop.hrtime() .. ".jsonl"
      transcript(path, 899000) -- 90% of a 1M window
      local a = fake_agent({ key = "nvim:usage:1", name = "spender" })
      require("nagare.status").apply({ pane_id = a.key, state = "idle", transcript_path = path, timestamp = "2026-01-01T00:00:00Z" })
      eq(a.transcript, path)
      wait_for(6000, function()
        return usage.get(a) ~= nil
      end, "usage computed")
      local u = usage.get(a)
      eq(u.model, "claude-opus-5")
      eq(u.context_pct, 90)
      -- $5 in, $25 out, $0.50 cache read per MTok
      local want = (1000 * 5 + 2000 * 25 + 899000 * 0.5) / 1e6
      truthy(math.abs(u.cost_usd - want) < 1e-9, vim.inspect(u))
      eq(usage.label(a), ("$%.2f 90%%"):format(want))
      local lines = require("nagare.board").build(140)
      local text = {}
      for _, l in ipairs(lines) do
        table.insert(text, l.text)
      end
      truthy(table.concat(text, "\n"):find(("$%.2f 90%%"):format(want), 1, true), table.concat(text, "\n"))
      local warned = false
      for _, m in ipairs(seen) do
        warned = warned or m:find("90% of its context", 1, true) ~= nil
      end
      truthy(warned, "context warning: " .. vim.inspect(seen))
    end)
    vim.notify = orig
    config.set("tmux", old)
    assert(ok, err)
  end },
}
