# TypeLua 语言规范 v0.1

TypeLua 是 Lua 的类型超集（对标 TypeScript 之于 JavaScript）：**类型可擦除、无运行时开销**，编译产物为地道 Lua（默认 5.4，兼容 5.1/LuaJIT）。

## 1. 设计原则（参考 TypeScript）

- 超集：合法 Lua 5.4 程序即合法 TypeLua（`type` 等为**上下文关键字**，`type(x)` 仍是内建调用）。
- 擦除：所有类型注解编译后删除；`enum` 例外（生成 Lua 表，因为有运行时值）。
- 结构化类型：`interface`/`type` 按形状兼容，不引入 `class` 运行时。
- 渐进：无注解即 `any`；`strictNilChecks` 默认开，可逐步收紧。

## 2. 类型

```
T ::= any | unknown | never | void | nil
    | boolean | number | integer | float | string | table | function | thread | userdata
    | "lit" | 42 | true | false        -- 字面量类型
    | Ident<T,...>                     -- 具名/泛型实例（含 map<K,V>, Array<T>）
    | T[]                              -- 数组
    | T | U                            -- 联合
    | T & U                            -- 交叉
    | T?                               -- 可选 = T | nil
    | [A, B]                           -- 元组
    | { f: T, g?: U, [K]: V, m(a: T): R }  -- 表类型
    | (x: T, y?: U, ...rest: V[]) -> R -- 函数类型
    | (T) | (A, B)                     -- 分组/多返回值（仅 -> 后）
    | typeof(x) | keyof T
```

- `integer`/`float` <: `number`。
- `T?` = `T | nil`；`strictNilChecks` 下 `nil` 不可赋给其他类型。
- 函数多返回值：`-> (A, B)`；调用时仅末尾表达式可展开。
- `map<K,V>` 点访问：`m.field` 要求 `K` 接受 `string`。

## 3. 声明

```lua
type Point = { x: number, y: number }
type List<T> = { head: T, tail: List<T> | nil }

interface Named { name: string, greet(msg: string): string }
interface Employee extends Named { id: integer }

enum Color { Red = "red", Green = "green", Blue = "blue" }
enum N { A, B = 5, C }  -- A=0, B=5, C=6

local x: number = 1
local s: string | nil = nil
local f: (x: number) -> string
local function id<T>(v: T): T return v end

function M.foo(a: number): number return a end  -- `:`/`->` 均可
declare function external(s: string): number    -- .d.tl，无发射
```

- `export`/`declare` 仅修饰声明；`declare` 无发射。
- 泛型：`<T>`, `<T extends C>`, `<T = Default>`；调用时推断（暂不支持显式 `f<T>()`）。
- 属性：`local x <const> = 1`，`local h <close> = ...`（`--target 5.1` 时剥离）。

## 4. 表达式扩展

- `x as T`：双向可赋其一即通过，否则报错。
- `x!`：去除 `nil`。
- `!=` 为 `~=` 别名，发射时统一为 `~=`。

## 5. 窄化

- `if x == nil / x ~= nil`（含提前 `return` 后的流分析）。
- `if type(x) == "string" | "number" | ...`。
- `not`、`and`（真分支）、`or`（假分支）组合。

## 6. 编译器

- `tlua.json` 对标 `tsconfig.json`：`compilerOptions.{target,strict,strictNilChecks,noImplicitAny,outDir,rootDir}` + `include/exclude/files`。
- 管线：lex → parse（错误恢复）→ check → emit，与 `tsc` 同构。
- 诊断码：`TL1xxx` 词法，`TL2xxx` 语法，`TL3xxx` 类型，`TL4xxx` 工程。
- `.d.tl` 只检查不发射。

## 7. 非目标（v0.1）

- `class` 运行时、`infer`/条件类型、显式泛型实参、宏、源码映射、LSP（后续版本）。
