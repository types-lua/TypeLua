local W = require("example.com.words.words")
local M = {}
function M.title(s)
    return W.shout(s) .. " (" .. tostring(W.count(s)) .. " words)"
end
return M
