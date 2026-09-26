# LevelDB × TypeLua（LuaJIT / FFI 路径）

用真 C 库（LevelDB v1.23）演示 FFI 端到端：`cdef` + `ffi.load` + 一次 `as` 定边界，
调用方零 cast。运行时需要 **LuaJIT**（`ffi` 是 LuaJIT 专属模块，原生 lua5.4
走隔壁 `leveldb-54/` 的 C 模块形态）。

## 原理

```
leveldb.h ──bindgen──▶ leveldb_raw.d.tl ──┐ contract（类型契约，只检查不发射）
              (lib LevelDBRaw)            │
leveldb.h ──bindgen──▶ leveldb_raw.tl  ───┘ raw shim（cdef + ffi.load + 唯一 as + return）
leveldb_raw.tl ──import──▶ leveldb.tl ──import──▶ main.tl
                             安全 wrapper        演示（只认 strings）
```

* `.h` 为唯一源：改头文件后重跑 `bindgen`（命令见 `build.sh` 头注释），`.d.tl`
  与 shim 必须原文提交，不手改。
* `leveldb.h` 是上游 `c.h` 的**精简子集**（签名已逐条对过
  `/home/tang/leveldb/include/leveldb/c.h`），外加两处 ABI 等价改写（指针换
  指针，机器层调用一致），原因都在头注释里：
  - `char **errptr` 写成 `void *`：bindgen 会把 `char**` 误判为 `string`，
    而 out-param 必须保持 `cdata`；
  - `leveldb_get` 返回、`iter_key`/`iter_value` 返回写成 `void *`：前者是
    调用方拥有的缓冲（`string` 会拷贝后泄漏原指针），后两者是带显式长度
    的借用缓冲（二进制安全，`string` 会在首个 NUL 截断）。
* 所有权收敛在 `leveldb.tl` 一处：errptr 每次 fresh（LevelDB 成功时不动
  out-param，复用会残留旧错）、get 缓冲拷贝后 `free`、迭代器借用指针当场
  拷贝、`scan` 不逃逸 live iterator、`DB` 析构三件套。
* `cdata` 在类型系统里不可索引、与 `nil` 不可比：wrapper 内用 `any` 中转
  一次（`as` 双向可赋即合法），调用方无感。注意 LuaJIT 里 cdata NULL 是
  truthy，判空必须 `== nil`，不能 `if p then`。

## 使用

```bash
sh examples/leveldb-ffi/build.sh   # 需 cmake+gcc，LevelDB 在 /home/tang/leveldb
                                   # （LEVELDB_SRC 可覆盖）；产出 libleveldb.so/.a
go run ./cmd/tlua check            # 全仓 39 文件零错误
go run ./cmd/tlua build            # dist/examples/leveldb-ffi/ 下有 .lua + .so + .a
luajit dist/examples/leveldb-ffi/main.lua   # 真机运行（DB 建在当前目录）
```

期望输出（`scan` 为字节序）：

```
leveldb major version:	1
k1 =	v1
missing key -> nil
scan:	empty
scan:	k1	v1
scan:	k2	v2
deleted key -> nil
done
```

demo 每次先 `destroy` 再 `open`，重复运行幂等。`.so` 与 shim 同目录（源码与
`dist` 同布局），shim 用 `debug.getinfo` 拼绝对路径 `ffi.load`，在哪跑都行；
`.a` 随构建透传到 `dist`（静态产物，不参与运行）。
