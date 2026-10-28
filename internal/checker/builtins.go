package checker

import (
	"fmt"

	"github.com/typelua/typelua/internal/types"
)

// 设计者视角：builtins 用 any-based 宽松签名避免对标准库误报；coroutine
// 等版本相关 API 按 Target 裁剪（5.1 隐藏 5.4-only）。IsBuiltin 标记让
// shadowing 检查豁免本地化重定义。
// 内置宽进宽出：除 tonumber 外全 any，Target 裁剪版本。
//
// 设计者视角：内置库签名是“宽进宽出”的海绵垫——除 tonumber/type 等少数精确外，
// 一律 any 进 any 出，避免对标准库误报。障碍是版本差异（coroutine.close 仅
// …（略，详见实现；排查先看 Span/诊断）
// 否则旧版本代码报不出错。取舍：coroutine 走精确 Object（见 coroutineModule），
// 其余走 any，海绵与手术刀分开用。不变量：调用方保证 sc 为待填顶层；幂等但只调
// 一次。排查现场：内置缺失先查 Ambient 模式是否跳过此处（见 checkFileModule）。
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
	// 核心函数区：print/pairs 等 20+ 个。
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
	// 标准模块按 any 挂，如 `table` 防误报。
	for _, m := range []string{"table", "string", "math", "io", "os", "debug", "package", "utf8", "bit32"} {
		sc.vars[m] = &Symbol{Name: m, Kind: symVar, Type: types.Any}
	}
	sc.vars["coroutine"] = &Symbol{Name: "coroutine", Kind: symVar, Type: c.coroutineModule()}
	// ---- cooperative CSP concurrency (Go-style, single-threaded) ----
	// `makeChan<T>(cap?: integer): chan<T>` (explicit T required: cap
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	tParam := &types.GenericParam{Name: "T"}
	sc.vars["makeChan"] = &Symbol{Name: "makeChan", Kind: symFunc, Type: &types.Func{
		Params:     []types.FuncParam{{Name: "cap", Type: types.Integer, Optional: true}},
		Returns:    []types.Type{&types.Chan{Elem: tParam}},
		TypeParams: []*types.GenericParam{tParam},
	}}
	// `sleep(sec: number)` suspends the current task (await it).
	sleepFn := &types.Func{Params: []types.FuncParam{{Name: "sec", Type: types.Number}}, IsAsync: true}
	sc.vars["sleep"] = &Symbol{Name: "sleep", Kind: symFunc, Type: sleepFn}
	sc.vars["timeout"] = &Symbol{Name: "timeout", Kind: symFunc, Type: sleepFn}
	// `WaitGroup` / `Mutex` coordination primitives.
	sc.vars["WaitGroup"] = &Symbol{Name: "WaitGroup", Kind: symVar, Type: &types.Object{Name: "WaitGroup", Fields: map[string]types.Field{
		"new": {Type: &types.Func{Returns: []types.Type{waitGroupType()}}},
	}}}
	sc.vars["Mutex"] = &Symbol{Name: "Mutex", Kind: symVar, Type: &types.Object{Name: "Mutex", Fields: map[string]types.Field{
		"new": {Type: &types.Func{Returns: []types.Type{mutexType()}}},
	}}}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	sc.vars["unpack"] = &Symbol{Name: "unpack", Kind: symFunc, Type: &types.Func{HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}}
	for _, s := range sc.vars {
		if s != nil {
			s.IsBuiltin = true
		}
	}
}
