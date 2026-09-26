// lvl54.c — Lua 5.4 C module for LevelDB (SPEC shape A: luaopen_*).
// The LuaJIT path (leveldb_raw.tl, ffi.load) cannot run on stock Lua 5.4
// (no ffi module; luaffifb has no ARM64 call stubs); this module exposes
// the same operations through the plain C API so one demo runs on 5.4.
// Typed contract: leveldb54.d.tl. Build: sh build.sh (needs lua5.4 headers
// and /home/tang/leveldb; links the PIC static archive, so leveldb54.so
// is self-contained — verify with `ldd leveldb54.so`).
//
// Safety rules (every function):
//   - DB handles are full userdata with a metatable + __gc safety net;
//     close() nulls the handle, so double close / close-after-gc can only
//     report "already closed", never free twice.
//   - All keys/values cross via explicit lengths (luaL_checklstring /
//     lua_pushlstring): embedded NULs round-trip.
//   - Every leveldb error buffer is freed exactly once, on the error path.
//   - Iterator key/value pointers are borrowed: copied before iter_next().
//   - The scan iterator is destroyed on all paths (success and error).

#include <lua.h>
#include <lauxlib.h>
#include <lualib.h>

#include <leveldb/c.h>
#include <stddef.h>

#define LDB_MT "TypeLua.LevelDB"

typedef struct {
  leveldb_t *db;
  leveldb_writeoptions_t *wo;
  leveldb_readoptions_t *ro;
  int closed;
} ldb_t;

static ldb_t *check_db(lua_State *L, int idx) {
  return (ldb_t *)luaL_checkudata(L, idx, LDB_MT);
}

/* Stack: (nil, errmsg). Call only with err != NULL. */
static int push_level_error(lua_State *L, char *err) {
  lua_pushnil(L);
  lua_pushstring(L, err);
  leveldb_free(err);
  return 2;
}

static int push_closed(lua_State *L) {
  lua_pushnil(L);
  lua_pushstring(L, "already closed");
  return 2;
}

static void close_handle(ldb_t *u) {
  if (!u->closed) {
    u->closed = 1;
    if (u->db) {
      leveldb_close(u->db);
      u->db = NULL;
    }
    if (u->wo) {
      leveldb_writeoptions_destroy(u->wo);
      u->wo = NULL;
    }
    if (u->ro) {
      leveldb_readoptions_destroy(u->ro);
      u->ro = NULL;
    }
  }
}

static int l_open(lua_State *L) {
  const char *path = luaL_checkstring(L, 1);
  leveldb_options_t *opt = leveldb_options_create();
  leveldb_options_set_create_if_missing(opt, 1);
  char *err = NULL;
  leveldb_t *db = leveldb_open(opt, path, &err);
  leveldb_options_destroy(opt);
  if (err != NULL) {
    return push_level_error(L, err);
  }
  if (db == NULL) {
    lua_pushnil(L);
    lua_pushstring(L, "open returned NULL");
    return 2;
  }
  ldb_t *u = (ldb_t *)lua_newuserdata(L, sizeof(ldb_t));
  u->db = db;
  u->wo = leveldb_writeoptions_create();
  u->ro = leveldb_readoptions_create();
  u->closed = 0;
  luaL_getmetatable(L, LDB_MT);
  lua_setmetatable(L, -2);
  lua_pushnil(L); /* second value: no error */
  return 2;
}

static int l_close(lua_State *L) {
  ldb_t *u = check_db(L, 1);
  if (u->closed) {
    return push_closed(L);
  }
  close_handle(u);
  lua_pushboolean(L, 1);
  return 1;
}

static int l_put(lua_State *L) {
  ldb_t *u = check_db(L, 1);
  if (u->closed) {
    return push_closed(L);
  }
  size_t klen = 0, vlen = 0;
  const char *k = luaL_checklstring(L, 2, &klen);
  const char *v = luaL_checklstring(L, 3, &vlen);
  char *err = NULL;
  leveldb_put(u->db, u->wo, k, klen, v, vlen, &err);
  if (err != NULL) {
    return push_level_error(L, err);
  }
  lua_pushboolean(L, 1);
  return 1;
}

static int l_get(lua_State *L) {
  ldb_t *u = check_db(L, 1);
  if (u->closed) {
    return push_closed(L);
  }
  size_t klen = 0;
  const char *k = luaL_checklstring(L, 2, &klen);
  char *err = NULL;
  size_t n = 0;
  char *p = leveldb_get(u->db, u->ro, k, klen, &n, &err);
  if (err != NULL) {
    return push_level_error(L, err);
  }
  if (p == NULL) {
    lua_pushnil(L); /* missing key */
    return 1;
  }
  lua_pushlstring(L, p, n);
  leveldb_free(p);
  return 1;
}

static int l_delete(lua_State *L) {
  ldb_t *u = check_db(L, 1);
  if (u->closed) {
    return push_closed(L);
  }
  size_t klen = 0;
  const char *k = luaL_checklstring(L, 2, &klen);
  char *err = NULL;
  leveldb_delete(u->db, u->wo, k, klen, &err);
  if (err != NULL) {
    return push_level_error(L, err);
  }
  lua_pushboolean(L, 1);
  return 1;
}

static int l_scan(lua_State *L) {
  ldb_t *u = check_db(L, 1);
  if (u->closed) {
    return push_closed(L);
  }
  leveldb_iterator_t *it = leveldb_create_iterator(u->db, u->ro);
  lua_newtable(L); /* keys */
  lua_newtable(L); /* vals */
  lua_Integer n = 0;
  leveldb_iter_seek_to_first(it);
  while (leveldb_iter_valid(it)) {
    size_t klen = 0, vlen = 0;
    const char *k = leveldb_iter_key(it, &klen);
    const char *v = leveldb_iter_value(it, &vlen);
    n++;
    lua_pushlstring(L, k, klen);
    lua_rawseti(L, -3, n); /* keys */
    lua_pushlstring(L, v, vlen);
    lua_rawseti(L, -2, n); /* vals */
    leveldb_iter_next(it);
  }
  char *err = NULL;
  leveldb_iter_get_error(it, &err);
  leveldb_iter_destroy(it);
  if (err != NULL) {
    lua_pop(L, 2); /* drop partial tables */
    return push_level_error(L, err);
  }
  return 2;
}

static int l_destroy(lua_State *L) {
  const char *path = luaL_checkstring(L, 1);
  leveldb_options_t *opt = leveldb_options_create();
  char *err = NULL;
  leveldb_destroy_db(opt, path, &err);
  leveldb_options_destroy(opt);
  if (err != NULL) {
    return push_level_error(L, err);
  }
  lua_pushboolean(L, 1);
  return 1;
}

static int l_version(lua_State *L) {
  lua_pushinteger(L, leveldb_major_version());
  return 1;
}

static int l_gc(lua_State *L) {
  close_handle(check_db(L, 1));
  return 0;
}

static const luaL_Reg funcs[] = {
  {"open", l_open},
  {"close", l_close},
  {"put", l_put},
  {"get", l_get},
  {"delete", l_delete},
  {"scan", l_scan},
  {"destroy", l_destroy},
  {"version", l_version},
  {NULL, NULL}
};

int luaopen_leveldb54(lua_State *L) {
  luaL_newmetatable(L, LDB_MT);
  lua_pushcfunction(L, l_gc);
  lua_setfield(L, -2, "__gc");
  lua_pop(L, 1);
  luaL_newlib(L, funcs);
  return 1;
}
