--- TypeLua FFI: LuaJIT C ABI via require("ffi") (any language through C).
--- Run: tlua check (needs no .so) / tlua build --target 5.1
--- Runtime requires LuaJIT; checking works on any target.
--- Any language exposing C ABI works: C/Rust/Zig/Go(c-shared) via
--- ffi.load, or Python/JS via an init.lua shim (see examples/native).

local ffi = require("ffi")

ffi.cdef[[
int add(int a, int b);
typedef struct { double x, y; } Point;
double dist(Point a, Point b);
const char* greet(const char* name);
]]

-- Per-library contract: describe the C namespace you actually use.
-- Generate the skeleton with: tlua bindgen --header mylib.h --out types/mylib.d.tl
type FfiPoint = { x: number, y: number }
interface FfiLib {
  add(a: integer, b: integer): integer,
  dist(a: FfiPoint, b: FfiPoint): number,
  greet(name: string): string,
}

local C = ffi.load("mylib") as FfiLib
print(C.add(2, 3))
print(C.dist({ x = 1, y = 2 }, { x = 4, y = 6 }))
print(C.greet("tlua"))

-- Opaque pointers stay distinct from numbers/strings (no silent mixing).
local buf: cdata = ffi.new("char[?]", 64)
ffi.fill(buf, 64)
local ct: ctype = ffi.typeof("char[?]")
print(ffi.string(buf, 4), ct)
local p: cdata = ffi.cast("void *", buf)
local owned: cdata = ffi.gc(p, nil)
print(owned)
