--- defer (all normal exits, LIFO) and errdefer (explicit error() only).
local function open(name: string): number
  print("open", name)
  defer print("close", name)
  errdefer print("abort", name)
  if name == "" then
    return error("empty")
  end
  return 1
end

local function outer(): number
  defer print("outer-done")
  local function inner(): number
    defer print("inner-done")
    return 1
  end
  return inner()
end

print(open("db"))
print(outer())
