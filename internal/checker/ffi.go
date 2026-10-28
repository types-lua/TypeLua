package checker

import (
	"github.com/typelua/typelua/internal/types"
)

// ffi 跨语言海关：cdef 原样发射，load 回 any 由 `as` 确权。
// Runtime requires LuaJIT (`--target 5.1`); the type itself is always
// available so conditional `require("ffi")` never produces false positives.
//
// Model: `ffi.cdef` is opaque (C source stays a string, emitted verbatim);
// typing happens at the boundary via `as` casts:
//
// …（略，详见实现；排查先看 Span/诊断）
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

	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	fields["cdef"] = mk(
		[]types.FuncParam{{Name: "def", Type: strT}},
		[]types.Type{voidT}, false,
	)
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// with `as` to a declared interface (per-library contract).
	fields["load"] = mk(
		[]types.FuncParam{{Name: "name", Type: strT}, {Name: "global", Type: boolT, Optional: true}},
		[]types.Type{anyT}, false,
	)
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	fields["new"] = mk(
		[]types.FuncParam{{Name: "ct", Type: strT}},
		[]types.Type{cdataT}, true,
	)
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	fields["cast"] = mk(
		[]types.FuncParam{{Name: "ct", Type: strT}, {Name: "init", Type: anyT}},
		[]types.Type{cdataT}, false,
	)
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	fields["typeof"] = mk(
		[]types.FuncParam{{Name: "obj", Type: anyT}},
		[]types.Type{ctypeT}, true,
	)
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	fields["gc"] = mk(
		[]types.FuncParam{{Name: "cd", Type: cdataT}, {Name: "fin", Type: anyT, Optional: true}},
		[]types.Type{cdataT}, false,
	)
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	fields["string"] = mk(
		[]types.FuncParam{{Name: "cd", Type: cdataT}, {Name: "len", Type: intT, Optional: true}},
		[]types.Type{strT}, false,
	)
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	fields["copy"] = mk(
		[]types.FuncParam{{Name: "dst", Type: cdataT}, {Name: "src", Type: anyT}, {Name: "len", Type: intT, Optional: true}},
		[]types.Type{voidT}, false,
	)
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	fields["fill"] = mk(
		[]types.FuncParam{{Name: "dst", Type: cdataT}, {Name: "len", Type: intT}, {Name: "c", Type: intT, Optional: true}},
		[]types.Type{voidT}, false,
	)
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	fields["istype"] = mk(
		[]types.FuncParam{{Name: "ct", Type: anyT}, {Name: "obj", Type: anyT}},
		[]types.Type{boolT}, false,
	)
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
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
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	fields["metatype"] = mk(
		[]types.FuncParam{{Name: "ct", Type: anyT}, {Name: "mt", Type: anyT}},
		[]types.Type{ctypeT}, false,
	)
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
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
