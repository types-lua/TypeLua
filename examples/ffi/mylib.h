// Example header for `tlua bindgen --header mylib.h --out types/mylib.d.tl --lib FfiLib`.
// Any language that can emit C ABI can ship a header like this;
// the generated .d.tl is the type contract TypeLua checks against.
typedef struct { double x, y; } Point;
typedef enum { Red = 0, Green = 1, Blue = 2 } Color;
int add(int a, int b);
double dist(Point a, Point b);
const char* greet(const char* name);
void* alloc(unsigned long n);
#define MAX_N 128
