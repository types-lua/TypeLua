// mylib.h — single source of truth for examples/c.
// C ABI surface consumed by TypeLua via ffi (see mylib.tl shim).
// Regenerate the contract after editing:
//   tlua bindgen --header mylib.h --out mylib.d.tl --lib CMyLib
#ifndef MYLIB_H
#define MYLIB_H

typedef struct { double x, y; } MyPoint;
typedef enum { MY_RED = 0, MY_GREEN = 1, MY_BLUE = 2 } MyColor;

int my_add(int a, int b);
double my_dist(MyPoint a, MyPoint b);
const char* my_greet(const char* name);
const char* my_color_name(MyColor c);
void* my_alloc(unsigned long n);
void my_free(void* p);

#define MYLIB_MAX_N 128
#define MYLIB_VERSION "1.0.0"

#endif
