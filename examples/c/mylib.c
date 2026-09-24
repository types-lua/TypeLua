// mylib.c — see mylib.h. Build: sh build.sh (gcc -shared -fPIC).
#include "mylib.h"
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

int my_add(int a, int b) { return a + b; }

double my_dist(MyPoint a, MyPoint b) {
  double dx = a.x - b.x;
  double dy = a.y - b.y;
  return sqrt(dx * dx + dy * dy);
}

const char* my_greet(const char* name) {
  static char buf[256];
  snprintf(buf, sizeof(buf), "hello %s", name ? name : "?");
  return buf;
}

const char* my_color_name(MyColor c) {
  switch (c) {
  case MY_RED: return "red";
  case MY_GREEN: return "green";
  case MY_BLUE: return "blue";
  default: return "unknown";
  }
}

void* my_alloc(unsigned long n) { return malloc(n ? n : 1); }
void my_free(void* p) { free(p); }
