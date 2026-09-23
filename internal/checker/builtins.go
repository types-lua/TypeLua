package checker

import (
	"fmt"

	"github.com/typelua/typelua/internal/types"
)

// Builtin value definitions (permissive any-based to avoid false positives).

// ---------- builtins (permissive any-based to avoid false positives) ----------

func (c *Checker) defineBuiltins(sc *Scope) {
	anyT := types.Any
	mkFunc := func(params ...types.Type) types.Type {
		var ps []types.FuncParam
		for i, p := range params {
			ps = append(ps, types.FuncParam{Name: fmt.Sprintf("arg%d", i), Type: p})
		}
		return &types.Func{Params: ps, HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}
	}
	_ = mkFunc
	// Core functions.
	sc.vars["print"] = &Symbol{Name: "print", Kind: symFunc, Type: &types.Func{HasVarArg: true, VarArg: anyT, Returns: nil}}
	sc.vars["pairs"] = &Symbol{Name: "pairs", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "t", Type: anyT}}, HasVarArg: false, Returns: []types.Type{anyT}}}
	sc.vars["ipairs"] = &Symbol{Name: "ipairs", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "t", Type: anyT}}, Returns: []types.Type{anyT}}}
	sc.vars["tostring"] = &Symbol{Name: "tostring", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "v", Type: anyT}}, Returns: []types.Type{types.String}}}
	sc.vars["tonumber"] = &Symbol{Name: "tonumber", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "v", Type: anyT}}, HasVarArg: true, VarArg: anyT, Returns: []types.Type{types.NewUnion([]types.Type{types.Number, types.Nil})}}}
	sc.vars["type"] = &Symbol{Name: "type", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "v", Type: anyT}}, Returns: []types.Type{types.String}}}
	sc.vars["require"] = &Symbol{Name: "require", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "m", Type: types.String}}, Returns: []types.Type{anyT}}}
	sc.vars["assert"] = &Symbol{Name: "assert", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "v", Type: anyT}}, HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}}
	sc.vars["error"] = &Symbol{Name: "error", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "msg", Type: anyT}}, HasVarArg: true, VarArg: anyT, Returns: []types.Type{types.Never}}}
	sc.vars["pcall"] = &Symbol{Name: "pcall", Kind: symFunc, Type: &types.Func{HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}}
	sc.vars["xpcall"] = &Symbol{Name: "xpcall", Kind: symFunc, Type: &types.Func{HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}}
	sc.vars["select"] = &Symbol{Name: "select", Kind: symFunc, Type: &types.Func{HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}}
	sc.vars["next"] = &Symbol{Name: "next", Kind: symFunc, Type: &types.Func{HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}}
	sc.vars["setmetatable"] = &Symbol{Name: "setmetatable", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "t", Type: anyT}, {Name: "m", Type: anyT}}, Returns: []types.Type{anyT}}}
	sc.vars["getmetatable"] = &Symbol{Name: "getmetatable", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "v", Type: anyT}}, Returns: []types.Type{anyT}}}
	sc.vars["rawget"] = &Symbol{Name: "rawget", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "t", Type: anyT}, {Name: "k", Type: anyT}}, Returns: []types.Type{anyT}}}
	sc.vars["rawset"] = &Symbol{Name: "rawset", Kind: symFunc, Type: &types.Func{HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}}
	sc.vars["rawequal"] = &Symbol{Name: "rawequal", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "a", Type: anyT}, {Name: "b", Type: anyT}}, Returns: []types.Type{types.Boolean}}}
	sc.vars["rawlen"] = &Symbol{Name: "rawlen", Kind: symFunc, Type: &types.Func{Params: []types.FuncParam{{Name: "v", Type: anyT}}, Returns: []types.Type{types.Integer}}}
	sc.vars["collectgarbage"] = &Symbol{Name: "collectgarbage", Kind: symFunc, Type: &types.Func{HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}}
	sc.vars["dofile"] = &Symbol{Name: "dofile", Kind: symFunc, Type: &types.Func{HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}}
	sc.vars["load"] = &Symbol{Name: "load", Kind: symFunc, Type: &types.Func{HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}}
	sc.vars["loadfile"] = &Symbol{Name: "loadfile", Kind: symFunc, Type: &types.Func{HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}}
	sc.vars["self"] = nil // placeholder; defined per-method.
	delete(sc.vars, "self")
	// Modules as any.
	for _, m := range []string{"table", "string", "math", "io", "os", "debug", "package", "utf8", "bit32"} {
		sc.vars[m] = &Symbol{Name: m, Kind: symVar, Type: types.Any}
	}
	sc.vars["coroutine"] = &Symbol{Name: "coroutine", Kind: symVar, Type: c.coroutineModule()}
	// ---- cooperative CSP concurrency (Go-style, single-threaded) ----
	// `makeChan<T>(cap?: integer): chan<T>` (explicit T required: cap
	// carries no value of T, so inference has nothing to bind).
	tParam := &types.GenericParam{Name: "T"}
	sc.vars["makeChan"] = &Symbol{Name: "makeChan", Kind: symFunc, Type: &types.Func{
		Params:     []types.FuncParam{{Name: "cap", Type: types.Integer, Optional: true}},
		Returns:    []types.Type{&types.Chan{Elem: tParam}},
		TypeParams: []*types.GenericParam{tParam},
	}}
	// `sleep(sec: number)` suspends the current task (await it).
	sleepFn := &types.Func{Params: []types.FuncParam{{Name: "sec", Type: types.Number}}}
	sc.vars["sleep"] = &Symbol{Name: "sleep", Kind: symFunc, Type: sleepFn}
	sc.vars["timeout"] = &Symbol{Name: "timeout", Kind: symFunc, Type: sleepFn}
	// `WaitGroup` / `Mutex` coordination primitives.
	sc.vars["WaitGroup"] = &Symbol{Name: "WaitGroup", Kind: symVar, Type: &types.Object{Name: "WaitGroup", Fields: map[string]types.Field{
		"new": {Type: &types.Func{Returns: []types.Type{waitGroupType()}}},
	}}}
	sc.vars["Mutex"] = &Symbol{Name: "Mutex", Kind: symVar, Type: &types.Object{Name: "Mutex", Fields: map[string]types.Field{
		"new": {Type: &types.Func{Returns: []types.Type{mutexType()}}},
	}}}
	// unpack compat.
	sc.vars["unpack"] = &Symbol{Name: "unpack", Kind: symFunc, Type: &types.Func{HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}}
	for _, s := range sc.vars {
		if s != nil {
			s.IsBuiltin = true
		}
	}
}
