--- Mid-level dependency: formats titles using example.com/words.
--- At runtime it requires the full dotted path from tlua_modules root.
import W: { shout(s: string): string, count(s: string): integer } from "example.com.words.words"

local M = {}

function M.title(s: string): string
  return W.shout(s) .. " (" .. tostring(W.count(s)) .. " words)"
end

return M
