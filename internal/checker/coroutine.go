package checker

import (
	"strconv"

	"github.com/typelua/typelua/internal/types"
)

// 设计者视角：coroutine 模块按 Target 裁剪 5.4-only API；running 的第二
// 返回值在 5.1 为 nil，用联合类型表达版本差异。
// coroutine 双轨精确件：5.4 独有 close，running 次值或 nil。
// Shapes cover 5.1 and 5.4: `close` and `isyieldable` exist only on 5.4+
// (absent under `--target 5.1`, so uses error as unknown fields), and
// `running` reports its second value as `boolean | nil` (nil on 5.1).
//
// …（略，详见实现；排查先看 Span/诊断）
//
// 设计者视角：fnField 是表构造的“装盒器”——Func 值须包成 Field 才进 Object。
// 薄封装省重复字面量。排查现场：方法缺失先查是否忘包此层。
func fnField(ft *types.Func) types.Field {
	return types.Field{Type: ft}
}

// fnField 装盒见实现，排查先看 Span/诊断。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
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
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	fields["create"] = fnField(&types.Func{
		Params:  []types.FuncParam{{Name: "f", Type: anyFunc}},
		Returns: []types.Type{types.Thread},
	})
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	fields["resume"] = fnField(&types.Func{
		Params:    []types.FuncParam{{Name: "co", Type: types.Thread}},
		HasVarArg: true,
		VarArg:    anyT,
		Returns:   []types.Type{types.Boolean, anyT},
	})
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	fields["yield"] = fnField(&types.Func{
		HasVarArg: true,
		VarArg:    anyT,
		Returns:   []types.Type{anyT},
	})
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	fields["wrap"] = fnField(&types.Func{
		Params:  []types.FuncParam{{Name: "f", Type: anyFunc}},
		Returns: []types.Type{anyFunc},
	})
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	fields["status"] = fnField(&types.Func{
		Params:  []types.FuncParam{{Name: "co", Type: types.Thread}},
		Returns: []types.Type{strLitUnion("running", "suspended", "normal", "dead")},
	})
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	fields["running"] = fnField(&types.Func{
		Returns: []types.Type{types.Thread, types.NewUnion([]types.Type{types.Boolean, types.Nil})},
	})
	if c.opt.Target != "5.1" {
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		fields["close"] = fnField(&types.Func{
			Params: []types.FuncParam{{Name: "co", Type: types.Thread}},
		})
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		fields["isyieldable"] = fnField(&types.Func{
			Returns: []types.Type{types.Boolean},
		})
	}
	return &types.Object{Name: "coroutine", Fields: fields}
}
