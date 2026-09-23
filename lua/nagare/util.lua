local M = {}

M.uv = vim.uv or vim.loop

--- Absolute, symlink-free path without a trailing slash.
function M.normalize(path)
  if not path or path == "" then
    return path
  end
  path = vim.fn.fnamemodify(vim.fn.expand(path), ":p")
  local real = M.uv.fs_realpath(path)
  path = real or path
  if #path > 1 then
    path = path:gsub("/+$", "")
  end
  return path
end

function M.basename(path)
  return vim.fn.fnamemodify(path, ":t")
end

function M.read_file(path)
  local fd = io.open(path, "r")
  if not fd then
    return nil
  end
  local data = fd:read("*a")
  fd:close()
  return data
end

function M.write_file(path, data)
  vim.fn.mkdir(vim.fn.fnamemodify(path, ":h"), "p")
  -- Write-then-rename, like the Go side, so a reader never sees half a file.
  local tmp = path .. ".tmp"
  local fd = io.open(tmp, "w")
  if not fd then
    return false
  end
  fd:write(data)
  fd:close()
  return os.rename(tmp, path)
end

function M.json_decode(data)
  if not data or data == "" then
    return nil
  end
  local ok, value = pcall(vim.json.decode, data)
  if ok then
    return value
  end
end

--- Resolves a directory into repository facts, one git call per directory.
--- Worktrees of one repo share `root` (the main checkout); `worktree` is set
--- only for a linked worktree, mirroring internal/git on the Go side.
local repo_cache = {}

function M.describe(dir)
  dir = M.normalize(dir)
  if repo_cache[dir] then
    return repo_cache[dir]
  end
  local out = vim.fn.systemlist({
    "git", "-C", dir, "rev-parse", "--git-common-dir", "--show-toplevel", "--abbrev-ref", "HEAD",
  })
  local info
  if vim.v.shell_error == 0 and #out >= 2 then
    local common = out[1]
    if not common:match("^/") then
      common = dir .. "/" .. common
    end
    common = M.normalize(common)
    local root = vim.fn.fnamemodify(common, ":h")
    local top = M.normalize(out[2])
    info = {
      root = root,
      toplevel = top,
      branch = out[3] ~= "HEAD" and out[3] or nil,
      worktree = top ~= root and M.basename(top) or nil,
      name = M.basename(root),
    }
  else
    info = { root = dir, toplevel = dir, name = M.basename(dir) }
  end
  repo_cache[dir] = info
  return info
end

--- Branches move; drop cached facts so the next describe re-reads them.
function M.forget_repos()
  repo_cache = {}
end

function M.ago(ts)
  if not ts then
    return ""
  end
  local d = os.time() - ts
  if d < 60 then
    return d .. "s"
  elseif d < 3600 then
    return math.floor(d / 60) .. "m"
  elseif d < 86400 then
    return math.floor(d / 3600) .. "h"
  end
  return math.floor(d / 86400) .. "d"
end

--- Parses the RFC 3339 UTC timestamps the Go side writes.
function M.parse_time(s)
  if type(s) ~= "string" then
    return nil
  end
  local y, mo, d, h, mi, se = s:match("^(%d+)-(%d+)-(%d+)T(%d+):(%d+):(%d+)")
  if not y then
    return nil
  end
  local t = os.time({ year = y, month = mo, day = d, hour = h, min = mi, sec = se })
  -- os.time reads the table as local time; shift it back to UTC.
  local now = os.time()
  local offset = os.difftime(now, os.time(os.date("!*t", now)))
  return t + offset
end

--- First line, whitespace-collapsed and cut to `width` display cells.
function M.oneline(s, width)
  if not s or s == "" then
    return ""
  end
  s = s:gsub("[\r\n]+", " "):gsub("%s+", " "):gsub("^%s+", "")
  if width and vim.fn.strdisplaywidth(s) > width then
    s = vim.fn.strcharpart(s, 0, math.max(width - 1, 0)) .. "…"
  end
  return s
end

--- Pads or truncates to exactly `width` display cells.
function M.fit(s, width)
  s = s or ""
  local w = vim.fn.strdisplaywidth(s)
  if w > width then
    s = vim.fn.strcharpart(s, 0, math.max(width - 1, 0)) .. "…"
    w = vim.fn.strdisplaywidth(s)
  end
  return s .. string.rep(" ", width - w)
end

return M
