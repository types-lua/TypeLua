# TypeLua 语言规范 v0.6

TypeLua 是 Lua 的类型超集（对标 TypeScript 之于 JavaScript）：类型注解可擦除，编译产物为地道 Lua（默认 5.4，兼容 5.1/LuaJIT）。`class` 是唯一的例外：它生成小体积元表样板（5.1 兼容）。

## 1. 设计原则（参考 TypeScript）

- 超集：合法 Lua 5.4 程序即合法 TypeLua（`type`/`class` 等为**上下文关键字**，`type(x)` 仍是内建调用）。
- 擦除：所有类型注解编译后删除；`enum`/`class` 例外（有运行时值）。
- 混合类型：`interface`/`type` 按形状兼容；`class` 实例按名义兼容（同类或子类），对接口按形状兼容。
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
    | Check extends Extends ? True : False  -- 条件类型（右结合）
    | infer U                          -- 仅 extends 子句内有效
    | T[K]                             -- 索引访问（K 为字面量并、keyof 结果等）
    | { (+readonly|-readonly)? [K in Keys](+?|-?|?)? : V }  -- 映射类型
    | typeof(x) | keyof T
```

- `integer`/`float` <: `number`。
- `T?` = `T | nil`；`strictNilChecks` 下 `nil` 不可赋给其他类型。
- 函数多返回值：`-> (A, B)`；调用时仅末尾表达式可展开。
- `map<K,V>` 点访问：`m.field` 要求 `K` 接受 `string`。
- 条件类型：裸类型参数检查具分发性（`El<string|number[]>` 逐成员求值）；`infer` 在数组/元组/函数/对象/类模式中绑定。
- 含未定类型参数的条件类型保持延迟，实例化（或 `Substitute` 具体化）时求值；延迟状态下子类型保持宽松（泛型代码不误报）。
- 索引访问要求键在目标中全部存在（联合两侧逐成员解析）；缺键、`boolean` 等不可映射的域一律报错。
- 联合体点访问/方法调用逐成员分发：全员具名始可访问（类型取并、可选/只读/可见性取严），缺员或 `nil` 成员整体报错（先窄化）。
- 映射的域须为字面量并、`keyof` 结果或 `string/number/integer`（后者生成索引签名）；`?`/`+?` 使字段可选，`readonly`/`+readonly` 使字段只读。
- `keyof` 上的映射具同态性：无标记时继承基对象字段的可选/只读；`-?` 强制必填，`-readonly` 强制可写（`Required`/`Mutable` 即此二者）。
- 非 `keyof` 的映射为绝对语义（标记决定一切，无标记即必填可写）。
- 递归别名与接口：`interface Node { v: number, next?: Node }`、`type List<T> = { head: T, tail: List<T> | nil }`、互递归皆可；直接自引用（`type A = A`、非对象结果）与自喂养实例化（`L<L<T>>` 类，64 层上限）报 `TL3106`。

## 3. 声明

```lua
type Point = { x: number, y: number }
type List<T> = { head: T, tail: List<T> | nil }
type Element<T> = T extends (infer U)[] ? U : T
type Head<T> = T extends [infer H, ...unknown[]] ? H : never

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
- 泛型：`<T>`, `<T extends C>`, `<T = Default>`；调用时推断，也支持显式 `f<T>()`（含 `obj:m<T>()`、`C.new<T>()`、`C<T>()`；不足位用推断/默认值补齐，超位与约束违反报错）。
- 属性：`local x <const> = 1`，`local h <close> = ...`（`--target 5.1` 时剥离）。

## 4. 类（class 运行时）

```lua
abstract class Animal {
  name: string,
  constructor(name: string) self.name = name end,
  abstract speak(): string,
  describe(): string return self.name end,
}
class Dog extends Animal implements Named {
  private tricks: string[] = {},
  static count: integer = 0,
  constructor(name: string) super(name) end,
  speak(): string return "woof" end,
}
local d = Dog.new("rex")   -- 或 Dog("rex")（__call）
```

- 单继承 `extends` + 多 `implements`（接口须结构满足）。
- 名义类型：子类实例可赋给基类；无关类即使同形状也不兼容。
- 修饰符：`public`（默认）/`private`/`protected`/`static`/`readonly`/`abstract`，编译期 enforcing，运行时擦除。
- `super(...)` 仅构造器调基类构造；`super:m(...)` 调基类方法（均改写为显式表调用）。
- 构造器规则：必填字段须初始化或在构造器赋值；派生构造器按需调 `super()`，`super()` 前不可用 `self`。
- 抽象类不可实例化（含运行时 guard）；具体子类须实现全部抽象成员。
- 泛型类：`class Box<T>`，实例化时推断，`Box<number>` 参与子类型。
- `class` 恒为块作用域（发射 `local`）；`declare class` 无发射。

## 5. 表达式扩展

- `x as T`：双向可赋其一即通过，否则报错。
- `x!`：去除 `nil`。
- `!=` 为 `~=` 别名，发射时统一为 `~=`。

## 6. 窄化

- `if x == nil / x ~= nil`（含提前 `return` 后的流分析）。
- `if type(x) == "string" | "number" | ...`。
- `not`、`and`（真分支）、`or`（假分支）组合（`and` 同根多成员顺序合成）。
- 真值窄化：`if x then` 去 `nil`/`false`（`boolean` 收敛为字面量），`else` 侧留 `nil`/`false`；`any` 不动。
- 以上对成员路径同样成立：`if node.next then return node.next.v end` 无需先存入局部变量（对象/类实例逐层求精）。
- 重赋值不重置窄化（与局部变量同限）。

## 7. 编译器

- `tlua.json` 对标 `tsconfig.json`：`compilerOptions.{target,strict,strictNilChecks,noImplicitAny,outDir,rootDir,sourceMap}` + `include/exclude/files`。
- 管线：lex → parse（错误恢复）→ check（含宏展开）→ emit，与 `tsc` 同构。
- 诊断码：`TL1xxx` 词法、`TL2xxx` 语法（含 `TL203x` 类成员、`TL2038` 宏形参、`TL2041` 宏变长位置、`TL2015` 嵌套过深）、`TL3xxx` 类型（含 `TL3234` 显式实参、`TL3602` 映射域、`TL3603` 索引访问、`TL37xx` 宏展开）、`TL4xxx` 工程。
- 嵌套上限 256 层（表达式/类型/块分别计数），超限报 `TL2015` 并截断恢复；`?` 与条件分支的歧义由推测性前瞻消解（`? True :` 归条件，否则归可选）。
- 恢复兜底：残缺 AST 绝不击垮后级——checker/emitter 对空节点宽容（报错已由 parser 记录）。
- `.d.tl` 只检查不发射。

## 8. 宏（卫生 AST 宏）

```lua
macro twice(x) = (x) + (x)              -- 表达式宏
macro unless(cond, body) do             -- 语句宏
  if not (cond) then
    body                                -- 裸形参即拼接位
  end
end
macro wrap(f, ...) = f(...)             -- 变长：多余实参收集于 `...`
```

- 检查前展开、用后擦除；支持嵌套调用、前向引用与块作用域（函数内定义的宏仅函数内可见）。
- 卫生：宏内局部绑定逐次重命名（形参同名遮蔽亦处理），调用方标识符永不捕获；标签同步重命名；`local class` 等价冗余接受，`export/declare macro` 拒绝。
- 语句宏只能出现在语句位置、表达式宏位的值位置混用报错；参数个数须精确匹配（变长宏要求至少具名参数个数）；递归展开报错；重复求值风险（如 `twice(f())` 求值两次）由宏作者负责，宏体条件/循环建议给参数加括号（`not` 优先级坑）。
- 变长 `...` 须为末位形参（`TL2041`）；体内的 `...` 仅可在调用实参位拼接（`f(...)`，零个时拼为空），嵌套函数内的 `...` 归属该函数；他处出现报 `TL3706`。
- 限制：宏形参仅值层面（不可用于类型位置）。

## 9. 源码映射与语言服务器

- `tlua build --sourceMap`（或 `sourceMap: true`）输出 `<out>.lua.map`（V3 JSON，自带 `sourcesContent`）并在产物尾加 `--# sourceMappingURL=` 注释；映射为语句粒度 + 具名函数名。
- `tlua lsp`（stdio）：诊断推送、悬停、跳转定义、补全（含可见变量/类型/关键字，`ident.`/`ident:` 后为成员补全）、引用、重命名、文档符号；同步为增量式（`textDocumentSync: 2`，批量/多行/Unicode 编辑全量重析）。
- 重命名/引用覆盖值符号、类型名（别名/接口/枚举/类，类与枚举连带值孪生）、成员（类方法与字段、接口字段、枚举成员，含 `self.x` 与赋值目标；合成的 `new` 不参与）。
- 成员跟踪含 `obj["field"]` 字符串索引（引号保留）与注解赋值/局部/return 处的表字面量键（含方法键与嵌套）；跨属同名成员仅访问可用，改名要求一致归属。
- 同名遮蔽拒绝半截改名（值被无关类型遮蔽、或反之，明确报错）；成员改名要求有记录在案的定义。

## 10. 非目标（v0.6）

- 结构（匿名）对象成员的改名/引用（无声明归属，不跟踪）。
- 函数实参/多返回值位置的表字面量实现键（仅注解赋值、`local`、`return` 跟踪）；`[[...]]` 长字符串键不跟踪。
- 递归类型的打印截断（回指显示名，如 `Node{ next?: Node }`）；错误信息中超深类型的冗长。
- 泛型约束/默认值中的自引用；64 层实例化上限内的误伤（源码手写 64 层以上嵌套实例化）。
- 成员补全不过滤 `private`；链式 `a.b.` 只解析末级标识符（单标识符）；补全不对 `any` 展开。
