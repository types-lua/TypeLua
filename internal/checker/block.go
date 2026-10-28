package checker

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// 设计者视角：collectTypes 先把本块所有类型名登记进 Scope.types（两遍
// 的第一遍），再 checkStmt 逐语句；这样类型声明的顺序与相互引用都不阻塞。
// recordScope 把作用域与块 span 绑定，供 LSP 做作用域查询。

// Block checking and per-block type collection.
//
// 设计者视角：块检查是“先点名、后上课”的两遍制，好比开学先收花名册再讲课——
// collectTypes 把本块四类声明（alias/interface/enum/class）先登记进 Scope.types，
// checkBlock 再逐语句检查。障碍是前向引用与互递归（A 用 B、B 用 A）：若边走边
// 登记，后声明的名字在前语句里查不到；先整块登记即解。候选：全局预扫 vs 逐块
// 预扫；选逐块，块级作用域的遮蔽仍精确。不变量：collectTypes 只写 types 册，
// 不碰 vars。排查现场：前向类型报 undefined，先查是否绕过 checkBlock 直调
// checkStmt 漏了 collectTypes。

// ---------- blocks ----------

// recordTypeDef records a type declaration name.
//
// 设计者视角：recordTypeDef 是类型名的“出生登记”——collectTypes 每登记一个
// typeEntry 即在此留 IsDef 打卡，供跳转到定义。障碍是重复声明不应二次登记
// （否则跳转到错的副本）：collectTypes 内已用 ok 分流，此处只做 nil 守卫。
// 取舍：登记 best-effort，Index 为 nil 即返。不变量：Span 为声明名 Span。
// 排查现场：类型跳转失灵先查此处的 Entry 指针是否为登记的那一个。
func (c *Checker) recordTypeDef(span token.Span, e *typeEntry) {
	if c.Index == nil || e == nil {
		return
	}
	c.Index.Uses = append(c.Index.Uses, SymUse{Span: span, IsDef: true, Entry: e})
}

// collectTypes pre-registers block-local type names into sc.
//
// 设计者视角：collectTypes 是块内类型声明的“抢先占座”——四类声明各占一 Entry，
// 重名即报 DuplicateType。障碍是同名值与类型共存合法（分表），故只查 sc.types
// 不查 vars；类/泛型参数原样存 params 供实例化。取舍：Entry 先占位、类型体延迟
// 到 resolve 时再算（防递归）。不变量：同块同名只留首个 Entry。排查现场：泛型
// 实例维度错乱先查 params 是否在此存对。
func (c *Checker) collectTypes(b *ast.Block, sc *Scope) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		switch n := st.(type) {
		case *ast.TypeAliasDecl:
			if _, ok := sc.types[n.Name.Name]; !ok {
				e := &typeEntry{name: n.Name.Name, params: n.Generics, alias: n, span: n.Name.Span(), cacheInst: map[string]types.Type{}}
				sc.types[n.Name.Name] = e
				c.recordTypeDef(n.Name.Span(), e)
			} else {
				c.errorf(diag.DuplicateType, n.Span(), "duplicate type '%s'", n.Name.Name)
			}
		case *ast.InterfaceDecl:
			if _, ok := sc.types[n.Name.Name]; !ok {
				e := &typeEntry{name: n.Name.Name, params: n.Generics, iface: n, span: n.Name.Span(), cacheInst: map[string]types.Type{}}
				sc.types[n.Name.Name] = e
				c.recordTypeDef(n.Name.Span(), e)
			} else {
				c.errorf(diag.DuplicateType, n.Span(), "duplicate type '%s'", n.Name.Name)
			}
		case *ast.EnumDecl:
			if _, ok := sc.types[n.Name.Name]; !ok {
				e := &typeEntry{name: n.Name.Name, enumDecl: n, span: n.Name.Span(), cacheInst: map[string]types.Type{}}
				sc.types[n.Name.Name] = e
				c.recordTypeDef(n.Name.Span(), e)
			} else {
				c.errorf(diag.DuplicateType, n.Span(), "duplicate type '%s'", n.Name.Name)
			}
		case *ast.ClassDecl:
			if _, ok := sc.types[n.Name.Name]; !ok {
				e := &typeEntry{name: n.Name.Name, params: n.Generics, classDecl: n, span: n.Name.Span(), cacheInst: map[string]types.Type{}}
				sc.types[n.Name.Name] = e
				c.recordTypeDef(n.Name.Span(), e)
			} else {
				c.errorf(diag.DuplicateType, n.Span(), "duplicate type '%s'", n.Name.Name)
			}
		}
	}
}

// checkBlock checks a block with per-block type pre-registration.
//
// 设计者视角：checkBlock 是块的“标准动线”——collectTypes 点名、recordScope 钉
// 户型图、逐语句 checkStmt、末尾 checkReturn。障碍是空块（b==nil）多见于空文件/
// 空函数体：必须早返，否则解引用 panic。取舍：作用域 push/pop 留给调用方（do/
// 函数体各自开层），此处只管本层内容。不变量：进出时 c.cur() 为同层。排查现场：
// 返回契约错乱先查调用方 retStack 是否配套，作用域泄漏先查上层 push/pop。
func (c *Checker) checkBlock(b *ast.Block) {
	if b == nil {
		return
	}
	c.collectTypes(b, c.cur())
	c.recordScope(c.cur(), b.Span())
	for _, st := range b.Stmts {
		c.checkStmt(st)
	}
	if b.Ret != nil {
		c.checkReturn(b.Ret)
	}
}
