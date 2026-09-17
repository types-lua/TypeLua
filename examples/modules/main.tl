--- TypeLua modules (consumer).
--- Run after build with `lua dist/modules/main.lua` (needs package.path).

type MathUtils = {
    add(a: number, b: number): number,
    point(x: number, y: number): { x: number, y: number },
}

local U = require("math_utils") as MathUtils

local sum: number = U.add(2, 3)
local pt = U.point(1, 2)
print(sum, pt.x, pt.y)
