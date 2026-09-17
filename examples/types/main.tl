--- TypeLua types showcase.
--- Covers primitives, literals, unions, intersections, arrays, maps, tuples, func types.

type ID = number | string

local a: number = 1
local b: integer = 2
local c: float = 2.5
local d: string = "hi"
local e: boolean = true
local f: nil = nil
local g: any = { anything = true }
local h: unknown = 1

-- Literals.
local litStr: "red" = "red"
local litNum: 42 = 42
local litBool: true = true

-- Union / optional / intersection.
local u: number | string = "hi"
u = 10
local opt: string | nil = nil
opt = "ok"
local optSugar: number? = nil
optSugar = 3
type AB = { a: number } & { b: string }
local ab: AB = { a = 1, b = "x" }

-- Arrays, maps, tuples.
local arr: number[] = { 1, 2, 3 }
local arr2: Array<string> = { "a", "b" }
local m: map<string, number> = { score = 100 }
print(m.score)
print(m["score"])
local tup: [string, number] = { "a", 1 }
local first: string = tup[1]

-- Table type with optional + indexer.
type Config = {
    host: string,
    port?: integer,
    [string]: any,
}
local cfg: Config = { host = "localhost" }

-- Function types.
local cmp: (x: number, y: number) -> boolean = function(x, y) return x < y end
print(cmp(1, 2))

-- typeof / keyof.
local pname: string = "point"
type P = { x: number, y: number }
type PKey = keyof P
local k: PKey = "x"
local p: P = { x = 1, y = 2 }
type PT = typeof(p)

print(a + b + c)
print(d .. "!")
print(litStr, litNum, litBool)
print(u, opt, optSugar)
print(arr[1], arr2[1], tup[1], tup[2])
print(cfg.host, k, first)
