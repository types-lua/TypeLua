#!/bin/sh
# Build the LevelDB Lua 5.4 C module (needs gcc/cmake + lua5.4 headers;
# runtime needs stock lua5.4). LevelDB itself lives at /home/tang/leveldb
# (clone, v1.23); override with: LEVELDB_SRC=/path/to/leveldb sh build.sh
# Products (all stay local, never committed):
#   leveldb54.so — Lua C module (luaopen_leveldb54), self-contained: the
#                  PIC static archive is linked in, so ldd shows only
#                  system libraries. Loaded via require("leveldb54").
# The contract (leveldb54.d.tl) is handwritten (bindgen targets FFI).
set -e
cd "$(dirname "$0")"
SRC="${LEVELDB_SRC:-../../../leveldb}"
if [ ! -f "$SRC/include/leveldb/c.h" ]; then
  echo "leveldb sources not found at $SRC (set LEVELDB_SRC)" >&2
  exit 1
fi
# PIC static archive for embedding into the shared C module (a plain .a may
# carry non-PIC relocations, which cannot link into a .so).
if [ ! -f "$SRC/build-static-pic/libleveldb.a" ]; then
  cmake -S "$SRC" -B "$SRC/build-static-pic" -DCMAKE_BUILD_TYPE=Release \
    -DBUILD_SHARED_LIBS=OFF -DCMAKE_POSITION_INDEPENDENT_CODE=ON \
    -DLEVELDB_BUILD_TESTS=OFF -DLEVELDB_BUILD_BENCHMARKS=OFF
  cmake --build "$SRC/build-static-pic" -j"$(nproc)" --target leveldb
fi
LUA_INC="$(pkg-config --cflags lua5.4 2>/dev/null || echo -I/usr/include/lua5.4)"
# shellcheck disable=SC2086
gcc -shared -fPIC -O2 -o ./leveldb54.so ./lvl54.c \
  -I"$SRC/include" $LUA_INC "$SRC/build-static-pic/libleveldb.a" \
  -lstdc++ -lpthread
echo "built leveldb54.so"
