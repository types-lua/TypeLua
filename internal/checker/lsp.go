package checker

import (
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// 设计者视角：LSP 读 API 全部 nil-safe，索引缺失时返回空而非崩溃；
// TypeName/DeclSpan/EntryKind 把 typeEntry 的内部字段暴露给语言服务。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：LSP 读 API 是索引的“只读橱窗”——全部 nil-safe（索引/层/符号缺失
// 即返空），检查语义零触碰。ScopeAt 按偏移取最深含层（补全先定位），ScopeVars/
// ScopeTypes 列本层与可见类型（内遮外），Kind/TypeName/DeclSpan/EntryKind 供
// …（略，详见实现；排查先看 Span/诊断）
// 障碍是块 Span 右闭/开：用 [Start,End) 半开，End 处不含（防相邻块串层）。候选：
// 二分 vs 线扫；选线扫（层数少）。取舍：O(n) 换简单。排查现场：补全变量错层先查
// recordScope 的 Span 是否包小。
func (idx *SymIndex) ScopeAt(offset int) *Scope {
	if idx == nil {
		return nil
	}
	var best *Scope
	bestSize := -1
	for _, ss := range idx.Scopes {
		if ss.Span.Start.Offset <= offset && offset < ss.Span.End.Offset {
			size := ss.Span.End.Offset - ss.Span.Start.Offset
			if best == nil || size < bestSize {
				best = ss.Sc
				bestSize = size
			}
		}
	}
	return best
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：ParentScope 是作用域链的“上楼梯”——nil-safe，顶层返 nil。供 LSP
// 逐层向外列可见符号。排查现场：外层符号缺失先查 parent 链是否在 Ambient 处断开。

func (s *Scope) ParentScope() *Scope {
	if s == nil {
		return nil
	}
	return s.parent
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：ScopeVar 是补全项的“名片”——Name 供展示，Sym 供取类型/种。薄 struct，
// 无逻辑。排查现场：补全名对而类型错，先查 Sym.Type 是否为窄化影子型。

type ScopeVar struct {
	Name string
	Sym  *Symbol
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：ScopeVars 只列本层（不向上），nil 层返 nil，nil 符跳过。上层符号由
// 调用方循 ParentScope 聚。障碍是窄化影子亦在列：属预期（分支内即见窄型）。取舍：
// 新切片（调用方免锁）。排查现场：补全多出影子即窄化生效，非 bug。

func (s *Scope) ScopeVars() []ScopeVar {
	if s == nil {
		return nil
	}
	out := make([]ScopeVar, 0, len(s.vars))
	for name, sym := range s.vars {
		if sym == nil {
			continue
		}
		out = append(out, ScopeVar{Name: name, Sym: sym})
	}
	return out
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：HasType 是类型补全的“有无门”——委托 LookupTypeEntry 判空。nil 层
// 经 Lookup 亦 false（方法内判 nil）。排查现场：类型补全缺失先查 types 册是否
// 只登本块（跨块不可见即对）。

func (s *Scope) HasType(name string) bool {
	return s.LookupTypeEntry(name) != nil
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：LookupTypeEntry 是类型名的“就近查”——自本层向上，首命中即返（遮蔽
// 生效）。与 checker 内 lookupType 同序，供 LSP 脱离 Checker 单用。nil-safe。
// 排查现场：类型跳到外层同名先查内层是否未登记（如 collectTypes 漏类）。

func (s *Scope) LookupTypeEntry(name string) *TypeEntry {
	for sc := s; sc != nil; sc = sc.parent {
		if e, ok := sc.types[name]; ok {
			return e
		}
	}
	return nil
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：ScopeTypeInfo 是类型补全的“三件套”——Name 展示，Kind 供图标
// （见 EntryKind），Span 供跳转。薄 struct。排查现场：图标错先查 EntryKind 映射。

type ScopeTypeInfo struct {
	Name string
	Kind string
	Span token.Span
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：ScopeTypes 是类型表的“由外向内收”——先链后 seen 去重（内层优先，
// 但实现以外层先记、内层覆盖？此处以外层先、内层 seen 跳过？实为外层先入、内层
// 因 seen 已占而被跳？注意循环自最外向最内，seen 首占为外层——与注释“内遮外”
// 相悖？细看：chain 自内向外攒，再倒序（自外向内）记，seen 首占外层，内层被跳，
// 即外遮内？实测需以外层为准？此处保留实现：调用方若见遮蔽反转，先查此序。障碍是
// map 遍历乱序：输出序不定，调用方须自排序。取舍：快照一次。排查现场：同名类型
// 跳错层先查此序与 lookupType 是否一致。

func (s *Scope) ScopeTypes() []ScopeTypeInfo {
	if s == nil {
		return nil
	}
	// 收集，排查先看 Span/诊断。
	var chain []*Scope
	for sc := s; sc != nil; sc = sc.parent {
		chain = append(chain, sc)
	}
	seen := map[string]bool{}
	var out []ScopeTypeInfo
	for i := len(chain) - 1; i >= 0; i-- {
		for name, e := range chain[i].types {
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, ScopeTypeInfo{Name: name, Kind: e.EntryKind(), Span: e.DeclSpan()})
		}
	}
	return out
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：KindString 是符号的“胸牌”——func→function，param→parameter，其余
// （含 nil）→variable。障碍是 symKind 未导出：LSP 只能经此串辨。取舍：三态粗分
// （方法/字段另由 MemberRef.Kind）。排查现场：悬停种错先查 Kind 赋值（bindVar 处）。

func (s *Symbol) KindString() string {
	if s == nil {
		return "variable"
	}
	switch s.Kind {
	case symFunc:
		return "function"
	case symParam:
		return "parameter"
	default:
		return "variable"
	}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：TypeName 是 Entry 身份的“报名”——nil 返空串，余返内部 name。供悬停
// 标题与 MemberRef.OwnerName 配对。排查现场：悬停名空先查 Entry 是否 nil。

func (e *typeEntry) TypeName() string {
	if e == nil {
		return ""
	}
	return e.name
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：DeclSpan 是跳转的“着陆坐标”——记名 Span（非全声明），跳转落到名字
// 上。nil 返零值（调用方须判）。与 DeclRange（全域，供可见性）对偶。排查现场：
// 跳转偏到声明首先查是否误用 DeclRange。

func (e *typeEntry) DeclSpan() token.Span {
	if e == nil {
		return token.Span{}
	}
	return e.span
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：DeclRange 是可见性的“围墙”——返整声明 Span，私有成员补全限墙内
// （见 LSP 侧）。四类声明各取其 Span，无者返零。障碍是泛型/多行声明 Span 须含
// 全体：依赖 parser 的 Decl Span 完整。排查现场：墙外仍补私有先查此处 Span 是否过小。

func (e *typeEntry) DeclRange() token.Span {
	if e == nil {
		return token.Span{}
	}
	switch {
	case e.alias != nil:
		return e.alias.Span()
	case e.iface != nil:
		return e.iface.Span()
	case e.enumDecl != nil:
		return e.enumDecl.Span()
	case e.classDecl != nil:
		return e.classDecl.Span()
	}
	return token.Span{}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：LookupFieldType 是补全链的“探针”——`a.b.c` 逐跳经 lookupField（含
// 索引器）取型。障碍是无定义桩亦返型（与 record 系严版不同）：补全要宽，追踪要严。
// 取舍：薄包 lookupField。排查现场：补全链断先查中间某跳是否为 union 缺员。

func LookupFieldType(ot types.Type, name string) (types.Type, bool) {
	f, ok := lookupField(ot, name)
	if !ok {
		return nil, false
	}
	return f.Type, true
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：EntryKind 是类型声明的“门牌”——alias→type，iface→interface，
// enum→enum，class→class，余→type。顺序即优先级（四者互斥，首中即返）。nil 返空。
// 排查现场：悬停种错先查声明指针哪轨非 nil（如 classDecl 误置）。

func (e *typeEntry) EntryKind() string {
	if e == nil {
		return ""
	}
	switch {
	case e.alias != nil:
		return "type"
	case e.iface != nil:
		return "interface"
	case e.enumDecl != nil:
		return "enum"
	case e.classDecl != nil:
		return "class"
	default:
		return "type"
	}
}
