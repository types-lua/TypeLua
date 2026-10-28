package checker

import (
	"strconv"

	"github.com/typelua/typelua/internal/types"
)

// 设计者视角：coroutine 模块按 Target 裁剪 5.4-only API；running 的第二
// 返回值在 5.1 为 nil，用联合类型表达版本差异。

// Precise coroutine module (single-threaded cooperative multitasking).
// Shapes cover 5.1 and 5.4: `close` and `isyieldable` exist only on 5.4+
// (absent under `--target 5.1`, so uses error as unknown fields), and
// `running` reports its second value as `boolean | nil` (nil on 5.1).

func fnField(ft *types.Func) types.Field {
	return types.Field{Type: ft}
}

func strLitUnion(lits ...string) types.Type {
	ms := make([]types.Type, len(lits))
	for i, s := range lits {
		ms[i] = &types.Literal{Value: strconv.Quote(s), Base: types.String}
	}
	return types.NewUnion(ms)
}

func (c *Checker) coroutineModule() types.Type {
	anyT := types.Any
	anyFunc := &types.Func{HasVarArg: true, VarArg: anyT, Returns: []types.Type{anyT}}
	fields := map[string]types.Field{}
	// create(f): thread. Any function shape accepted (narrowed on use).
	fields["create"] = fnField(&types.Func{
		Params:  []types.FuncParam{{Name: "f", Type: anyFunc}},
		Returns: []types.Type{types.Thread},
	})
	// resume(co, ...): (boolean, any). Extra yielded values past
	// position 2 read as nil (same truncation class as pcall).
	fields["resume"] = fnField(&types.Func{
		Params:    []types.FuncParam{{Name: "co", Type: types.Thread}},
		HasVarArg: true,
		VarArg:    anyT,
		Returns:   []types.Type{types.Boolean, anyT},
	})
	// yield(...): any (whatever the resumer passes back).
	fields["yield"] = fnField(&types.Func{
		HasVarArg: true,
		VarArg:    anyT,
		Returns:   []types.Type{anyT},
	})
	// wrap(f): function with an equally permissive shape.
	fields["wrap"] = fnField(&types.Func{
		Params:  []types.FuncParam{{Name: "f", Type: anyFunc}},
		Returns: []types.Type{anyFunc},
	})
	// status(co): lifecycle literal union (narrows with == / ~=).
	fields["status"] = fnField(&types.Func{
		Params:  []types.FuncParam{{Name: "co", Type: types.Thread}},
		Returns: []types.Type{strLitUnion("running", "suspended", "normal", "dead")},
	})
	// running(): (thread, boolean|nil); ismain is nil on 5.1.
	fields["running"] = fnField(&types.Func{
		Returns: []types.Type{types.Thread, types.NewUnion([]types.Type{types.Boolean, types.Nil})},
	})
	if c.opt.Target != "5.1" {
		// close(co): 5.4+ only (kills a suspended coroutine).
		fields["close"] = fnField(&types.Func{
			Params: []types.FuncParam{{Name: "co", Type: types.Thread}},
		})
		// isyieldable(): 5.4+ only.
		fields["isyieldable"] = fnField(&types.Func{
			Returns: []types.Type{types.Boolean},
		})
	}
	return &types.Object{Name: "coroutine", Fields: fields}
}
