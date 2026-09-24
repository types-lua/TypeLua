--- Cross-language contract: the library author ships two files.
--- 1. Runtime adapter (this file, plain Lua): hides HOW the foreign code
---    loads (ffi.load / luaopen_* C module / subprocess IPC / wasm). Any
---    language works as long as it can present a `require`-able Lua table.
--- 2. Type contract (native.d.tl): WHAT the table holds, checked by tlua.
--- Swap the body for a C module or IPC without touching callers (they only
--- see NativeLib). Pure-Lua fallback keeps `lua dist/...` runnable without
--- LuaJIT; real packages ffi.load their vendored .so here.
local ok, ffi = pcall(require, "ffi")
if ok and ffi and ffi.cdef then
  ffi.cdef[[
int native_add(int a, int b);
const char* native_hello(const char* name);
]]
  local okLoad, lib = pcall(ffi.load, "native")
  if okLoad and lib then
    return lib
  end
end
-- Fallback (also the shape a C/IPC backend must honor).
local M = {}
function M.native_add(a, b) return a + b end
function M.native_hello(name) return "hello " .. name end
return M
