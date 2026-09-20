--- TypeLua classes: fields, constructors, methods, statics,
--- inheritance, abstract members, visibility, generics.
--- Compiles to a 5.1-compatible metatable idiom (no runtime library).

-- Abstract base: cannot be instantiated, partial implementation ok.
abstract class Animal {
  name: string,
  x: number = 0,
  constructor(name: string) self.name = name end,
  abstract speak(): string,
  describe(): string return self.name end,
  move(dx: number): void self.x = dx end,
}

-- Concrete subclass: fills the abstract member, chains via super().
class Dog extends Animal {
  private tricks: string[] = {},
  static count: integer = 0,
  constructor(name: string)
    super(name)
    Dog.count = Dog.count + 1
  end,
  speak(): string return "woof" end,
  learn(trick: string): void
    table.insert(self.tricks, trick)
  end,
  loud(): string return "loud " .. super:describe() end,
}

local d = Dog.new("rex")
local d2 = Dog("buddy")
print(d:speak(), d2:speak())
d:move(3)
print(d.x)
d:learn("sit")
print(d:loud(), Dog.count)

-- Subclass instances flow into base-typed slots.
local a: Animal = d
a:move(1)
print(a.x)

-- Generic container with a generic static factory.
class Box<T> {
  v: T,
  constructor(v: T) self.v = v end,
  get(): T return self.v end,
  static wrap<U>(v: U): Box<U> return Box.new(v) end,
}

local b = Box.new(10)
local n: number = b:get()
print(n)
local w = Box.wrap("hi")
print(w:get())

-- Interface conformance is structural over public members.
interface Named {
  name: string,
}
local named: Named = d
print(named.name)

-- Recursive types for linked structures (narrow via a local).
class Node {
  v: number = 0,
  next: Node | nil = nil,
}
local head = Node.new()
head.v = 1
local nx = head.next
if nx ~= nil then print(nx.v) end
