# TypeLua — 有类型的 Lua（TypeScript for Lua），Go 实现

Lua 超集 + 静态类型 + 擦除编译到地道 Lua；另有小体积 `class` 运行时（5.1 兼容元表）。目标：**功能完整、行为可预测、少 bug**。

## 快速开始

```bash
go build -o tlua ./cmd/tlua
./tlua init
./tlua check
./tlua build   # -> dist/*.lua
./tlua build --watch   # 或 ./tlua watch：监听改动持续增量编译（对标 tsc --watch）
```

示例见 `examples/*/`（32 个文件，`tlua check` 全过），规范见 `SPEC.md`（v0.8：§15 FFI/§16 跨语言）。

| 示例 | 覆盖 |
|---|---|
| `basic` | 端到端入门：类型/接口/枚举/泛型/窄化 |
| `types` | 基元/字面量/联合/交叉/数组/map/元组/函数类型/`typeof`/`keyof` |
| `generics` | 泛型函数/别名/约束/默认值/嵌套`>>` |
| `interfaces` | 结构化、`extends`、可选/只读、方法简写、递归接口、联合体访问 |
| `enums` | 字符串/数字自增枚举、穷举分支 |
| `functions` | 多返回值、变参、闭包、方法定义/调用 |
| `narrowing` | `nil`/`type()`/真值/提前返回/`not`/`as`/`!`/成员路径 |
| `modules` | `return M` + `require as T` 边界注解 |
| `oop` | 无`class`：接口+元表（`__index`/`setmetatable`） |
| `stdlib` | `table/string/math/io/os`（动态垫片+自建类型边界） |
| `lua54` | `<const>`/`<close>`/`goto`/位运算/`//`/长串 |
| `compat` | 5.1/LuaJIT 兼容子集（`--target 5.1` 验证） |
| `class` | 类：继承/抽象/可见性/静态/泛型/`super`（真机运行验证） |
| `conditional` | 条件类型/`infer`：分发、元组/函数/对象/类模式（真机运行验证） |
| `macros` | 卫生宏：表达式/语句宏、变长 `...`、遮蔽与捕获防护（真机运行验证） |
| `advanced` | 显式泛型实参、映射类型（含 `+?`/`-?`/`+readonly`/`-readonly` 与同态继承）、索引访问（真机运行验证） |
| `pkgs` | 包管理：直接 + 间接依赖、`replace` 本地替换、vendor（离线可验） |
| `defer` | `defer`/`errdefer`：LIFO 清理、正常返回与显式 `error()` 分流 |
| `concurrent` | 并发：`async/await` + `spawn` + `chan<T>` + `select`/`default`/超时 + `WaitGroup`/`Mutex`（单线程协作 CSP，真机运行验证） |
| `ffi` | FFI：`require("ffi")` + `cdata/ctype` + `cdef/load as CLib`（`bindgen` 由 `mylib.h` 生成契约） |
| `native` | 跨语言：`init.lua` shim 藏差异 + 全局 `.d.tl` 定边界（任意语言经 C ABI 接入） |
| `c` | C 端到端：`.h/.c` + `build.sh`（gcc）+ `bindgen` 契约 + 一次封装的 typed shim，调用方 `import` 零 cast |

## tlua.json

```json
{
  "compilerOptions": { "target": "5.4", "strict": true, "outDir": "dist", "rootDir": ".", "types": [], "typeRoots": ["types", "tlua_modules/@types"] },
  "include": ["**/*.tl"],
  "exclude": ["dist"]
}
```

`target`: `5.4`（默认）或 `5.1`（剥离 `<const>`/`<close>`，LuaJIT 用 5.1）。`types`/`typeRoots` 对标 `tsconfig`：全局 `.d.tl`（只检查不发射）；`tlua bindgen --header foo.h --out types/foo.d.tl --shim foo.tl` 由 C 头一次生成契约与 typed shim（头文件为唯一源）。

## 语言速览

```lua
type Point = { x: number, y: number }
local function dist(a: Point, b: Point): number
  return (a.x-b.x)^2 + (a.y-b.y)^2
end
local p: Point = { x = 1, y = 2 }
enum Color { Red = "red", Blue = "blue" }
local c: Color = Color.Red
local x: number | nil = nil
if x ~= nil then print(x + 1) end

class Dog extends Animal {
  constructor(name: string) super(name) end,
  speak(): string return "woof" end,
}
local d = Dog.new("rex")
```

## 工程结构（对标 tsc）

- `internal/token,lexer`：词法（含长串/注释/数值全集）
- `internal/ast,parser`：递归下降 + Pratt + 错误恢复
- `internal/types,checker`：结构子类型/泛型推断/窄化
- `internal/emitter,config,compiler`：擦除发射 + `tlua.json` + 管线
- `internal/toml,mod`：TOML 子集 + 去中心化包管理（manifest/lock/MVS/git 拉取/缓存/vendor）
- `cmd/tlua`：`init/check/build/version/lsp`（stdio 语言服务器：诊断/悬停/定义/补全/引用/重命名，增量同步）；
  `check/build --watch`（及 `watch` 子命令）：轮询 + 去抖的持续编译，改动（含新增/删除/`tlua.json`）自动重编，删源自动清陈旧产物，坏配置/单轮崩溃不退出

## 质量

- `go test ./...`，`go vet ./...`
- 解析器错误恢复（嵌套上限 + 空容忍后级）+ 自举（`examples/`）+ 发射回环（emit 后仍可解析）
