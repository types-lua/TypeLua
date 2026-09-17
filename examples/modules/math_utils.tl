--- TypeLua modules: one table per file, `return M` at the end.
--- `require` returns `any`, so annotate at the boundary with `as`.

local M = {}

function M.add(a: number, b: number): number
    return a + b
end

function M.point(x: number, y: number): { x: number, y: number }
    return { x = x, y = y }
end

return M
