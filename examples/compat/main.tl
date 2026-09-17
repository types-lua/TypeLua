--- TypeLua x Lua 5.1 / LuaJIT compat.
--- No `//`, no bitwise, no `<const>`/`<close>`; build with `--target 5.1`.
--- The emitter also strips attributes automatically if you forget one.

local function add(a: number, b: number): number
    return a + b
end

local t: map<string, number> = { x = 1 }
t.y = 2
print(add(t.x, t.y))

local arr: number[] = { 1, 2, 3 }
local total: number = 0
for i = 1, #arr do
    total = total + arr[i]
end
print(total)

local s: string = "a" .. "b"
print(s, string.upper(s))
