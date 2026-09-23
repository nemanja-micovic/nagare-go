-- lualine component: `lualine_x = { "nagare" }`. Each status gets its own
-- colour, like lualine's diagnostics component: "● 2 ◐ 1".
local component = require("lualine.component"):extend()

function component:init(options)
  component.super.init(self, options)
  local nagare = require("nagare")
  self.hl = {}
  for _, s in ipairs({ "waiting_input", "running" }) do
    self.hl[s] = self:create_hl(nagare.status_hl[s], s)
  end
end

function component:update_status()
  local nagare = require("nagare")
  local c = nagare.summary()
  local parts = {}
  for _, s in ipairs({ "waiting_input", "running" }) do
    if c[s] > 0 then
      table.insert(parts, self:format_hl(self.hl[s]) .. nagare.status_icon[s] .. " " .. c[s])
    end
  end
  return table.concat(parts, " ")
end

return component
