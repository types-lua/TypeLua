# TypeLua

[![CI](https://github.com/types-lua/TypeLua/actions/workflows/ci.yml/badge.svg)](https://github.com/types-lua/TypeLua/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/types-lua/TypeLua)](https://github.com/types-lua/TypeLua/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Docs](https://img.shields.io/badge/docs-types--lua.github.io%2Fdocs-5b5bd6)](https://types-lua.github.io/docs/)

**带静态类型的 Lua 超集，Go 实现。** 用 Lua 的写法获得类型、泛型、接口、类、
宏与 Go 风格并发；编译到地道 Lua，类型在发射时擦除。默认目标 Lua 5.4，同时支持
5.1/LuaJIT 兼容模式。

[English](README.md) · [文档](https://types-lua.github.io/docs/) · [语言规范](SPEC.md)

```lua
type Point = { x: number, y: number }

local function dist(a: Point, b: Point): number
  local dx: number = a.x - b.x
  local dy: number = a.y - b.y
  return dx * dx + dy * dy
end

enum Color { Red = "red", Blue = "blue" }
interface Named { name: string }

class Dog implements Named {
  name: string,
  constructor(name: string) self.name = name end,
  speak(): string return "woof" end,
}

local p: Point = { x = 3, y = 4 }
local d = Dog.new("rex")
print(dist(p, { x = 0, y = 0 }), Color.Red, d:speak())  --> 25 red woof
```

## 为什么选 TypeLua

- **是超集，不是新语言。** 合法 Lua 5.4 程序就是合法 TypeLua。类型可选，有则检查，
  发射时擦除（只有 `class`/`enum` 带运行时值）。
- **渐进类型，不挡路。** 不写注解即 `any`；`strictNilChecks` 下通过 `== nil`、
  `type(x)`、字面量、真值与成员路径把 `T | nil` 收窄。
- **真正的泛型与类型编程。** 约束、默认值、条件类型与 `infer`、索引访问、
  映射类型与 `keyof`。
- **可以发布的库。** 包内相对导入（`import x from "./x"`）、依赖清单、MVS 解析、
  vendor，以及 C/FFI 的 `.d.tl` 类型边界。
- **一个二进制，一套工具链。** 编译器、语言服务器、测试运行器、示例运行器、
  包管理器都是 `tlua`。
- **产物可预测。** 默认发射 Lua 5.4，指定时兼容 5.1/LuaJIT，类型零运行时开销。

## 安装

预编译二进制见 [Releases](https://github.com/types-lua/TypeLua/releases)。也可以
从源码构建（需要 Go 1.27+）：

```bash
git clone https://github.com/types-lua/TypeLua.git
cd TypeLua
go build -o tlua ./cmd/tlua
./tlua version
```

## 快速开始

```bash
tlua init            # 生成 tlua.json + main.tl
tlua check           # 类型检查（有错退出码 1）
tlua test            # 发现 tests/**，运行命名用例
tlua example         # 列出示例（cargo 风格）
tlua example basic   # 运行一个示例
tlua run main.tl     # 内嵌解释器直接运行，无需安装 Lua
tlua build           # 发射 dist/*.lua
tlua build --watch   # 或 tlua watch：监听改动增量编译
```

`tlua run` 会把程序擦除类型后写入临时目录，用内嵌 `golua` 执行。FFI 与原生模块
需要外部解释器：

```bash
tlua run --target 5.1 --lua luajit main.tl
```

## 命令行

| 命令 | 作用 |
|---|---|
| `tlua init [--dir .]` | 创建 `tlua.json` 与示例 `main.tl` |
| `tlua check [files...]` | 类型检查；`--format=json`、`--watch` |
| `tlua test [files...]` | 发现并运行测试；`--filter`、`--format=json`、`--lua`、`--target` |
| `tlua example [name]` | 列出或运行 `examples/*.tl` / `examples/*/main.tl` |
| `tlua run <entry.tl>` | 发射并执行；`--check`、`--lua`、`--target`、`--format=json` |
| `tlua build [files...]` | 发射 Lua；`--outDir`、`--target 5.4\|5.1`、`--sourceMap`、`--treeShake`、`--format=json`、`--watch` |
| `tlua watch` | 常驻增量编译（`build` 的旗标，除 `--format`） |
| `tlua mod <cmd>` | 包管理：`init`、`get`、`fetch`、`vendor`、`list`、`graph` |
| `tlua bindgen` | 由 C 头文件生成 `.d.tl` 契约与 typed shim |
| `tlua lsp` | stdio 语言服务器 |
| `tlua version` | 打印版本 |

诊断码稳定：`TL1xxx` 词法、`TL2xxx` 语法、`TL3xxx` 类型检查、`TL37xx` 宏、
`TL38xx` 并发、`TL4xxx` 工程。`--format=json` 每条诊断输出
`{file,line,column,endLine,endColumn,severity,code,category,message}`。

## tlua.json

```json
{
  "compilerOptions": {
    "target": "5.4",
    "strict": true,
    "strictNilChecks": true,
    "outDir": "dist",
    "rootDir": "."
  },
  "include": ["**/*.tl"],
  "exclude": ["dist"]
}
```

| 选项 | 默认 | 含义 |
|---|---|---|
| `target` | `"5.4"` | `"5.1"` 会剥离 `<const>`/`<close>` 并隐藏 5.4-only API |
| `strict` | `true` | 严格可赋值规则 |
| `strictNilChecks` | 跟随 `strict` | `nil` 不可赋给非可选类型 |
| `noImplicitAny` | `false` | 未注解参数报错 |
| `outDir` / `rootDir` | `dist` / `.` | 输出布局 |
| `sourceMap` | `false` | 生成 `<out>.lua.map`（V3 + sourcesContent） |
| `treeShake` | `false` | 删除零引用的纯本地函数 |
| `types` / `typeRoots` | 全部 ambient / `types`、`tlua_modules/@types` | `.d.tl` 白名单与根目录 |

## 语言特性

类型：`any`、`unknown`、`never`、`nil`、`boolean`、`number`、`integer`、
`float`、`string`、`table`、`function`、`thread`、`userdata`、`cdata`、
`ctype`，字面量、数组（`T[]`）、`map<K,V>`、元组、联合、交叉、可选（`T?`）、
对象、函数类型与多返回值（`-> (A, B)`）。

```lua
-- 窄化
local function label(x: number | string | nil): string
  if x == nil then return "none" end
  if type(x) == "string" then return "str:" .. x end
  return "num:" .. tostring(x + 1)
end

-- 泛型与条件类型
type Element<T> = T extends (infer U)[] ? U : T
local function first<T>(xs: T[]): T | nil return xs[1] end

-- defer/errdefer（LIFO 清理，仅显式 error 触发 errdefer）
local function read(): number
  defer print("cleanup")
  return 1
end
```

- `interface` 按结构兼容，`class` 实例按名义兼容；类支持单继承、接口、可见性、
  `static`、`readonly`、`abstract`。
- `macro` 是卫生 AST 宏：`macro twice(x) = (x) + (x)`。
- 并发是协程上的单线程 CSP：`async`/`await`、`spawn`、`chan<T>`、`select`、
  `WaitGroup`、`Mutex`。
- `x as T` 要求任一方向可赋；`x!` 去掉 `nil`；`!=` 是 `~=` 的别名。

## 模块与包

```lua
import "side-effect"                  -- 副作用 require
import Greet from "example.com.greet" -- 推断模块类型
import Api: { hello(n: string): string } from "api"  -- 冻结边界
```

多文件库内部使用包内相对导入：

```lua
-- lib/init.tl
import codec from "./codec"   -- 解析 lib/codec.tl，发射为 require("lib.codec")
return { add = codec.add }
```

```lua
-- 消费者
import L from "example.com.lib"
print(L.add(1, 2))
```

相对导入按**引用文件所在目录**解析（`./x` → `x.tl`，再 `x/init.tl`），发射为
稳定模块名，因此多文件库无论 vendor 到哪个路径都不需要改 `package.path`。
目标缺失、越出工程、指向声明文件、模块名冲突均报 `TL4002`。

包就是 git 仓库，地址即路径（`github.com/owner/repo[/sub]`），由 `tlua.toml` +
`tlua.lock` 锁定（MVS、`exact` pin、本地 `path` 依赖）：

```toml
[package]
name = "myapp"

[dependencies]
"github.com/foo/bar" = "^v1.2.0"
"example.com/local" = { path = "./deps/local" }
```

```bash
tlua mod get && tlua mod vendor
tlua check && tlua build
```

vendor 源码位于 `tlua_modules/<path>/`，自动参与检查、发射与运行时加载。

## 测试

`tlua test` 是零配置运行器：发现 `tests/` 下的文件（以及 `*_test.tl` /
`*.test.tl`），先做全量类型检查，注入断言模块，再逐个用例隔离运行。

```lua
import T from "tlua_test"

return {
  ["整数相加"] = function(): void
    T.eq(1 + 1, 2)
  end,
  ["非法输入抛错"] = function(): void
    T.raises(function(): void error("boom") end)
  end,
}
```

```bash
tlua test
tlua test --filter 相加
tlua test --format=json             # NDJSON：每用例一行 + 汇总
tlua test --lua lua5.4
tlua test --lua luajit --target 5.1
```

断言：`T.eq`、`T.eqBytes`、`T.ok`、`T.raises`、`T.fail`。失败会输出
`模块 / 用例: 信息`，进程非零退出。

## 示例

```bash
tlua example                # 列出示例及其首行 --- 说明
tlua example basic          # 运行 examples/basic.tl 或 examples/basic/main.tl
tlua example basic -- a b   # 透传脚本参数
tlua example basic --check
```

运行器通过工程根部的包装入口执行，因此子目录示例也能正常导入工程模块。仓库自带
25 个示例，覆盖 `basic`、`ffi`、`leveldb-ffi`、`concurrent`、`macros`、
`treeshake` 等。

## 编辑器支持

`tlua lsp` 通过 stdio 提供 LSP：诊断、悬停、跳转定义、补全（含 `.`/`:` 成员）、
引用、重命名、文档符号，增量同步。

VS Code：[`types-lua/vscode-typelua`](https://github.com/types-lua/vscode-typelua)
提供语法高亮、代码片段与语言客户端。

## FFI 与原生代码

```lua
local ffi = require("ffi")
ffi.cdef[[ int add(int a, int b); ]]

type CLib = { add(a: integer, b: integer): integer }
local C = ffi.load("mylib") as CLib
print(C.add(20, 22))
```

`require("ffi")` 在未被遮蔽时是精确类型；`cdef` 字符串不透明。可由 C 头生成契约：

```bash
tlua bindgen --header mylib.h --out types/mylib.d.tl --shim mylib.tl --lib MyLib
```

FFI 需要 LuaJIT（`--target 5.1 --lua luajit`）。`.d.tl` 只检查不发射；原生资产
（`*.lua` shim、`*.so/.dylib/.dll`、`*.a`）会透传到 `dist` 与 vendor。

## 工程结构

```
cmd/tlua/            CLI：init/check/test/example/run/build/watch/mod/bindgen/lsp
internal/token,lexer 词法（长串、注释、全部数值形式）
internal/ast,parser  递归下降 + Pratt 解析器，带错误恢复
internal/types       类型表示与子类型判定
internal/checker     作用域、窄化、泛型、类、FFI、并发
internal/emitter     类型擦除发射（可选 source map）
internal/compiler    程序管线、增量会话、模块解析
internal/config      tlua.json 与文件发现
internal/toml,mod    TOML 子集与去中心化包管理
internal/diag        稳定诊断注册表（TLxxxx）
internal/lsp         stdio 语言服务器
internal/expand,shake 宏与树摇
internal/watch,sourcemap 文件监听与 source map 编码
examples/            25 个可运行示例，全部通过类型检查
```

## 质量

- `go test ./...` 与 `go vet ./...` 保持全绿；CI 还会自检全部示例并做测试运行器冒烟。
- 解析器错误恢复、256 层嵌套保护、发射后再解析回环。
- 诊断集中在 `internal/diag/codes.go`，文档表格由注册表生成。

## 文档

- 中英双语用户指南 + 面向 agent 的 `llms.txt`：
  <https://types-lua.github.io/docs/>
- 语言契约：[SPEC.md](SPEC.md)
- 编译器内部文档：[docs/](docs/)
- 本仓库的 agent 启动页：[AGENTS.md](AGENTS.md)

## 贡献

新语法需要 解析器 + 检查器 + 发射器 + 示例 + 测试 + SPEC 章节。提交前运行
`go test ./...`、`go vet ./...`、`tlua check` 与 `tlua test`。不要自造
`TLxxxx` 码：先加入 `internal/diag/codes.go`（注册表测试会保证文档同步）。

## 许可证

MIT，见 [LICENSE](LICENSE)。
