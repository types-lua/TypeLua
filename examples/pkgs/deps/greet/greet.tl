--- Example vendored-style dependency (local replace; see ../tlua.toml).
local M = {}

function M.hello(name: string): string
  return "hi " .. name
end

return M
