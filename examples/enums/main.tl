--- TypeLua enums: string enums, numeric enums with auto-increment.

enum Color {
    Red = "red",
    Green = "green",
    Blue = "blue",
}

enum Direction {
    North,
    South,
    East,
    West,
}

enum HttpStatus {
    Ok = 200,
    NotFound = 404,
    ServerError,
}

local c: Color = Color.Red
print(c)

-- Enum is assignable to its underlying type.
local s: string = c
print(s)

local d: Direction = Direction.North
print(d)

local st: HttpStatus = HttpStatus.Ok
print(st)
print(HttpStatus.NotFound)
print(HttpStatus.ServerError)

-- Exhaustive-ish dispatch on string enum.
local function colorName(c: Color): string
    if c == Color.Red then
        return "red!"
    end
    if c == Color.Green then
        return "green!"
    end
    return "blue!"
end

print(colorName(Color.Green))
