--- TypeLua native: any language through a require-able shim + .d.tl.
--- Run: tlua check (no .so needed) / tlua build (shim copies to dist).
--- The shim (native_shim.lua) hides ffi vs C-module vs IPC; the contract
--- (native.d.tl, ambient) types the boundary. Swap Rust/Go/C/Python under
--- the shim without touching this caller.

local M = require("native_shim") as NativeLib
print(M.native_add(20, 22))
print(M.native_hello("tlua"))
