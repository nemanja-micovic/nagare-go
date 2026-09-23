-- Headless test runner for the Neovim plugin. No dependencies:
--
--   nvim --headless --clean -l tests/nvim/run.lua
--
-- Each *_spec.lua returns a list of { name, fn }. A test fails by erroring.
-- NAGARE_BIN, when set, points the end-to-end tests at a built nagare-go.
local root = vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":p:h:h:h")
vim.opt.rtp:prepend(root)
vim.o.swapfile = false
vim.o.columns, vim.o.lines = 160, 50

-- Keep the tests away from the user's real state.
local sandbox = vim.fn.tempname()
vim.fn.mkdir(sandbox, "p")
vim.env.XDG_DATA_HOME = sandbox .. "/data"
_G.SANDBOX = sandbox

-- Helpers shared by the specs.

function _G.eq(got, want, what)
  if not vim.deep_equal(got, want) then
    error(("%s: got %s, want %s"):format(what or "value", vim.inspect(got), vim.inspect(want)), 2)
  end
end

function _G.truthy(v, what)
  if not v then
    error((what or "value") .. " should be truthy", 2)
  end
end

--- Waits up to ms for cond() to hold, running the event loop meanwhile.
function _G.wait_for(ms, cond, what)
  if not vim.wait(ms, cond, 20) then
    error("timed out waiting for " .. (what or "condition"), 2)
  end
end

--- Registers an agent without starting a process, for tests about
--- ordering and rendering rather than terminals.
function _G.fake_agent(fields)
  local agents = require("nagare.agents")
  local n = #agents.list + 1
  local a = vim.tbl_extend("force", {
    id = 1000 + n,
    key = "nvim:test:" .. n,
    kind = "claude",
    name = "claude_" .. n,
    root = "/src/proj",
    project = "proj",
    cwd = "/src/proj",
    status = "idle",
    changed = os.time(),
    used = os.time(),
    source = "nvim",
  }, fields or {})
  table.insert(agents.list, a)
  agents.by_key[a.key] = a
  return a
end

--- A throwaway git repository with one commit. Returns its path.
function _G.git_repo(name)
  local dir = SANDBOX .. "/" .. name
  vim.fn.mkdir(dir, "p")
  local function git(...)
    local out = vim.fn.system(vim.list_extend({ "git", "-C", dir }, { ... }))
    assert(vim.v.shell_error == 0, out)
  end
  git("init", "-q", "-b", "main")
  git("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")
  return require("nagare.util").normalize(dir)
end

-- Notifications would interleave with test output.
vim.notify = function() end

require("nagare").setup({
  keys = false,
  tmux = { enabled = false },
  states_dir = sandbox .. "/states",
  poll_ms = 100,
  on_project_open = false,
  restore = false,
  notify = { min_seconds = 0 },
})
-- The real entry point: :Nagare, the <Plug> maps and the scheduled init.
vim.cmd("runtime plugin/nagare.lua")
require("nagare")._init()

local specs = vim.fn.glob(root .. "/tests/nvim/*_spec.lua", false, true)
local filter = vim.env.TEST_FILTER -- plain substring
local pattern = vim.env.TEST_PATTERN -- Lua pattern, e.g. "open: [or]"
local passed, failed = 0, {}

for _, file in ipairs(specs) do
  local tests = dofile(file)
  for _, t in ipairs(tests) do
    local name = vim.fn.fnamemodify(file, ":t:r") .. " › " .. t[1]
    if (not filter or name:find(filter, 1, true)) and (not pattern or name:find(pattern)) then
      -- Every test starts from a clean editor and registry.
      pcall(vim.cmd, "silent! tabonly!")
      pcall(vim.cmd, "silent! only!")
      pcall(vim.cmd, "enew!")
      pcall(vim.api.nvim_tabpage_del_var, 0, "nagare_root")
      vim.cmd("silent! cd " .. vim.fn.fnameescape(sandbox))
      for _, a in ipairs(require("nagare.agents").list) do
        require("nagare.agents").kill(a)
      end
      require("nagare.agents")._reset()
      require("nagare.projects")._reset()
      require("nagare.tmux").list = {}
      local ok, err = xpcall(t[2], debug.traceback)
      if ok then
        passed = passed + 1
        io.stdout:write("ok   " .. name .. "\n")
      else
        table.insert(failed, name)
        io.stdout:write("FAIL " .. name .. "\n" .. tostring(err) .. "\n")
      end
    end
  end
end

io.stdout:write(("\n%d passed, %d failed\n"):format(passed, #failed))
vim.fn.delete(sandbox, "rf")
os.exit(#failed == 0 and 0 or 1)
