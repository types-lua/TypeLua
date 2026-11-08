# TypeLua

[![CI](https://github.com/types-lua/TypeLua/actions/workflows/ci.yml/badge.svg)](https://github.com/types-lua/TypeLua/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/types-lua/TypeLua)](https://github.com/types-lua/TypeLua/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Docs](https://img.shields.io/badge/docs-types--lua.github.io%2Fdocs-5b5bd6)](https://types-lua.github.io/docs/)

**A typed superset of Lua, implemented in Go.** Write Lua with static types,
generics, interfaces, classes, macros and Go-style concurrency; compile to
idiomatic Lua with the types erased. Default target is Lua 5.4, with a
5.1/LuaJIT-compatible mode.

[中文版](README.zh-CN.md) · [Documentation](https://types-lua.github.io/docs/) · [SPEC](SPEC.md)

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

## Why TypeLua

- **A superset, not a new language.** Every valid Lua 5.4 program is valid
  TypeLua. Types are optional, checked when present, and erased on emit
  (`class`/`enum` are the only runtime-bearing constructs).
- **Gradual typing that stays out of the way.** No annotation means `any`;
  `strictNilChecks` narrows `T | nil` through `== nil`, `type(x)`, literals,
  truthiness and member paths.
- **Real generics and type-level programming.** Constraints, defaults,
  conditional types with `infer`, indexed access, mapped and keyof-based types.
- **Libraries you can ship.** Package-local imports (`import x from "./x"`),
  dependency manifests, MVS resolution, vendoring, and typed `.d.tl`
  boundaries for C/FFI.
- **One binary, one toolchain.** Compiler, language server, test runner,
  example runner and package manager are all `tlua`.
- **Predictable output.** Emitted Lua is 5.4 by default, 5.1/LuaJIT-compatible
  on request, with no runtime shim for types.

## Install

Prebuilt binaries are published on the
[Releases](https://github.com/types-lua/TypeLua/releases) page. Or build from
source (Go 1.27+):

```bash
git clone https://github.com/types-lua/TypeLua.git
cd TypeLua
go build -o tlua ./cmd/tlua
./tlua version
```

## Quick start

```bash
tlua init            # tlua.json + main.tl
tlua check           # type-check (exit 1 on errors)
tlua test            # discover tests/**, run named cases
tlua example         # list examples (cargo-style)
tlua example basic   # run one example
tlua run main.tl     # execute in the embedded interpreter (no Lua needed)
tlua build           # emit dist/*.lua
tlua build --watch   # or: tlua watch — incremental rebuild on change
```

`tlua run` type-erases into a temp directory and executes with the embedded
`golua` runtime. FFI and native modules need an external interpreter:

```bash
tlua run --target 5.1 --lua luajit main.tl
```

## CLI

| Command | Purpose |
|---|---|
| `tlua init [--dir .]` | Create `tlua.json` and an example `main.tl` |
| `tlua check [files...]` | Type-check; `--format=json`, `--watch` |
| `tlua test [files...]` | Discover and run tests; `--filter`, `--module`, `--case`, `--list`, `--format=json`, `--lua`, `--target` |
| `tlua example [name]` | List or run `examples/*.tl` / `examples/*/main.tl` |
| `tlua run <entry.tl>` | Emit + execute; `--check`, `--lua`, `--target`, `--format=json` |
| `tlua build [files...]` | Emit Lua; `--outDir`, `--target 5.4\|5.1`, `--sourceMap`, `--treeShake`, `--format=json`, `--watch` |
| `tlua watch` | Long-running incremental build (`build` flags except `--format`) |
| `tlua mod <cmd>` | Packages: `init`, `get`, `fetch`, `vendor`, `list`, `graph` |
| `tlua bindgen` | Generate a `.d.tl` contract + typed shim from a C header |
| `tlua lsp` | Language server over stdio |
| `tlua dap` | Debug adapter (DAP) over stdio; used by the VS Code debugger |
| `tlua version` | Print the version |

Diagnostics are stable `TLxxxx` codes: `TL1xxx` lexer, `TL2xxx` parser,
`TL3xxx` checker, `TL37xx` macros, `TL38xx` concurrency, `TL4xxx` project.
`--format=json` prints `{file,line,column,endLine,endColumn,severity,code,category,message}`
per diagnostic.

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

| Option | Default | Meaning |
|---|---|---|
| `target` | `"5.4"` | `"5.1"` strips `<const>`/`<close>` and hides 5.4-only APIs |
| `strict` | `true` | Strict assignability rules |
| `strictNilChecks` | follows `strict` | `nil` is not assignable to non-optional types |
| `noImplicitAny` | `false` | Report unannotated parameters |
| `outDir` / `rootDir` | `dist` / `.` | Output layout |
| `sourceMap` | `false` | Emit `<out>.lua.map` (V3 + sourcesContent) |
| `treeShake` | `false` | Drop zero-reference pure local functions |
| `types` / `typeRoots` | all ambient / `types`, `tlua_modules/@types` | Ambient `.d.tl` allowlist and roots |

## Language

Types: `any`, `unknown`, `never`, `nil`, `boolean`, `number`, `integer`,
`float`, `string`, `table`, `function`, `thread`, `userdata`, `cdata`,
`ctype`, literals, arrays (`T[]`), maps (`map<K,V>`), tuples, unions,
intersections, optional (`T?`), object types, function types and multi-returns
(`-> (A, B)`).

```lua
-- Narrowing
local function label(x: number | string | nil): string
  if x == nil then return "none" end
  if type(x) == "string" then return "str:" .. x end
  return "num:" .. tostring(x + 1)
end

-- Generics and conditional types
type Element<T> = T extends (infer U)[] ? U : T
local function first<T>(xs: T[]): T | nil return xs[1] end

-- Defer/errdefer (LIFO cleanup, explicit errors only)
local function read(): number
  defer print("cleanup")
  return 1
end
```

- `interface` is structural; `class` instances are nominal. `class` supports
  single inheritance, interfaces, visibility, `static`, `readonly` and
  `abstract`.
- `macro` is a hygienic AST macro: `macro twice(x) = (x) + (x)`.
- Concurrency is single-threaded CSP on coroutines: `async`/`await`,
  `spawn`, `chan<T>`, `select`, `WaitGroup`, `Mutex`.
- `x as T` requires one assignable direction; `x!` strips `nil`;
  `!=` is an alias of `~=`.

## Modules and packages

```lua
import "side-effect"                  -- require for effect
import Greet from "example.com.greet" -- inferred module type
import Api: { hello(n: string): string } from "api"  -- frozen boundary
```

Inside a multi-file library, use package-local imports:

```lua
-- lib/init.tl
import codec from "./codec"   -- lib/codec.tl; emitted as require("lib.codec")
return { add = codec.add }
```

```lua
-- consumer
import L from "example.com.lib"
print(L.add(1, 2))
```

Relative imports resolve against the requiring file (`./x` → `x.tl`, then
`x/init.tl`) and are emitted as stable module names, so multi-file libraries
work from any vendor path without patching `package.path`. Missing targets,
paths escaping the project, declaration targets and name collisions are
`TL4002`.

Packages are git repositories addressed by path (`github.com/owner/repo[/sub]`),
pinned by `tlua.toml` + `tlua.lock` (MVS, `exact` pins, local `path` deps):

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

Vendored sources live in `tlua_modules/<path>/` and are checked, emitted and
loaded at runtime automatically.

## Testing

`tlua test` is a zero-configuration runner: it discovers files under `tests/`
(and `*_test.tl` / `*.test.tl`), type-checks the project, injects a small
assertion module and runs each named case in isolation.

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
tlua test
tlua test --filter adds
tlua test --format=json             # NDJSON, one object per case + summary
tlua test --lua lua5.4
tlua test --lua luajit --target 5.1
```

Assertions: `T.eq`, `T.eqBytes`, `T.ok`, `T.raises`, `T.fail`. Failures report
`module / case: message` and the process exits non-zero.

## Examples

```bash
tlua example                # list examples with their first `---` line
tlua example basic          # run examples/basic.tl or examples/basic/main.tl
tlua example basic -- a b   # pass script arguments
tlua example basic --check
```

The runner uses a project-root wrapper, so examples in subdirectories can
import project modules normally. The repository ships 25 examples, from
`basic` to `ffi`, `leveldb-ffi`, `concurrent`, `macros` and `treeshake`.

## Editor support

`tlua lsp` speaks LSP over stdio: diagnostics, hover, go to definition,
completion (including `.`/`:` members), references, rename and document
symbols, with incremental sync.

VS Code: [`types-lua/vscode-typelua`](https://github.com/types-lua/vscode-typelua)
provides syntax highlighting, snippets, the language client, a Test Explorer
and breakpoint debugging through `tlua dap`.

## FFI and native code

```lua
local ffi = require("ffi")
ffi.cdef[[ int add(int a, int b); ]]

type CLib = { add(a: integer, b: integer): integer }
local C = ffi.load("mylib") as CLib
print(C.add(20, 22))
```

`require("ffi")` is typed when not shadowed; `cdef` strings are opaque.
Generate contracts from C headers:

```bash
tlua bindgen --header mylib.h --out types/mylib.d.tl --shim mylib.tl --lib MyLib
```

FFI needs LuaJIT (`--target 5.1 --lua luajit`). `.d.tl` files are checked but
never emitted; native assets (`*.lua` shims, `*.so/.dylib/.dll`, `*.a`) are
passed through to `dist` and vendor.

## Project layout

```
cmd/tlua/            CLI: init/check/test/example/run/build/watch/mod/bindgen/lsp
internal/token,lexer Lexer (long strings, comments, all numeric forms)
internal/ast,parser  Recursive descent + Pratt parser with error recovery
internal/types       Type representation and subtyping
internal/checker     Scopes, narrowing, generics, classes, FFI, concurrency
internal/emitter     Type erasure to Lua (+ optional source maps)
internal/compiler    Program pipeline, incremental sessions, module resolution
internal/config      tlua.json handling and file discovery
internal/toml,mod    TOML subset and decentralized package manager
internal/diag        Stable diagnostic registry (TLxxxx)
internal/lsp         stdio language server
internal/expand,shake Macros and tree shaking
internal/watch,sourcemap  File watching and source map encoding
examples/            25 runnable examples, all type-checked
```

## Quality

- `go test ./...` and `go vet ./...` stay green; CI runs both plus a self-check
  of all examples and a runner smoke test.
- Parser error recovery, 256-level nesting guard, and emit/re-parse round-trips.
- Diagnostics are centralized in `internal/diag/codes.go`; the documentation
  table is generated from the registry.

## Documentation

- User guide, bilingual, with `llms.txt` for agents:
  <https://types-lua.github.io/docs/>
- Language contract: [SPEC.md](SPEC.md)
- Compiler internals: [docs/](docs/)
- Agent bootstrap for this repository: [AGENTS.md](AGENTS.md)

## Contributing

New syntax needs parser + checker + emitter + an example + tests + a SPEC
section. Run `go test ./...`, `go vet ./...`, `tlua check` and
`tlua test` before sending a change. Never invent a `TLxxxx` code: add it to
`internal/diag/codes.go` (the registry test keeps docs in sync).

## License

MIT. See [LICENSE](LICENSE).
