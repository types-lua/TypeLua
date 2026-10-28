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
//
// 设计者视角：coroutine 是“版本双轨”的精确模块——5.1/5.4 差异只在 close/
// isyieldable 有无与 running 第二值是否为 nil。障碍是条件 require 永不误报：
// 类型恒可用（见 ffi 注释同理），缺失 API 靠“无字段即报错”自然表达版本门禁。
// 候选：两套表 vs 一套+条件；选一套+条件，复用同一 Object 构造。status 用字面量
// 并集以便窄化（== "dead" 可收窄）。取舍：resume/yield 取宽松 any（协程传值动态）。
// 不变量：Target=="5.1" 时无 close/isyieldable。排查现场：5.1 下 close 未报先查
// Target 是否透传至此。

// fnField wraps a Func as an Object field (coroutine/ffi table builder).
//
// 设计者视角：fnField 是表构造的“装盒器”——Func 值须包成 Field 才进 Object。
// 薄封装省重复字面量。排查现场：方法缺失先查是否忘包此层。
func fnField(ft *types.Func) types.Field {
	return types.Field{Type: ft}
}

// strLitUnion builds a string-literal union (e.g. coroutine status).
//
// 设计者视角：strLitUnion 把程序生命周期的四个字面拼成并集，供 =="dead" 窄化
// （见 narrowLitOfType）。障碍是字面须带引号拼写：用 strconv.Quote 保源拼写，
// 窄化时 norm 比对引号内外皆可。取舍：小分配换窄化能力。排查现场：status 窄化
// 失效先查此处 Base 是否为 String。
func strLitUnion(lits ...string) types.Type {
	ms := make([]types.Type, len(lits))
	for i, s := range lits {
		ms[i] = &types.Literal{Value: strconv.Quote(s), Base: types.String}
	}
	return types.NewUnion(ms)
}

// coroutineModule builds the precise coroutine table type (target-gated).
//
// 设计者视角：coroutineModule 是上段双轨的“装配线”——按 Target 决定是否装
// close/isyieldable，其余字段恒装。create/resume/yield/wrap 取宽松（动态传值），
// status/running 取精确（字面并集/双返回）。障碍是 running 第二值版本差异：
// 用 boolean|nil 并集统一表达，5.1 的 nil 即此。取舍：表名恒 "coroutine" 供
// ownerEntry 回查。不变量：返回 *Object 非 nil。排查现场：成员类型漂移先查此处
// 装配是否漏字段。
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
