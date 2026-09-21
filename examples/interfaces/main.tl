--- TypeLua interfaces: structural typing, extends, optional, readonly.

interface Named {
    name: string,
}

interface User extends Named {
    id: integer,
    nickname?: string,
    readonly created: string,
    greet(msg: string): string,
}

local function makeUser(name: string, id: integer): User
    return {
        name = name,
        id = id,
        created = "today",
        greet = function(msg: string) return msg .. name end,
    }
end

local u: User = makeUser("ada", 7)
print(u.name, u.id, u.created)
print(u.greet("hi "))

-- Width subtyping: extra fields are fine.
local u2: Named = u
print(u2.name)

-- Optional field may be absent, and reads as `T | nil`.
local nick: string | nil = u.nickname
if nick ~= nil then
    print(nick)
end

-- Table type method shorthand (both `:` and `->` returns).
type Calculator = {
    add(a: number, b: number): number,
    mul(a: number, b: number) -> number,
}
local calc: Calculator = {
    add = function(a: number, b: number) return a + b end,
    mul = function(a: number, b: number) return a * b end,
}
print(calc.add(2, 3), calc.mul(2, 3))

-- Recursive interfaces and union member access.
interface Node { v: number, next?: Node }
local function head(n: Node): number
  if n.next then
    return n.next.v
  end
  return n.v
end
type Pair = { x: number, tag: string }
type Triple = { x: number, tag: string, y?: boolean }
local function getX(p: Pair | Triple): number
  return p.x
end
print(head({ v = 1, next = { v = 2 } }))
print(getX({ x = 3, tag = "t" }))
