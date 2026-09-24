#!/bin/sh
# Build the C example library (needs gcc; runtime needs LuaJIT).
# Regenerate the type contract after editing mylib.h:
#   tlua bindgen --header mylib.h --out mylib.d.tl --lib CMyLib
set -e
cd "$(dirname "$0")"
gcc -shared -fPIC -O2 -o libmylib.so mylib.c -lm
echo "built libmylib.so"
