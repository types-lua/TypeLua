--- Typed C shim (the only boundary): cdef + load + cast happen once here.
--- Callers just `import C from "mylib"` — no casts, no cdef strings.
--- Contract `CMyLib` comes from mylib.d.tl (regen: see mylib.h header).
--- Runtime: LuaJIT + libmylib.so beside this file (sh build.sh).

local ffi = require("ffi")

ffi.cdef[[
typedef struct { double x, y; } MyPoint;
typedef enum { MY_RED = 0, MY_GREEN = 1, MY_BLUE = 2 } MyColor;
int my_add(int a, int b);
double my_dist(MyPoint a, MyPoint b);
const char* my_greet(const char* name);
const char* my_color_name(MyColor c);
void* my_alloc(unsigned long n);
void my_free(void* p);
]]

-- .so sits beside this file in both source and dist layouts.
local function libDir(): string
  local info = debug.getinfo(1, "S")
  local src = info.source
  if string.sub(src, 1, 1) == "@" then
    src = string.sub(src, 2)
  end
  local dir = string.match(src, "^(.*/)")
  if dir == nil then
    return "./"
  end
  return dir
end

local M = ffi.load(libDir() .. "libmylib.so") as CMyLib
return M
