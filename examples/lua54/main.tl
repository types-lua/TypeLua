--- TypeLua x Lua 5.4: const/close, goto, bitwise, integer division.
--- Build with default `--target 5.4`. For 5.1, see examples/compat.

local x <const> = 10
print(x)

local function scoped(): number
    local h <close> = setmetatable({}, { __close = function() end }) as any
    return 1
end
print(scoped())

-- goto / labels.
local n: integer = 0
::retry::
n = n + 1
if n < 3 then
    goto retry
end
print(n)

-- Bitwise needs integers.
local a: integer = 12 -- 0b1100
local b: integer = 10 -- 0b1010
print(a & b, a | b, a ~ b, a >> 1, a << 1, ~a)

-- Integer division / modulo / power / concat / length.
print(7 // 2, 7 % 2, 2 ^ 10)
print("a" .. "b", #"hello")

-- Numeric + generic for with annotations.
for i: integer = 1, 3 do
    print(i)
end
local kv: map<string, integer> = { a = 1, b = 2 }
for k: string, v: integer in pairs(kv) do
    print(k, v)
end

-- Long strings / comments survive lexing.
local s: string = [==[
multi
line
]==]
print(s)

-- vararg + multiple returns + assert.
local function first2(...args: string[]): (string | nil, string | nil)
    return args[1], args[2]
end
local p, q = first2("x", "y")
print(p, q)
