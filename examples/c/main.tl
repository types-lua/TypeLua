--- C end-to-end (the elegant shape): one typed shim, zero casts here.
--- Build the lib: sh build.sh (gcc). Check: tlua check.
--- Run (LuaJIT): luajit dist/examples/c/main.lua (after tlua build).

import C from "mylib"

print(C.my_add(20, 22))

local p: MyPoint = { x = 1, y = 2 }
local q: MyPoint = { x = 4, y = 6 }
print(C.my_dist(p, q))
print(C.my_greet("tlua"))
print(C.my_color_name(1))
print(C.MYLIB_MAX_N, C.MYLIB_VERSION)

local buf: cdata = C.my_alloc(64)
C.my_free(buf)
