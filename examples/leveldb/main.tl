--- LevelDB end-to-end: typed wrapper over FFI (LuaJIT runtime).
--- Build the libs: sh build.sh (needs /home/tang/leveldb, cmake+gcc).
--- Check: tlua check. Run (LuaJIT): luajit dist/examples/leveldb/main.lua.

import DB from "leveldb"

local function main(): void
  print("leveldb major version:", DB.version())
  local path = "leveldb_demo.db"
  -- Fresh start on every run (ignore "does not exist" on first run).
  DB.destroy(path)
  local db, openErr = DB.open(path)
  if openErr ~= nil then
    print("open failed:", openErr)
    return
  end
  if db == nil then
    print("open failed: no handle")
    return
  end

  local ok, putErr = DB.put(db, "k1", "v1")
  if not ok then
    print("put failed:", putErr)
    return
  end
  local ok2, _ = DB.put(db, "k2", "v2")
  local ok3, __ = DB.put(db, "empty", "")
  if not ok2 or not ok3 then
    print("put failed")
    return
  end

  local v, getErr = DB.get(db, "k1")
  if getErr ~= nil then
    print("get failed:", getErr)
    return
  end
  print("k1 =", v)

  local missing, _merr = DB.get(db, "nope")
  if missing == nil then
    print("missing key -> nil")
  end

  local keys, vals, scanErr = DB.scan(db)
  if scanErr ~= nil then
    print("scan failed:", scanErr)
    return
  end
  if keys == nil or vals == nil then
    print("scan failed: no result")
    return
  end
  for i = 1, #keys do
    print("scan:", keys[i], vals[i])
  end

  local dok, delErr = DB.delete(db, "k1")
  if not dok then
    print("delete failed:", delErr)
    return
  end
  local gone, _gerr = DB.get(db, "k1")
  if gone == nil then
    print("deleted key -> nil")
  end

  DB.close(db)
  print("done")
end

main()
