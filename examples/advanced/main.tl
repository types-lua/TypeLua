--- Explicit generic arguments, mapped types and indexed access.
--- Run: tlua check / tlua build (types erase; runtime is plain values).

-- Explicit instantiation beats inference when you want it pinned.
local function id<T>(x: T): T return x end
local a = id<string>("hi")
local b = id(42)

local function withDef<T, U = number>(x: T): U
  return nil as any
end
local w: number = withDef<string>("hi")

-- Generic methods, including on classes and interfaces.
interface Mapper {
  map<U>(x: number, f: (n: number) -> U): U,
}
-- NOTE: plain function-valued fields are called with dot (no
-- implicit self); only class methods (colon definitions) use colon.
local function useMapper(m: Mapper): string
  return m.map<string>(1, function(n: number) return "n" end)
end

class Box<T> {
  v: T,
  constructor(v: T) self.v = v end,
  get(): T return self.v end,
}
local boxed = Box.new<string>("hi")
local also = Box<string>("hi")
print(boxed:get(), also:get(), a, b, w)

-- Mapped types build object shapes from key domains.
type Point = { x: number, y: number }
type PartialPoint = { [K in keyof Point]?: Point[K] }
type PickPoint = { [K in "x"]: Point[K] }
type Scores = { [K in string]: number }

local p: PartialPoint = { x = 1 }
local pk: PickPoint = { x = 2 }
local sc: Scores = {}
sc["alice"] = 100
print(p.x, pk.x, sc["alice"])

-- Indexed access reads member types back out.
local px: Point["x"] = 3
type First2 = [string, number]
local fn2: First2[number] = 42
print(px, fn2)

-- Generic mapped utilities compose.
type Pick2<T, K> = { [P in K]: T[P] }
local sub: Pick2<Point, "y"> = { y = 4 }
print(sub.y)
print(useMapper({
  map = function<U>(x: number, f: (n: number) -> U): U return f(x) end,
}))
print(useMapper({
  map = function(x: number, f: any): any return f(x) end,
}))
