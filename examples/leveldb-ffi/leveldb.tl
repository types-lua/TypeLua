--- Safe LevelDB wrapper over the raw FFI binding (leveldb_raw.tl).
--- Only this file touches cdata/errptr mechanics; callers use plain
--- strings and (value, errmsg) pairs. Ownership summary:
---   * errptr holders are fresh per call (LevelDB leaves them untouched
---     on success, so reuse would leak stale errors).
---   * leveldb_get buffers are copied to Lua strings then leveldb_free'd.
---   * iterator key/value pointers are borrowed: copied before next().
---   * DB owns handle + wopt + ropt; close() destroys all three.

local ffi = require("ffi")

import Raw from "leveldb_raw"

type DB = { handle: cdata, wopt: cdata, ropt: cdata }

-- Fresh (pointer, any-view) errptr pair. The any-view exists because
-- indexing cdata is not typed; passing either view to a cdata parameter
-- checks clean in both directions.
local function newErr(): (cdata, any)
  local p: cdata = ffi.new("char*[1]")
  local a: any = p
  return p, a
end

-- Drain an errptr view: nil when clean, otherwise the message (buffer
-- freed exactly once, on the error path only).
local function errMsg(a: any): string | nil
  if a[0] == nil then
    return nil
  end
  local msg: string = ffi.string(a[0])
  Raw.leveldb_free(a[0])
  return msg
end

local function open(path: string): (DB | nil, string | nil)
  local opt: cdata = Raw.leveldb_options_create()
  Raw.leveldb_options_set_create_if_missing(opt, 1)
  local errp, erra = newErr()
  local h: any = Raw.leveldb_open(opt, path, errp)
  Raw.leveldb_options_destroy(opt)
  local msg = errMsg(erra)
  if msg ~= nil then
    return nil, msg
  end
  if h == nil then
    return nil, "open returned NULL"
  end
  local wopt: cdata = Raw.leveldb_writeoptions_create()
  local ropt: cdata = Raw.leveldb_readoptions_create()
  return { handle = h, wopt = wopt, ropt = ropt }, nil
end

local function close(db: DB): void
  Raw.leveldb_close(db.handle)
  Raw.leveldb_writeoptions_destroy(db.wopt)
  Raw.leveldb_readoptions_destroy(db.ropt)
end

local function put(db: DB, key: string, val: string): (boolean, string | nil)
  local errp, erra = newErr()
  Raw.leveldb_put(db.handle, db.wopt, key, #key, val, #val, errp)
  local msg = errMsg(erra)
  if msg ~= nil then
    return false, msg
  end
  return true, nil
end

local function get(db: DB, key: string): (string | nil, string | nil)
  local errp, erra = newErr()
  local lenp: cdata = ffi.new("size_t[1]")
  local lena: any = lenp
  local p: any = Raw.leveldb_get(db.handle, db.ropt, key, #key, lenp, errp)
  local msg = errMsg(erra)
  if msg ~= nil then
    return nil, msg
  end
  if p == nil then
    return nil, nil
  end
  local s: string = ffi.string(p, lena[0])
  Raw.leveldb_free(p)
  return s, nil
end

local function delete(db: DB, key: string): (boolean, string | nil)
  local errp, erra = newErr()
  Raw.leveldb_delete(db.handle, db.wopt, key, #key, errp)
  local msg = errMsg(erra)
  if msg ~= nil then
    return false, msg
  end
  return true, nil
end

-- Full ordered scan collected into Lua arrays (no live iterator escapes,
-- so no use-after-destroy is possible).
local function scan(db: DB): (string[], string[], string | nil)
  local keys: string[] = {}
  local vals: string[] = {}
  local it: cdata = Raw.leveldb_create_iterator(db.handle, db.ropt)
  local kp: cdata = ffi.new("size_t[1]")
  local ka: any = kp
  local vp: cdata = ffi.new("size_t[1]")
  local va: any = vp
  Raw.leveldb_iter_seek_to_first(it)
  while Raw.leveldb_iter_valid(it) ~= 0 do
    local k: string = ffi.string(Raw.leveldb_iter_key(it, kp), ka[0])
    local v: string = ffi.string(Raw.leveldb_iter_value(it, vp), va[0])
    keys[#keys + 1] = k
    vals[#vals + 1] = v
    Raw.leveldb_iter_next(it)
  end
  local errp, erra = newErr()
  Raw.leveldb_iter_get_error(it, errp)
  Raw.leveldb_iter_destroy(it)
  local msg = errMsg(erra)
  if msg ~= nil then
    return {}, {}, msg
  end
  return keys, vals, nil
end

local function destroy(path: string): (boolean, string | nil)
  local opt: cdata = Raw.leveldb_options_create()
  local errp, erra = newErr()
  Raw.leveldb_destroy_db(opt, path, errp)
  Raw.leveldb_options_destroy(opt)
  local msg = errMsg(erra)
  if msg ~= nil then
    return false, msg
  end
  return true, nil
end

local function version(): integer
  return Raw.leveldb_major_version()
end

local M = {
  open = open,
  close = close,
  put = put,
  get = get,
  delete = delete,
  scan = scan,
  destroy = destroy,
  version = version,
}
return M
