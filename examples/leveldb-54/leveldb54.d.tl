-- Contract for the Lua 5.4 C module (lvl54.c -> leveldb54.so,
-- entry luaopen_leveldb54). Hand-written: bindgen targets the LuaJIT FFI
-- path (see leveldb_raw.d.tl); this module speaks plain C API instead.
-- DB handles are opaque userdata (never indexed, only passed back).
-- Usage: import N: LevelDB54 from "leveldb54"

interface LevelDB54 {
  open(path: string) -> (userdata | nil, string | nil),
  close(db: userdata) -> (boolean, string | nil),
  put(db: userdata, key: string, val: string) -> (boolean, string | nil),
  get(db: userdata, key: string) -> (string | nil, string | nil),
  delete(db: userdata, key: string) -> (boolean, string | nil),
  scan(db: userdata) -> (string[] | nil, string[] | nil, string | nil),
  destroy(path: string) -> (boolean, string | nil),
  version() -> integer,
}
