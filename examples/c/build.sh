#!/bin/sh
# Build the C example library (needs gcc; runtime needs LuaJIT).
# Single source of truth is mylib.h — regenerate both artifacts after editing:
#   tlua bindgen --header mylib.h --out mylib.d.tl --shim mylib.tl --lib CMyLib
set -e
cd "$(dirname "$0")"
gcc -shared -fPIC -O2 -o libmylib.so mylib.c -lm
echo "built libmylib.so"
