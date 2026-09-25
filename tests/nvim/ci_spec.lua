local config = require("nagare.config")
local ci = require("nagare.ci")

local api = vim.api

-- A stand-in `gh` that answers from files the test writes.
local function fake_gh(dir)
  local path = dir .. "/gh"
  vim.fn.writefile({
    "#!/bin/bash",
    'case "$1 $2" in',
    '  "pr view") cat "' .. dir .. '/pr.json" ;;',
    '  "pr checks") cat "' .. dir .. '/checks.json" ;;',
    '  "run view") echo "step 3: TestLogin FAILED: expected 200 got 500" ;;',
    "  *) exit 1 ;;",
    "esac",
  }, path)
  vim.fn.setfperm(path, "rwxr-xr-x")
  return path
end

return {
  { "summarize: fail beats pending beats pass", function()
    eq((ci.summarize({ { bucket = "pass" }, { bucket = "pending" } })), "pending")
    eq((ci.summarize({ { bucket = "pass" }, { bucket = "fail", name = "test" } })), "fail")
    eq((ci.summarize({ { bucket = "pass" }, { bucket = "skipping" } })), "pass")
    eq((ci.summarize({})), "pending", "no checks yet")
    eq(ci.run_id("https://github.com/o/r/actions/runs/123/job/456"), "123")
  end },

  { "loop: a failing check is announced, shown, and its log sent to the agent", function()
    local dir = SANDBOX .. "/gh-" .. vim.loop.hrtime()
    vim.fn.mkdir(dir, "p")
    config.set("gh", fake_gh(dir))
    config.agents.fake = { cmd = { "bash", "-c", 'while read -r l; do echo "GOT:$l"; done', "fake" }, sigil = "F" }
    local repo = git_repo("ci")
    local wt = assert(require("nagare.projects").add_worktree(repo, "fix-login"))
    local a = assert(require("nagare.agents").spawn({ kind = "fake", cwd = wt, name = "fix-login" }))
    vim.fn.writefile({ '{"number":42,"url":"https://github.com/o/r/pull/42","state":"OPEN"}' }, dir .. "/pr.json")
    vim.fn.writefile({ '[{"name":"lint","bucket":"pass","link":""},{"name":"test","bucket":"pending","link":""}]' }, dir .. "/checks.json")

    local seen = {}
    local orig = vim.notify
    vim.notify = function(msg)
      table.insert(seen, msg)
    end
    local ok, err = pcall(function()
      truthy(ci.track(a), "PR found")
      ci.poll(a)
      eq(ci.label(a), "CI…")
      vim.fn.writefile({ '[{"name":"lint","bucket":"pass","link":""},{"name":"test","bucket":"fail","link":"https://github.com/o/r/actions/runs/77/job/1"}]' }, dir .. "/checks.json")
      ci.poll(a)
      eq(ci.label(a), "CI✗")
      truthy(seen[#seen]:find("CI failed for", 1, true) and seen[#seen]:find("test", 1, true), vim.inspect(seen))
      local lines = require("nagare.board").build(140)
      local text = {}
      for _, l in ipairs(lines) do
        table.insert(text, l.text)
      end
      truthy(table.concat(text, "\n"):find("CI✗", 1, true), "board shows it")

      truthy(ci.send_failures(a))
      wait_for(3000, function()
        return table.concat(api.nvim_buf_get_lines(a.buf, 0, -1, false), "\n"):find("GOT:CI failed on PR #42", 1, true) ~= nil
      end, "agent got the failure")
      truthy(table.concat(api.nvim_buf_get_lines(a.buf, 0, -1, false), "\n"):find("TestLogin FAILED", 1, true), "with the log")

      vim.fn.writefile({ '{"number":42,"url":"u","state":"MERGED"}' }, dir .. "/pr.json")
      ci.poll(a)
      eq(ci.label(a), "merged")
    end)
    vim.notify = orig
    config.set("gh", "gh")
    ci.stop()
    assert(ok, err)
  end },
}
