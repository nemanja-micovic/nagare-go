local agents = require("nagare.agents")
local config = require("nagare.config")
local session = require("nagare.session")

local api = vim.api

local function agent_windows()
  local out = {}
  for _, w in ipairs(api.nvim_tabpage_list_wins(0)) do
    local a = agents.from_buf(api.nvim_win_get_buf(w))
    if a then
      table.insert(out, { win = w, agent = a, ft = vim.bo[api.nvim_win_get_buf(w)].filetype })
    end
  end
  return out
end

return {
  { "snapshot: records agent splits and keeps g:NagareLayout current", function()
    config.agents.fake = { cmd = { "bash", "-c", "sleep 60", "fake" }, sigil = "F" }
    local repo = git_repo("sess-snap")
    local a = assert(agents.spawn({ kind = "fake", cwd = repo, name = "keeper" }))
    require("nagare").show(a)
    vim.cmd("stopinsert")
    local snap = session.snapshot()
    eq(#snap, 1)
    eq(snap[1].name, "keeper")
    eq(snap[1].root, repo)
    session.record()
    vim.wait(50)
    eq(vim.json.decode(vim.g.NagareLayout)[1].cwd, repo)
  end },

  { "mksession round trip: splits come back as placeholders that resume", function()
    config.agents.fake = { cmd = { "bash", "-c", 'echo "ARGS:$*"; sleep 60', "fake" }, sigil = "F",
      continue = { "--continue" } }
    local repo = git_repo("sess-rt")
    local a = assert(agents.spawn({ kind = "fake", cwd = repo, name = "worker" }))
    require("nagare").show(a)
    vim.cmd("stopinsert")
    session.record()
    vim.wait(50)
    local file = SANDBOX .. "/sess-" .. vim.loop.hrtime() .. ".vim"
    vim.o.sessionoptions = "buffers,curdir,tabpages,winsize,help,globals,skiprtp,folds"
    vim.cmd("mksession! " .. vim.fn.fnameescape(file))

    -- A restart: the process is gone, the agent is a saved row.
    agents.save()
    agents.kill(a)
    agents._reset()
    -- A real restart leaves no old terminal buffers behind.
    for _, b in ipairs(api.nvim_list_bufs()) do
      if vim.b[b].nagare_agent then
        pcall(api.nvim_buf_delete, b, { force = true })
      end
    end
    pcall(vim.cmd, "silent! tabonly!")
    pcall(vim.cmd, "silent! only!")
    vim.g.NagareLayout = nil
    config.set("restore", true)
    vim.cmd("silent source " .. vim.fn.fnameescape(file))
    config.set("restore", false)
    wait_for(2000, function()
      return #agent_windows() == 1
    end, "the agent split came back")
    local w = agent_windows()[1]
    eq(w.ft, "nagare_saved", "a placeholder, nothing started")
    eq(w.agent.status, "saved")
    eq(w.agent.buf, nil)

    api.nvim_set_current_win(w.win)
    api.nvim_feedkeys(api.nvim_replace_termcodes("<CR>", true, false, true), "x", false)
    wait_for(3000, function()
      return w.agent.buf and api.nvim_win_get_buf(w.win) == w.agent.buf
        and table.concat(api.nvim_buf_get_lines(w.agent.buf, 0, -1, false), "\n"):find("ARGS:--continue", 1, true) ~= nil
    end, "Enter resumed it in place")
    vim.cmd("stopinsert")
  end },

  { "restore: a live agent's own buffer comes back, not a placeholder", function()
    config.agents.fake = { cmd = { "bash", "-c", "sleep 60", "fake" }, sigil = "F" }
    local repo = git_repo("sess-live")
    require("nagare.projects").open(repo)
    local a = assert(agents.spawn({ kind = "fake", cwd = repo, name = "live" }))
    eq(session.restore({ { cwd = repo, name = "live", root = repo, width = 50 } }), 1)
    local w = agent_windows()
    eq(#w, 1)
    eq(api.nvim_win_get_buf(w[1].win), a.buf)
    eq(session.restore({ { cwd = repo, name = "live", root = repo } }), 0, "not duplicated")
  end },

  { "resession extension saves and restores through the same path", function()
    local ext = require("resession.extensions.nagare")
    config.agents.fake = { cmd = { "bash", "-c", "sleep 60", "fake" }, sigil = "F" }
    local repo = git_repo("sess-resession")
    local a = assert(agents.spawn({ kind = "fake", cwd = repo, name = "r" }))
    require("nagare").show(a)
    vim.cmd("stopinsert")
    local data = ext.on_save()
    eq(data[1].name, "r")
    vim.cmd("close")
    ext.on_post_load(data)
    wait_for(1000, function()
      return #agent_windows() == 1
    end, "restored")
  end },
}
