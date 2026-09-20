--- TypeLua conditional types and infer.
--- Everything here erases; the runtime below is plain values.

-- Basic branch on a constraint.
type IsString<T> = T extends string ? true : false

-- infer binds the matched part (array element here).
type Element<T> = T extends (infer U)[] ? U : T

-- Tuple patterns with a rest element.
type Head<T> = T extends [infer H, ...unknown[]] ? H : never

-- infer in function positions.
type ReturnOf<T> = T extends (...args: any[]) -> infer R ? R : never

-- infer through nominal class patterns.
class Box<T> {
  v: T,
  constructor(v: T) self.v = v end,
  get(): T return self.v end,
}
type UnwrapBox<T> = T extends Box<infer U> ? U : never

-- Chained conditionals read like a decision table.
type Level<T> = T extends string ? 1 : T extends number ? 2 : 3

-- Naked type parameters distribute over unions:
-- Element<string | number[]> is string | number.
local s: IsString<string> = true
local ns: IsString<number> = false
print(s, ns)

local e: Element<number[]> = 10
local e2: Element<string> = "hi"
local e3: Element<string | number[]> = "mix"
print(e, e2, e3)

local h: Head<[string, number]> = "first"
print(h)

local function answer(): number return 42 end
local r: ReturnOf<typeof(answer)> = 42
print(r)

local b = Box.new(7)
local u: UnwrapBox<Box<number>> = 7
print(u, b:get())

local lv: Level<boolean> = 3
print(lv)

-- Distribution keeps nil so narrowing still works.
local maybe: Element<string | nil> = nil
if maybe ~= nil then
  print(maybe)
end
maybe = "back"
print(maybe)
