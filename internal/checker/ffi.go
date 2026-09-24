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
