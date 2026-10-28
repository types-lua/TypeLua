package checker

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// 设计者视角：索引记录是 best-effort，永远不参与诊断；StructOwner 只追踪
// 直接变量流，别名/参数/表值不追踪，避免误合并。

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：索引是“只记不判”的考勤簿——所有 record* 先判 Index==nil 即返，永不
// errorf；名义成员须 lookupNamedField 命中（有定义点）才记，索引器解出的无名场不记；
// union 须全员同 Entry 才记（见 recordMemberUse）。结构表（匿名字面）走 StructOwner
// 直流（别名不追）。取舍：漏记优于误并。排查现场：跳转缺失先查是否索引器解出，
// 误合并先查 union 是否异主。

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：recordUse 是变量使用的“打卡”——Span 记处，Sym 记户，Root 经 symRoot
// 归定义。障碍是影子 Symbol（窄化）若记影不记根，引用聚类分裂；故恒记 Root。
// 取舍：nil 守卫 best-effort。排查现场：引用缺分支先查 Root 是否归一。
func (c *Checker) recordUse(span token.Span, s *Symbol) {
	if c.Index == nil || s == nil {
		return
	}
	c.Index.Uses = append(c.Index.Uses, SymUse{Span: span, Sym: s, Root: symRoot(s)})
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：recordDef 与 recordUse 对偶——定义处多 IsDef 旗，供跳转着陆与重命名
// 锚点。bindVar 与此处成对（先落户后打卡）。排查现场：跳转无着陆先查是否漏调此处
// （如全局函数更新旧户未打卡）。
func (c *Checker) recordDef(span token.Span, s *Symbol) {
	if c.Index == nil || s == nil {
		return
	}
	c.Index.Uses = append(c.Index.Uses, SymUse{Span: span, Sym: s, Root: symRoot(s), IsDef: true})
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：recordTypeUse 是类型引用的“描红”——resolveNamed 每解一名即记，
// Entry 指针即身份。障碍是泛型实参内层亦需记（resolve 递归自带）。取舍：与值
// 打卡分册（SymUse.Entry 轨）。排查现场：类型引用缺失先查 resolveNamed 是否早返
// （如原语/特殊泛型分支不记即对）。
func (c *Checker) recordTypeUse(span token.Span, e *typeEntry) {
	if c.Index == nil || e == nil {
		return
	}
	c.Index.Uses = append(c.Index.Uses, SymUse{Span: span, Entry: e})
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：recordDefEntry 是类/枚举名的“双户口”——值册与类型册同名共生，
// 一次打卡记 Sym+Entry 双轨，重命名一次改两处。障碍是单轨会漏一半（只改值不改
// 类型）。取舍：打卡时双带，查询时双归。排查现场：重命名漏类型/值先查此处是否
// 只记一轨。

func (c *Checker) recordDefEntry(span token.Span, s *Symbol, e *typeEntry) {
	if c.Index == nil || s == nil {
		return
	}
	c.Index.Uses = append(c.Index.Uses, SymUse{Span: span, Sym: s, Root: symRoot(s), IsDef: true, Entry: e})
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：recordMemberDef 是成员定义的“钉桩”——类/接口/枚举每成员记 Owner+名+
// 种详见实现，排查看 Span。
// lookupNamedField 对其 false，故从不记（见注释）。取舍：定义必记，使用方严验。
// 排查现场：成员重命名无着陆先查此处 Kind 是否错。
func (c *Checker) recordMemberDef(span token.Span, e *typeEntry, name, kind string) {
	if c.Index == nil || e == nil {
		return
	}
	c.Index.Uses = append(c.Index.Uses, SymUse{Span: span, IsDef: true,
		Member: &MemberRef{Owner: e, OwnerName: e.name, Name: name, Kind: kind}})
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：lookupNamedField 是成员追踪的“验桩器”——只认声明字段（Object/
// Class/静态/Enum），索引器解出的不认（无桩可钉）。障碍是与 lookupField 混用会
// 把动态访问并到声明上；故追踪专用此严版，检查专用宽版。取舍：严进（漏记优于误并）。
// 排查现场：成员引用误合并先查是否误用 lookupField。

func lookupNamedField(ot types.Type, name string) (types.Field, bool) {
	switch x := ot.(type) {
	case *types.Object:
		if f, ok := x.Fields[name]; ok {
			return f, true
		}
	case *types.Class:
		if f, ok := x.Fields[name]; ok {
			return types.Field{Type: f.Type, Optional: f.Optional, Readonly: f.Readonly, Access: f.Access, Owner: f.Owner}, true
		}
	case *types.ClassValue:
		if f, ok := x.Statics[name]; ok {
			return types.Field{Type: f.Type, Optional: f.Optional, Readonly: f.Readonly, Access: f.Access, Owner: f.Owner}, true
		}
		// Synthetic `new` has no definition; never tracked.
	case *types.Enum:
		if m, ok := x.Members[name]; ok {
			return types.Field{Type: m}, true
		}
	}
	return types.Field{}, false
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 联合详见实现，排查看 Span。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：recordMemberUse 是成员使用的“点名”——先严验桩详见实现，排查看 Span。
// 再经 ownerEntry 回 Entry，union 须全员同主才记（异主即弃，防 A.x 与 B.x 误并）。
// 障碍是结构表/any 无主：跳过即对（检查仍走宽版）。取舍：宁缺（untracked）勿滥。
// 不变量：记即有桩有主。排查现场：成员引用缺失先查是否索引器解出或 union 异主。

func (c *Checker) recordMemberUse(span token.Span, objT types.Type, name string) {
	if c.Index == nil {
		return
	}
	if u, ok := objT.(*types.Union); ok {
		var e *typeEntry
		for _, m := range u.Types {
			if _, ok := lookupNamedField(m, name); !ok {
				return
			}
			me := c.ownerEntry(m)
			if me == nil {
				return
			}
			if e == nil {
				e = me
			} else if e != me {
				return
			}
		}
		if e == nil {
			return
		}
		c.Index.Uses = append(c.Index.Uses, SymUse{Span: span,
			Member: &MemberRef{Owner: e, OwnerName: e.name, Name: name}})
		return
	}
	if _, ok := lookupNamedField(objT, name); !ok {
		return
	}
	e := c.ownerEntry(objT)
	if e == nil {
		return
	}
	c.Index.Uses = append(c.Index.Uses, SymUse{Span: span,
		Member: &MemberRef{Owner: e, OwnerName: e.name, Name: name}})
}

// recordStringIndex records `obj["field"]` with a quoted literal key as a
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：recordStringIndex 让方括号串键享受点访问同等待遇——经 stringKeyName
// 切内 Span（保引号），有桩有主才记。障碍是动态键与长串：切不出即弃。取舍：
// 内 Span 打卡，重命名不碰引号。排查现场：`a["x"]` 跳转缺失先查键是否为字面串。

func (c *Checker) recordStringIndex(key ast.Expr, objT types.Type) {
	if c.Index == nil {
		return
	}
	sl, ok := key.(*ast.StringLit)
	if !ok {
		return
	}
	name, inner, ok := stringKeyName(sl)
	if !ok {
		return
	}
	if _, ok := lookupNamedField(objT, name); !ok {
		return
	}
	e := c.ownerEntry(objT)
	if e == nil {
		return
	}
	c.Index.Uses = append(c.Index.Uses, SymUse{Span: inner,
		Member: &MemberRef{Owner: e, OwnerName: e.name, Name: name}})
}

// recordStructIndex records `obj["field"]` through a plain variable with
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：recordStructIndex 是结构表的方括号版——经 structRoot 回根变量，
// 有桩详见实现，排查看 Span。
// 基无根：structRoot 仅认 Ident，余者弃。排查现场：结构串键未记先查根是否为直量。
func (c *Checker) recordStructIndex(key, obj ast.Expr) {
	if c.Index == nil {
		return
	}
	sl, ok := key.(*ast.StringLit)
	if !ok {
		return
	}
	name, inner, ok := stringKeyName(sl)
	if !ok {
		return
	}
	if root := structRoot(c, obj); root != nil {
		c.recordStructUse(inner, root, name)
	}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：ownerEntry 是值型到声明的“返乡证”——按名（Class/Value/Object/Enum
// 之 Name）在用点层回查 Entry。障碍是遮蔽：同名类型内外层不同，必须用点查
// （c.cur()）而非建时缓存；匿名（Name 空）即无乡。取舍：名回查（简单）而非指针
// 直连（Substitute 后指针漂）。不变量：nil 即结构/动态。排查现场：成员归属漂移
// 先查用点层是否有同名遮蔽。

func (c *Checker) ownerEntry(t types.Type) *typeEntry {
	var name string
	switch x := t.(type) {
	case *types.Class:
		name = x.Name
	case *types.ClassValue:
		if x.Class != nil {
			name = x.Class.Name
		}
	case *types.Object:
		name = x.Name
	case *types.Enum:
		name = x.Name
	default:
		return nil
	}
	if name == "" {
		return nil
	}
	return c.cur().lookupType(name)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：recordStructLiteral 是匿名字面的“分户”——Named 与 ["k"] 双式逐键
// 记 StructOwner=根变量。障碍是名义所属（ownerEntry 非 nil）须让路（走名义轨，
// 防双记）。取舍：ExprField（列表位）不记。排查现场：结构定义缺失先查是否为纯列表。
func (c *Checker) recordStructLiteral(root *Symbol, lit *ast.TableLit) {
	if c.Index == nil || root == nil || lit == nil {
		return
	}
	for _, f := range lit.Fields {
		var name string
		var sp token.Span
		switch f.Kind {
		case ast.NamedField:
			if f.Key == nil {
				continue
			}
			name, sp = f.Key.Name, f.Key.Span()
		case ast.IndexedField:
			sl, ok := f.KeyX.(*ast.StringLit)
			if !ok {
				continue
			}
			nm, inner, ok := stringKeyName(sl)
			if !ok {
				continue
			}
			name, sp = nm, inner
		default:
			continue
		}
		c.recordStructDef(root, name, sp)
	}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：recordStructDef 是结构成员的“钉桩”（定义侧）——OwnerName 取根变量名，
// StructOwner 取根指针，Kind 恒 "field"。与 recordStructUse 配对（桩与点）。
// 取舍：IsDef 恒真。排查现场：结构引用无着陆先查桩名拼写（含引号剥离）。
func (c *Checker) recordStructDef(root *Symbol, name string, span token.Span) {
	if c.Index == nil || root == nil {
		return
	}
	c.Index.Uses = append(c.Index.Uses, SymUse{Span: span, IsDef: true,
		Member: &MemberRef{OwnerName: root.Name, Name: name, Kind: "field", StructOwner: root}})
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：hasStructDef 是结构引用的“验桩”——线性扫 Uses 找同根同名 IsDef。
// 用前必验（recordStructUse 内），无桩不记点，防 `p.q` 在 q 未字面定义时误并。
// 取舍：O(n) 扫（Uses 量小）。排查现场：结构点记不上先查桩是否在另一根（如别名）。
func (c *Checker) hasStructDef(root *Symbol, name string) bool {
	if c.Index == nil || root == nil {
		return false
	}
	for i := range c.Index.Uses {
		u := &c.Index.Uses[i]
		if u.IsDef && u.Member != nil && u.Member.StructOwner == root && u.Member.Name == name {
			return true
		}
	}
	return false
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：recordStructUse 是结构成员的“点名”（使用侧）——有桩才记，无桩即弃；
// 只认直流（同变量直达），别名/参数/表值一律不追（防 `q=p; q.x` 误并）。障碍是
// 过度追踪的误合并 vs 漏记：选漏记（sound 优先，检查不受影响）。取舍：调用方先
// structRoot 解根。排查现场：结构跳转缺失多为别名流（设计如此）。

func (c *Checker) recordStructUse(span token.Span, root *Symbol, name string) {
	if c.Index == nil || root == nil {
		return
	}
	if !c.hasStructDef(root, name) {
		return
	}
	c.Index.Uses = append(c.Index.Uses, SymUse{Span: span,
		Member: &MemberRef{OwnerName: root.Name, Name: name, StructOwner: root}})
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：structRoot 是结构追踪的“寻根”——只认 Ident 直量（`p.q` 之 p），
// `a.b.c` 之深基无变量身份即 nil。经 symRoot 归定义（窄化影子亦归）。障碍是
// 方法调用基与索引基：此处一律不认（调用方各取所需）。取舍：严进。排查现场：
// 结构链 `a.b.c` 不记多为深基（设计如此）。
func structRoot(c *Checker, obj ast.Expr) *Symbol {
	id, ok := obj.(*ast.Ident)
	if !ok {
		return nil
	}
	if s := c.cur().lookupVar(id.Name); s != nil {
		return symRoot(s)
	}
	return nil
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：recordScope 是户型图的“钉钉子”——层与块 Span 配对入 Scopes，
// ScopeAt 按偏移找最深含层。障碍是层忘钉即查无此层（补全失灵）；故 checkBlock/
// 文件顶/函数体/方法体各钉一处。取舍：best-effort。排查现场：补全变量缺失先查
// 对应块是否漏钉。
func (c *Checker) recordScope(sc *Scope, span token.Span) {
	if c.Index == nil {
		return
	}
	c.Index.Scopes = append(c.Index.Scopes, ScopeSpan{Sc: sc, Span: span})
}
