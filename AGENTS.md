# AGENTS.md — TypeLua agent bootstrap

TypeLua is a typed superset of Lua (TypeScript-for-Lua), implemented in Go.
Types erase to idiomatic Lua (default 5.4, compatible 5.1/LuaJIT). Only
`class`/`enum` emit runtime values (metatable prelude / tables).

Upstream: `https://github.com/types-lua/TypeLua.git` (branch `main`).
Go module: `github.com/typelua/typelua` (Go >= 1.27).

## Commands (run from repo root)

```bash
go build -o tlua ./cmd/tlua
./tlua init                      # scaffold tlua.json + main.tl
./tlua check                     # type-check (exit 1 on error)
./tlua test                      # discover tests/**, run named cases
./tlua check --format=json       # machine-readable diags for agents
./tlua build                     # emit to dist/*.lua
./tlua build --format=json --target 5.1 --treeShake
./tlua run main.tl -- args...    # parse-only by default, embedded golua
./tlua run --check main.tl       # full check before run
./tlua run --lua luajit main.tl  # FFI / .so modules need external Lua
./tlua example                   # list examples/ (cargo-style)
./tlua example NAME -- args      # run examples/NAME.tl or examples/NAME/main.tl
./tlua test --list               # enumerate cases (JSON for editors)
./tlua test --filter NAME        # run matching cases only
./tlua test --case NAME          # run one exact case
./tlua test --format=json        # one JSON object per line
./tlua lsp                       # stdio language server
./tlua dap                       # stdio debug adapter (used by VS Code)
go test ./...                    # must stay green
go vet ./...                     # must stay clean
```

`tlua check` covers 39 files (`tlua.json` include `examples/**/*.tl`).
JSON diag shape: `{file,line,column,endLine,endColumn,severity,code,category,message}`.
`code` is stable (`TL1xxx` lexer, `TL2xxx` parser, `TL3xxx` checker,
`TL37xx` macros, `TL38xx` concurrency, `TL4xxx` project). `category` is
derived from the code. Exit code is unchanged (`text` default).

## Source of truth

- `SPEC.md` (v0.8, §1–§16) — language contract. Read this before writing `.tl`.
- `README.md` (English, default) / `README.zh-CN.md` (Chinese) — project overview.
- `examples/*/` — 25 runnable programs, all `tlua check`-clean. Copy idioms from here.
- `docs/` — compiler internals (pipeline, subtyping, LSP). Not user docs.
- `internal/diag/codes.go` — full diagnostic registry.
- Language user docs (EN/ZH + llms.txt) live in `https://github.com/types-lua/docs`.

## tlua.json (tsconfig analogue)

`target` 5.4|5.1 (default 5.4), `strict` (default true),
`strictNilChecks` (default follows `strict`), `noImplicitAny` (default false),
`outDir`/`rootDir`, `sourceMap`, `treeShake` (default off),
`types` (ambient `.d.tl` allowlist, empty = all),
`typeRoots` (default `types`, `tlua_modules/@types`),
`include`/`exclude`/`files`. `--target 5.1` strips `<const>`/`<close>`,
hides `coroutine.close/isyieldable`.

## Writing .tl (pitfalls that break agents)

- Valid Lua 5.4 is valid TypeLua. `type`/`class`/`import`/`defer`/`await`/
  `spawn`/`select`/`async`/`macro` are **contextual/soft keywords**:
  `local import = 1`, `await(x)` (call), `spawn(x)` (call) stay plain Lua.
- Function bodies: `... end` and `{ ... }` are equivalent; emitter always
  outputs `end`-style Lua. `:` return type and `->` return type both work.
- `!=` is an alias of `~=` (emitted as `~=`). `x as T` needs one direction
  assignable; `x!` strips `nil`. Multi-return is `-> (A, B)`; only the last
  call expands.
- `nil` handling: `strictNilChecks` on by default. Narrow with
  `==nil`/`~=nil`, `type(x)=="string"`, `=="lit"`, truthiness, member paths
  (`if node.next then node.next.v end`). Reassignment does not reset narrowing.
- `interface` is structural, `class` instances are nominal (unrelated classes
  never mix even with same shape). `class` is always block-scoped `local`;
  `abstract` cannot be instantiated; `private`/`protected`/`readonly` are
  compile-time only. `super(...)` only in derived constructors before `self`;
  `super:m(...)` for base methods.
- Generics: `<T>`, `<T extends C>`, `<T = Default>`; calls infer or take
  explicit `f<T>()` (`obj:m<T>()`, `C.new<T>()`). `chan<T>` must spell `T`:
  `makeChan<integer>(4)` (`TL3803` otherwise).
- `import N from "./x"` is a **package-local** import: resolved against the
  requiring file (`x.tl`, then `x/init.tl`) and emitted as a stable module name
  relative to the project or `tlua_modules/` root (`lib.codec`,
  `example.com.pkg.codec`). `pkg/init.tl` canonicalizes to `pkg`. Missing,
  escaping, `.d.tl` targets and name collisions are `TL4002`; `require("./x")`
  is rejected (no runtime relative semantics).
- `import N from "p"` / `import N: T from "p"` is `require` sugar with module
  type inference (`TL3010` on boundary mismatch). `require("ffi")` is typed
  only when the `require` identifier is not shadowed, else falls back to `any`.
- `defer`/`errdefer` take a **call** (`f()`, `obj:m()`), run at function exit
  (LIFO, lazy closure); `errdefer` only on explicit `error()` in the same
  function. Never pass bare `...` (`TL3235`).
- Concurrency is single-threaded CSP: `async function` + `await` markers only;
  `await`/`select` outside `async`/chunk is `TL3801`/`TL3802`; sync callers
  cannot call `async`/blocking primitives (`TL3805`). `select { case ... }`
  uses braces, `break` exits the select. No true parallelism.
- Macros are hygienic AST macros: `macro twice(x) = (x)+(x)`, statement form
  uses `do ... end`, vararg `...` must be last param and only spliced as
  call args (`TL37xx`).
- FFI/C: `ffi.cdef[[...]]` is opaque (never parsed), hand-write `as CLib`
  boundaries; needs `--target 5.1` + `--lua luajit`. `.d.tl` files check but
  never emit. `bindgen --header foo.h --out types/foo.d.tl --shim foo.tl`
  generates the contract; the `.h` is the single source.
- 5.1/LuaJIT: no `<const>`/`<close>`, no `//`, no bitwise ops, `yield` cannot
  cross `pcall`/for-iterators, static `.a` links into the host and uses `ffi.C`.

## LSP (stdio, for editor agents)

`textDocumentSync: 2` (incremental), hover, definition, completion (`.`/`:`),
references, rename, document symbols. The server loads ambient `.d.tl`
declarations from the nearest `tlua.json` (`typeRoots`) for diagnostics;
cross-file import types remain single-file (`any`), so `tlua check` is
authoritative for project-wide inference.

## Change discipline

- Keep `SPEC.md` + `examples/` + `internal/diag/codes.go` in sync. New syntax
  needs parser + checker + emitter + example + test + SPEC section.
- `docs/11-diagnostics.md` is generated (`go generate ./internal/diag`).
- Never invent a `TLxxxx` code; add it to `codes.go` (sorted, documented).
- Every snippet you put in user-facing docs must pass
  `tlua check` in a scratch project before commit.
