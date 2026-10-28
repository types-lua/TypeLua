package checker

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/types"
)

// 设计者视角：类型声明在 collectTypes 阶段已登记；此处仅 resolve 以
// 触发错误与缓存，并为 enum 生成运行时表值。

// 类型详见实现，排查看 Span。
//
// 设计者视角：类型声明在 collectTypes 已占座，此处只“点火”——resolve 触发错误与
// 缓存详见实现，排查看 Span。
// 声明：lookup 指针须与本节点同（Entry.alias/iface/enumDecl 归属），否则为副本
// 直接退（错已报）。取舍：声明体不另验（resolve 即验）。排查现场：前向引用取空
// 先查 collectTypes 是否登记。

// ---------- type declarations ----------

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：checkTypeAlias 是别名的“点火器”——lookup 到本节点 Entry 即 resolve
// （错与环在 entry 层报+缓存）。无他事。排查现场：别名环未报先查 resolving 守卫。
func (c *Checker) checkTypeAlias(n *ast.TypeAliasDecl) {
	e := c.cur().lookupType(n.Name.Name)
	if e == nil {
		return
	}
	// 解析，排查先看 Span/诊断。
	_ = c.resolveTypeEntry(e, nil)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：checkInterface 多一步“钉桩”——FuncType 字段记 method 余记 field，
// 再 resolve（继承合并与重字段错在 buildInterface 报）。障碍是方法简写识别：
// 以 Type 是否 *FuncType 判（非名）。排查现场：接口成员跳转缺失先查此处 Kind 判据。
func (c *Checker) checkInterface(n *ast.InterfaceDecl) {
	e := c.cur().lookupType(n.Name.Name)
	if e == nil {
		return
	}
	for _, m := range n.Fields {
		if m.Name == nil {
			continue
		}
		kind := "field"
		if _, ok := m.Type.(*ast.FuncType); ok {
			kind = "method"
		}
		c.recordMemberDef(m.Name.Span(), e, m.Name.Name, kind)
	}
	_ = c.resolveTypeEntry(e, nil)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：checkEnum 是枚举的“双生”——类型侧 resolve 取 Enum，值侧 Object 表
// （成员字面为字段）落值册，双记 Entry（见 recordDefEntry），成员逐桩。障碍是
// 非 Enum 解（如环坏）：无表即无值（静返）。取舍：值表名与类型同名（双栖）。
// 排查现场：`Color.Red` 找不到先查值表是否建成。
func (c *Checker) checkEnum(n *ast.EnumDecl) {
	e := c.cur().lookupType(n.Name.Name)
	if e == nil {
		return
	}
	et := c.resolveTypeEntry(e, nil)
	// 定义，排查先看 Span/诊断。
	if _, ok := et.(*types.Enum); ok {
		fields := map[string]types.Field{}
		for name, m := range et.(*types.Enum).Members {
			fields[name] = types.Field{Type: m}
		}
		obj := &types.Object{Name: n.Name.Name, Fields: fields}
		c.recordDefEntry(n.Name.Span(), c.bindVar(n.Name.Name, &Symbol{Name: n.Name.Name, Kind: symVar, Type: obj, Span: n.Span()}), e)
		for _, m := range n.Members {
			if m.Name == nil {
				continue
			}
			c.recordMemberDef(m.Name.Span(), e, m.Name.Name, "member")
		}
	}
}
