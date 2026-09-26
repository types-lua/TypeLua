# LevelDB × TypeLua（原生 Lua 5.4 / C 模块路径）

与 `leveldb-ffi/` 同一演示，跑在 **stock lua5.4** 上。5.4 没有 `ffi` 模块
（`luaffifb` 无 ARM64 调用桩，此路不通），故走 SPEC §16 形态 A：专用 C 模块。

## 原理

```
lvl54.c ──gcc──▶ leveldb54.so ──require──▶ main.tl
(luaopen_leveldb54)   (自包含)      (import … as LevelDB54)
     ▲                                  ▲
     └──── leveldb54.d.tl（手写契约，只检查不发射）
```

* `bindgen` 只服务 FFI 路径，C 模块的契约手写（DB 句柄是不透明 `userdata`，
  无 `cdata` 索引问题，所以不需要中间 wrapper 层，`main.tl` 直调）。
* `leveldb54.so` **自包含**：PIC 静态库（`build-static-pic`，普通 `.a` 的非
  PIC 重定位链不进 `.so`）被链进模块，`ldd` 仅剩系统库，无路径依赖。
* C 侧安全规则（见 `lvl54.c` 头注释）：userdata + metatable + `__gc` 兜底，
  `close` 置 NULL 防 double-free；全部 key/value 走显式长度（NUL 可往返）；
  err 缓冲只在错误路径 free 一次；迭代器借用指针当场拷贝，两条路径都 destroy。
* `require("leveldb54")` 靠 `package.cpath` 的脚本目录模板找到同目录 `.so`
 （构建产物 prelude 已置好，用户 `LUA_CPATH` 保留）。

## 使用

```bash
sh examples/leveldb-54/build.sh    # 需 lua5.4 头 + /home/tang/leveldb
                                   # （LEVELDB_SRC 可覆盖）；产出 leveldb54.so
go run ./cmd/tlua check            # 全仓 39 文件零错误
go run ./cmd/tlua build            # dist/examples/leveldb-54/ 下有 main.lua + .so
lua5.4 dist/examples/leveldb-54/main.lua    # 真机运行（DB 建在当前目录）
```

期望输出与 `leveldb-ffi/` 逐字节一致（除 DB 文件名 `leveldb_demo54.db`）：

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

同样先 `destroy` 再 `open`，重复运行幂等。误把 FFI 版 `main.lua` 丢给 5.4 会
干净报错 `module 'ffi' not found`——这正是拆成两个范例的原因。
