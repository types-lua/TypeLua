package checker

import (
	"github.com/typelua/typelua/internal/types"
)

// Precise LuaJIT ffi module (C ABI bridge for cross-language libraries).
// Runtime requires LuaJIT (`--target 5.1`); the type itself is always
// available so conditional `require("ffi")` never produces false positives.
//
// Model: `ffi.cdef` is opaque (C source stays a string, emitted verbatim);
// typing happens at the boundary via `as` casts:
//
//	local ffi = require("ffi")
//	ffi.cdef[[ int add(int a, int b); ]]
//	type CLib = { add(a: integer, b: integer): integer }
//	local C = ffi.load("mylib") as CLib
//
// `cdata`/`ctype` are distinct primitives (not aliases of userdata) so
// pointer confusion surfaces as TL3010 instead of silently passing.
//
// 设计者视角：ffi 是跨语言的“海关”——cdef 不透明（C 源码原样发射），load 回 any
// 由调用方 `as` 自家 CLib 确权，cdata/ctype 独立原语防指针串味。障碍是条件 require
// 在非 JIT 下运行不到：类型恒可用以免误报，运行时门另由 target/部署保。候选：
// 精确各函数签名 vs 全 any；选精确（载荷类型 string/integer/boolean/cdata 分明）。
// 不变量：表名恒 "ffi" 供 ownerEntry。排查现场：C 库调用误报先查是否忘 `as CLib`。

// ffiModule builds the precise ffi table type (always available, see above).
//
// 设计者视角：ffiModule 是上模的“装配线”——cdef/load/new/cast/typeof/gc/string/copy/
// fill/istype/sizeof/alignof/offsetof/metatype/C/os/arch/abi 逐字段精确装，
// 变参位标 any。障碍是 C 命名空间动态：C 恒 any，os/arch 恒 string，abi 为函数。
// 取舍：硬编码（JIT API 稳定）。排查现场：某 ffi 函数类型漂移先查此处装配。
func (c *Checker) ffiModule() types.Type {
	anyT := types.Any
	strT := types.String
	voidT := types.Void
	boolT := types.Boolean
	intT := types.Integer
	cdataT := types.Cdata
	ctypeT := types.Ctype

	mk := func(params []types.FuncParam, rets []types.Type, variadic bool) types.Field {
		return types.Field{Type: &types.Func{Params: params, Returns: rets, HasVarArg: variadic, VarArg: anyT}}
	}
	fields := map[string]types.Field{}

	// cdef(def: string): void — C declarations, opaque to the checker.
	fields["cdef"] = mk(
		[]types.FuncParam{{Name: "def", Type: strT}},
		[]types.Type{voidT}, false,
	)
	// load(name [, global]): any — returns the C namespace; caller casts
	// with `as` to a declared interface (per-library contract).
	fields["load"] = mk(
		[]types.FuncParam{{Name: "name", Type: strT}, {Name: "global", Type: boolT, Optional: true}},
		[]types.Type{anyT}, false,
	)
	// new(ct [, ...]): cdata
	fields["new"] = mk(
		[]types.FuncParam{{Name: "ct", Type: strT}},
		[]types.Type{cdataT}, true,
	)
	// cast(ct, init): cdata
	fields["cast"] = mk(
		[]types.FuncParam{{Name: "ct", Type: strT}, {Name: "init", Type: anyT}},
		[]types.Type{cdataT}, false,
	)
	// typeof(obj [, ...]): ctype
	fields["typeof"] = mk(
		[]types.FuncParam{{Name: "obj", Type: anyT}},
		[]types.Type{ctypeT}, true,
	)
	// gc(cd [, finalizer]): cdata
	fields["gc"] = mk(
		[]types.FuncParam{{Name: "cd", Type: cdataT}, {Name: "fin", Type: anyT, Optional: true}},
		[]types.Type{cdataT}, false,
	)
	// string(cd [, len]): string
	fields["string"] = mk(
		[]types.FuncParam{{Name: "cd", Type: cdataT}, {Name: "len", Type: intT, Optional: true}},
		[]types.Type{strT}, false,
	)
	// copy(dst, src [, len]): void
	fields["copy"] = mk(
		[]types.FuncParam{{Name: "dst", Type: cdataT}, {Name: "src", Type: anyT}, {Name: "len", Type: intT, Optional: true}},
		[]types.Type{voidT}, false,
	)
	// fill(dst, len [, c]): void
	fields["fill"] = mk(
		[]types.FuncParam{{Name: "dst", Type: cdataT}, {Name: "len", Type: intT}, {Name: "c", Type: intT, Optional: true}},
		[]types.Type{voidT}, false,
	)
	// istype(ct, obj): boolean
	fields["istype"] = mk(
		[]types.FuncParam{{Name: "ct", Type: anyT}, {Name: "obj", Type: anyT}},
		[]types.Type{boolT}, false,
	)
	// errno()/alignof()/sizeof()/offsetof(): permissive numeric helpers.
	fields["errno"] = mk(nil, []types.Type{intT}, false)
	fields["sizeof"] = mk(
		[]types.FuncParam{{Name: "obj", Type: anyT}},
		[]types.Type{intT}, false,
	)
	fields["alignof"] = mk(
		[]types.FuncParam{{Name: "obj", Type: anyT}},
		[]types.Type{intT}, false,
	)
	fields["offsetof"] = mk(
		[]types.FuncParam{{Name: "ct", Type: anyT}, {Name: "field", Type: strT}},
		[]types.Type{intT}, false,
	)
	// metatype(ct, metatable): ctype
	fields["metatype"] = mk(
		[]types.FuncParam{{Name: "ct", Type: anyT}, {Name: "mt", Type: anyT}},
		[]types.Type{ctypeT}, false,
	)
	// C namespace + platform strings: dynamic, stay any/string.
	fields["C"] = types.Field{Type: anyT}
	fields["os"] = types.Field{Type: strT}
	fields["arch"] = types.Field{Type: strT}
	fields["abi"] = types.Field{Type: func() types.Type {
		return &types.Func{
			Params:    []types.FuncParam{{Name: "what", Type: strT}},
			Returns:   []types.Type{boolT},
			HasVarArg: true, VarArg: anyT,
		}
	}()}

	return &types.Object{Name: "ffi", Fields: fields}
}
