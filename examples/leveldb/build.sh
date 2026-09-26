#!/bin/sh
# Build the leveldb example libraries (needs gcc/cmake; runtime needs LuaJIT).
# LevelDB itself lives at /home/tang/leveldb (clone, v1.23); override with:
#   LEVELDB_SRC=/path/to/leveldb sh build.sh
# Copies the shared library (ffi.load at runtime) and the static archive
# (.a passthrough artifact) next to the example. Regenerate the contract
# after editing leveldb.h:
#   tlua bindgen --header leveldb.h --out leveldb_raw.d.tl --shim leveldb_raw.tl --lib LevelDBRaw
set -e
cd "$(dirname "$0")"
SRC="${LEVELDB_SRC:-../../../leveldb}"
if [ ! -f "$SRC/include/leveldb/c.h" ]; then
  echo "leveldb sources not found at $SRC (set LEVELDB_SRC)" >&2
  exit 1
fi
if [ ! -f "$SRC/build/libleveldb.so.1.23.0" ]; then
  cmake -S "$SRC" -B "$SRC/build" -DCMAKE_BUILD_TYPE=Release \
    -DBUILD_SHARED_LIBS=ON -DLEVELDB_BUILD_TESTS=OFF -DLEVELDB_BUILD_BENCHMARKS=OFF
  cmake --build "$SRC/build" -j"$(nproc)" --target leveldb
fi
if [ ! -f "$SRC/build-static/libleveldb.a" ]; then
  cmake -S "$SRC" -B "$SRC/build-static" -DCMAKE_BUILD_TYPE=Release \
    -DBUILD_SHARED_LIBS=OFF -DLEVELDB_BUILD_TESTS=OFF -DLEVELDB_BUILD_BENCHMARKS=OFF
  cmake --build "$SRC/build-static" -j"$(nproc)" --target leveldb
fi
cp "$SRC/build/libleveldb.so.1.23.0" ./libleveldb.so
cp "$SRC/build-static/libleveldb.a" ./libleveldb.a
echo "built libleveldb.so + libleveldb.a"
