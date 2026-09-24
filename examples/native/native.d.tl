--- Type contract for examples/native (global .d.tl, like tsconfig types).
--- Unique names (Native*) avoid colliding with other examples' ambient.
interface NativeLib {
  native_add(a: integer, b: integer): integer,
  native_hello(name: string): string,
}
