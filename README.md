# TypeLua — 有类型的 Lua（TypeScript for Lua），Go 实现

Lua 超集 + 静态类型 + 擦除编译到地道 Lua；另有小体积 `class` 运行时（5.1 兼容元表）。目标：**功能完整、行为可预测、少 bug**。

## 快速开始

```bash
go build -o tlua ./cmd/tlua
./tlua init
./tlua check
./tlua test    # discover tests/**, run named cases (built-in assertions)
./tlua build   # -> dist/*.lua
./tlua build --watch   # 或 ./tlua watch：监听改动持续增量编译（对标 tsc --watch）
./tlua run main.tl [args...]  # 直接运行：默认只解析不卡类型，内嵌 golua，无需安装 lua
./tlua example                # 列出 examples/（cargo 风格），首次行注释即说明
./tlua example basic          # 运行 examples/basic.tl 或 examples/basic/main.tl
```

示例见 `examples/*/`（39 个文件，`tlua check` 全过），规范见 `SPEC.md`（v0.8：§15 FFI/§16 跨语言）。

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
| `leveldb-ffi` | 真实 C 库端到端（LuaJIT）：`leveldb.h` 精简子集 + `bindgen` 契约/raw shim + 手写安全 wrapper（errptr/所有权/迭代器全收敛）+ `build.sh`（cmake，由 `/home/tang/leveldb` 产出 `.so` 直接跑 + `.a` 透传；真机运行验证，含 NUL 二进制探针） |
| `leveldb-54` | 同一演示的原生 5.4 形态：`lvl54.c` → `leveldb54.so`（`luaopen_leveldb54`，静态嵌入，自包含）+ 手写契约 + `main.tl`（`lua5.4 dist/examples/leveldb-54/main.lua`，输出与 LuaJIT 一致） |
| `treeshake` | 树摇：死纯函数 `--treeShake` 掉（560→229 字节），输出与默认构建逐字节同行为 |

## tlua.json

```json
{
  "compilerOptions": { "target": "5.4", "strict": true, "outDir": "dist", "rootDir": ".", "types": [], "typeRoots": ["types", "tlua_modules/@types"] },
  "include": ["**/*.tl"],
  "exclude": ["dist"]
}
```

`target`: `5.4`（默认）或 `5.1`（剥离 `<const>`/`<close>`，LuaJIT 用 5.1）。`types`/`typeRoots` 对标 `tsconfig`：全局 `.d.tl`（只检查不发射）；`tlua bindgen --header foo.h --out types/foo.d.tl --shim foo.tl` 由 C 头一次生成契约与 typed shim（头文件为唯一源）。`treeShake`（默认关，`build --treeShake`）：删零引用纯本地函数，有副作用的一律保留。

## tlua run（直接运行，不留编译产物）

```bash
./tlua run main.tl -- arg1 arg2   # 内嵌 golua 运行，脚本参数经 arg[1..] 透传（arg[0] 为脚本名）
./tlua run --check main.tl        # 先完整类型检查，报错即停（默认只拦语法错误，类型错误照跑）
./tlua run --lua lua5.4 main.tl   # 用外部解释器（lua5.4/luajit…），等价于 lua dist/main.lua
./tlua run --target 5.1 main.tl   # 目标版本默认和 build 一致（tlua.json，无则 5.4）
```

- 默认内嵌 `golua`（纯 Go 实现，零外部依赖）：转译结果写入临时目录后在进程内执行，退出自动清理；多文件 `require`、包自举（`package.path` 前缀 + 点式 vendor searcher）、`concurrent` 调度器照常工作；工程外的单文件自动退化为单文件程序。
- 限制：内嵌模式跑不了 `require("ffi")` 与 `.so/.dll/.a` C 模块（golua 无 C 模块加载器）——这类工程用 `--lua luajit`（配 `--target 5.1`）。

## 包内相对导入

多文件库不再需要手写 `package.path` 补丁：`import` 支持相对路径，编译器会解析成稳定的运行期模块名。

```lua
-- lib/init.tl
import codec from "./codec"     -- 编译为 require("lib.codec")
return { add = codec.add }
```

```lua
-- 消费者
import L from "example.com.lib" -- 无论包被 vendor 到哪个依赖键
```

`./x` 先找 `x.tl`、再找 `x/init.tl`；目标是工程根内的普通 `.tl`，发射名相对工程根（vendored 包相对 `tlua_modules/`）。缺失、越界、指向 `.d.tl`、或与另一文件重名（`a.b.tl` vs `a/b.tl`）都报 `TL4002`。`require("./x")` 没有相对语义，会被拒绝并提示改用 `import`。

## tlua example（cargo 风格的示例）

```bash
./tlua example                 # 列出 examples/*.tl 与 examples/*/main.tl
./tlua example basic           # 运行；子目录示例也能直接 import 工程根模块
./tlua example basic -- arg1   # 透传脚本参数
./tlua example basic --check   # 先完整类型检查
./tlua example basic --lua lua5.4
```

示例文件首行 `--- 说明` 会成为列表里的描述。命令通过工程根部的包装入口执行，
因此 `examples/`、`tests/` 里的文件都能按普通模块导入根目录的库。

## tlua test（零配置测试）

测试文件放在 `tests/`（或命名 `*_test.tl` / `*.test.tl`），返回一组命名用例：

```lua
import T from "tlua_test"

return {
  ["adds numbers"] = function(): void
    T.eq(1 + 1, 2)
  end,
  ["rejects bad input"] = function(): void
    T.raises(function(): void error("boom") end)
  end,
}
```

```bash
./tlua test                     # 内嵌 golua，先类型检查再运行
./tlua test --filter adds       # 只跑名字含 adds 的用例
./tlua test --format=json       # 每行一个 JSON 对象，便于 CI/agent
./tlua test --lua lua5.4        # 换解释器
./tlua test --lua luajit --target 5.1
```

`tlua_test` 模块由命令自动注入到 `tlua_modules/tlua_test.tl`，无需安装；
断言失败会带上 `模块 / 用例` 名称，进程以非零码退出。

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
- `cmd/tlua`：`init/check/build/run/version/lsp`（stdio 语言服务器：诊断/悬停/定义/补全/引用/重命名，增量同步）；
  `check/build --watch`（及 `watch` 子命令）：轮询 + 去抖的持续编译，改动（含新增/删除/`tlua.json`）自动重编，删源自动清陈旧产物，坏配置/单轮崩溃不退出

## 质量

- `go test ./...`，`go vet ./...`
- 解析器错误恢复（嵌套上限 + 空容忍后级）+ 自举（`examples/`）+ 发射回环（emit 后仍可解析）
