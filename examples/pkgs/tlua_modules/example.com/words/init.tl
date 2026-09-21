--- Leaf dependency: word utilities (no dependencies of its own).
local M = {}

function M.shout(s: string): string
  return string.upper(s)
end

function M.count(s: string): integer
  local n = 0
  for _ in string.gmatch(s, "%S+") do
    n = n + 1
  end
  return n
end

return M
