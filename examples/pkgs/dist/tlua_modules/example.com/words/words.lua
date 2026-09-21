local M = {}
function M.shout(s)
    return string.upper(s)
end
function M.count(s)
    local n = 0
    for _ in string.gmatch(s, "%S+") do
        n = n + 1
    end
    return n
end
return M
