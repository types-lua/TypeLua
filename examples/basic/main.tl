--- TypeLua basic example.
--- Run: tlua check / tlua build

type Point = { x: number, y: number }

interface Shape {
    area(): number
}

enum Color {
    Red = "red",
    Green = "green",
    Blue = "blue",
}

local function dist(a: Point, b: Point): number
    local dx: number = a.x - b.x
    local dy: number = a.y - b.y
    return dx * dx + dy * dy
end

local function greet<T>(name: T): string
    return "hello " .. tostring(name)
end

local p: Point = { x = 1, y = 2 }
local q: Point = { x = 4, y = 6 }

print(dist(p, q))
print(greet("world"))

local maybe: number | nil = nil
if maybe ~= nil then
    print(maybe + 1)
end

local c: Color = Color.Red
print(c)

local nums: number[] = { 1, 2, 3 }
for i = 1, #nums do
    print(nums[i])
end

local m: map<string, number> = { score = 100 }
print(m.score)
