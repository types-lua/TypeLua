--- Mid-level dependency: formats titles using example.com/words.
--- At runtime it requires the full dotted path from tlua_modules root.
import W from "example.com.words"

local M = {}

function M.title(s: string): string
  return W.shout(s) .. " (" .. tostring(W.count(s)) .. " words)"
end

return M
