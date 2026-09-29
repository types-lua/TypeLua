--- TypeLua functions: annotations, multi-return, varargs, closures, methods.

-- Both `:` and `->` return annotations.
local function add(a: number, b: number): number
    return a + b
end

local function mul(a: number, b: number) -> number
    return a * b
end

-- Multiple return values.
local function divmod(a: number, b: number): (number, number)
    return a / b, a % b
end

local q, r = divmod(7, 3)
local qq: number = q
print(qq, r)

-- Only the last call in a list expands.
local function pair(): (number, string)
    return 1, "one"
end

local x, y = pair()
print(x, y)

-- Varargs.
local function sum(...nums: number[]): number
    local total: number = 0
    for i, v in ipairs(nums) do
        total = total + v
    end
    return total
end

print(sum(1, 2, 3, 4))

-- Closures capture locals.
local function counter(): () -> number
    local n: number = 0
    return function(): number
        n = n + 1
        return n
    end
end

local next = counter()
print(next(), next())

-- Function types as values.
local op: (a: number, b: number) -> number = add
print(op(2, 3))
print(mul(2, 3))

-- Method definition + call.
local M = {}
function M.hello(name: string): string
    return "hi " .. name
end

print(M.hello("ada"))

local Counter = { count = 0 }
function Counter:inc(): number
    self.count = self.count + 1
    return self.count
end

print(Counter:inc())

-- Brace bodies: `{ ... }` replaces `... end` everywhere a function body
-- appears (named, anonymous, methods, async). Same AST, same types.
local function sub(a: number, b: number): number { return a - b }

local greet = function(name: string): string { return "yo " .. name }

function Counter:dec(): number { self.count = self.count - 1 return self.count }

print(sub(9, 4), greet("bob"), Counter:dec())
