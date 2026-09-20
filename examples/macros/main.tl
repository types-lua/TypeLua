--- Hygienic macros: expanded (and erased) before checking.
--- Locals introduced by a body never capture caller variables.

macro twice(x) = (x) + (x)

macro unless(cond, body) do
  if not (cond) then
    body
  end
end

macro sum3(a, b, c) = (a) + (b) + (c)

local n = twice(21)
print(n)

local t = 5
unless(t > 10, print("small"))
print(sum3(1, 2, 3))

-- Hygiene: the body's `v` is renamed per expansion.
local v = "caller"
macro shadowing(x) do
  local v = 1
  print(v)
  print(x)
end
shadowing(v)
print(v)
