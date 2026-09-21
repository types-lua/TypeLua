--- TypeLua stdlib: table/string/math/io/os are `any`-typed shims.
--- They never produce false positives; annotate your own boundaries.

local t: string[] = { "b", "a", "c" }
table.sort(t)
print(table.concat(t, ","))

local s: string = "  hello  "
print(string.upper(s))
print(string.len(s))
print(string.format("%q-%d", s, 7))

print(math.floor(3.7))
print(math.max(1, 9, 3))
print(math.floor(10 / 3), 10 // 3, 10 % 3)

-- Generic helpers stay typed even when stdlib is dynamic.
local function join(arr: string[], sep: string): string
    return table.concat(arr, sep) as string
end

print(join({ "a", "b" }, "+"))

-- IO is dynamic; guard results before typing them.
local f = io.open("/tmp/tlua-demo.txt", "w")
if f ~= nil then
    local fh = f as any
    fh:write("hi")
    fh:close()
end
print(os.time(), os.date("%Y-%m-%d"))

-- Coroutines are precisely typed (unlike the any-typed shims above).
local co = coroutine.create(function()
  coroutine.yield("step")
end)
local ok, v = coroutine.resume(co)
local st = coroutine.status(co)
if st == "dead" then
  print("done")
else
  print("live")
end
local w = coroutine.wrap(function(a: number) return a * 2 end)
print(ok, v, w(21))
