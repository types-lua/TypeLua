--- TypeLua generics showcase.
--- Generic functions, generic aliases, constraints, defaults, nesting.

type Box<T> = { v: T }
type Pair<K, V> = { k: K, v: V }

local function identity<T>(x: T): T
    return x
end

local n: number = identity(1)
local s: string = identity("hi")

local function first<T>(arr: T[]): T | nil
    return arr[1]
end

local f1: number | nil = first({ 1, 2, 3 })
local f2: string | nil = first({ "a" })

-- Constraint: only numbers.
local function add<T extends number>(a: T, b: T): number
    return a + b
end

print(add(1, 2))

-- Default type argument.
type Wrap<T = string> = { inner: T }
local w1: Wrap = { inner = "hi" }
local w2: Wrap<number> = { inner = 1 }

-- Nested generics: the `>>` splitter matters here.
local m: map<string, Box<number[]>> = {}
m["a"] = { v = { 1, 2 } }
print(m["a"].v[1])

local b: Box<number> = { v = 10 }
local p: Pair<string, number> = { k = "age", v = 3 }
print(b.v, p.k, p.v, n, s, f1, f2, w1.inner, w2.inner)
