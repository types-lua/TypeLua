--- Tree-shaking demo: pure helpers nobody calls vanish with --treeShake.
--- Run: tlua check (always clean) / tlua build [--treeShake]
---   lua dist/main.lua            # identical output either way
--- DEAD below is dropped only with --treeShake; default build keeps all.

--- DEAD: never called.
local function unusedHelper(name: string): string
  return "unused " .. name
end

--- DEAD: closure-value form, never called.
local deadCb = function(msg: string): string
  return "cb " .. msg
end

--- DEAD: transitive chain; nothing reaches it.
local function deadLeaf(x: integer): integer
  return x * 2
end

local function deadMid(x: integer): integer
  return deadLeaf(x) + 1
end

--- DEAD: recursion alone is not a use.
local function deadFact(n: integer): integer
  if n <= 1 then
    return 1
  end
  return n * deadFact(n - 1)
end

--- LIVE: called by the main flow.
local function greet(name: string): string
  return "hello " .. name
end

--- LIVE: module boundary (table field) must survive.
local Ops = {}

function Ops.double(x: integer): integer
  return x * 2
end

--- LIVE: passed as a callback value.
local function apply(f: (x: integer) -> integer, v: integer): integer
  return f(v)
end

print(greet("tlua"))
print(apply(Ops.double, 20))
print("done")
