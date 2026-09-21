--- TypeLua narrowing: nil checks, typeof, early returns, and/or.

local function needNumber(x: number | nil): number
    if x == nil then
        return 0
    end
    return x
end

local function needString(x: string | nil): string
    if x ~= nil then
        return x
    end
    return "empty"
end

local function what(v: any): string
    if type(v) == "string" then
        return v
    end
    if type(v) == "number" then
        return tostring(v)
    end
    if type(v) == "table" then
        return "table"
    end
    return "other"
end

-- `not` flips the branch.
local function isMissing(s: string | nil): boolean
    if not s then
        return true
    end
    return false
end

-- Non-nil assertion after a manual check.
local function getOrThrow(m: map<string, number>, key: string): number
    local v: number | nil = m[key]
    assert(v ~= nil)
    return v!
end

-- Casts for downcasts you have verified.
local function asNumber(v: any): number
    return v as number
end

print(needNumber(nil), needNumber(5))
print(needString(nil), needString("hi"))
print(what("x"), what(1), what({}))
print(isMissing(nil), isMissing("a"))

local scores: map<string, number> = { a = 1 }
print(getOrThrow(scores, "a"))
print(asNumber(10))

-- Member narrowing: `node.next` needs no local copy first.
local function head(n: { v: number, next?: { v: number } }): number
  if n.next == nil then
    return n.v
  end
  return n.next.v
end
print(head({ v = 1 }))

-- Truthiness narrowing (only nil/false are falsy).
local function greet(name: string | nil): string
  if name then
    return name
  end
  return "stranger"
end
print(greet(nil), greet("ada"))
