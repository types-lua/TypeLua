package parser

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
)

// 声明子系统物理布局：
//
//	parseExport / parseDeclare  ──► 分派到
//	  ├── parseClassDecl   (class/abstract class, members)
//	  ├── parseTypeDecl    (type alias / interface / enum)
//	  ├── parseGlobalFunc / parseAsyncGlobal  (function)
//	  ├── parseLocal       (local var/func/type/class/macro)
//	  └── parseMacroDef   (macro)
//
// parseClassDecl 成员循环：
//   modifiers(async/vis/static/readonly/abstract) ──► name ──►
//     方法：generics? params return? body(end or }) ；声明/抽象无体
//     字段：[: type] [= init]
//   恢复：synchronizeClassMember 推进到下一个成员起始。

// parseExport 解析 `export <decl>`：class/type/interface/enum/function/async/local 可导出，macro 不可。
//
// 设计者视角：parseExport 是模块门口的“盖章处”——吃掉 export 后按次 Token 分诊，
// 命中即解析本体再把 S.Start 前移到 export（高亮含章），LocalFuncDef 另立 Exported 旗。
// 障碍有二：一是 `export async function` 双词开头，需 isAsyncFuncStart 前瞻；
// 二是 macro 无运行时值，导出即记 ParseInvalidExport。取舍：非法导出不同情，
// synchronize 后回 EmptyStmt，让后文继续。不变量：成功节点的 S 必含 export 起点。
// 排查现场：若导出函数 Span 漏了 export，先看各分支的 S.Start 回写。
func (p *Parser) parseExport() ast.Stmt {
	start := p.next()
	if p.at(token.ClassKw) || (p.at(token.AbstractKw) && p.peek(1).Kind == token.ClassKw) {
		d := p.parseClassDecl(true, false, false)
		if cd, ok := d.(*ast.ClassDecl); ok {
			sp := cd.Span()
			sp.Start = start.Span.Start
			cd.S = sp
		}
		return d
	}
	if p.at(token.TypeKw) || p.at(token.InterfaceKw) || p.at(token.EnumKw) {
		d := p.parseTypeDecl(true, false)
		// Span 前移含 export，如 `export type T = number` 高亮含章。
		sp := d.Span()
		sp.Start = start.Span.Start
		switch n := d.(type) {
		case *ast.TypeAliasDecl:
			n.S = sp
		case *ast.InterfaceDecl:
			n.S = sp
		case *ast.EnumDecl:
			n.S = sp
		}
		return d
	}
	if p.at(token.Function) {
		// `export function foo() end` 即全局糖。
		p.next()
		name := p.parseFuncName()
		fn := p.parseFuncBody(name != nil && name.Method != nil)
		sp := token.Span{Start: start.Span.Start, End: fn.Span().End, File: p.file}
		return &ast.FuncDefStmt{Name: name, Func: fn, S: sp}
	}
	if p.isAsyncFuncStart() {
		// `export async function` 同全局异步。
		return p.parseAsyncGlobal()
	}
	if p.at(token.Local) {
		// `export local f = …` 透传 Exported 旗。
		st := p.parseLocal()
		if lf, ok := st.(*ast.LocalFuncDef); ok {
			lf.Exported = true
			lf.S.Start = start.Span.Start
		}
		return st
	}
	if p.at(token.MacroKw) {
		p.errorf(diag.ParseInvalidExport, p.cur().Span, "macros cannot be exported (they expand within their file)")
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	p.errorf(diag.ParseInvalidExport, p.cur().Span, "expected declaration after 'export'")
	p.synchronize()
	return &ast.EmptyStmt{S: start.Span}
}

// parseDeclare 解析 `declare <类型空间声明>`：class/type/interface/enum 与无体 function，macro 不可。
//
// 设计者视角：parseDeclare 是类型世界的“报关单”——只收无运行时发射的声明，
// function 形态只收签名（params+`->`/`: `返回，无 body，Body 置空块占位），身后无 end。
// 障碍是泛型 `<T>` 与调用括号的粘连：试探成功且后接 '(' 才认泛型，否则回退当比较。
// 取舍：declare class 的字段初值在 parseClassMember 内判死刑详见实现，排查看 Span。
// 而非此处一刀切，让错误锚在字段而非 declare。候选曾是复用 parseGlobalFunc 再掏空 body，
// 但签名与体解析交织，单列签名路更不易把 `end` 误吃。不变量：返回节点 Declared 恒真。
// 排查现场：若 declare function 吞了下句，先看 LParen expect 失败后的游标。
func (p *Parser) parseDeclare() ast.Stmt {
	start := p.next()
	// declare 只收类型空间（零运行时发射），如 `declare class C {}`。
	if p.at(token.ClassKw) || (p.at(token.AbstractKw) && p.peek(1).Kind == token.ClassKw) {
		d := p.parseClassDecl(false, true, false)
		if cd, ok := d.(*ast.ClassDecl); ok {
			sp := cd.Span()
			sp.Start = start.Span.Start
			cd.S = sp
		}
		return d
	}
	if p.at(token.TypeKw) || p.at(token.InterfaceKw) || p.at(token.EnumKw) {
		d := p.parseTypeDecl(false, true)
		sp := d.Span()
		sp.Start = start.Span.Start
		switch n := d.(type) {
		case *ast.TypeAliasDecl:
			n.S = sp
		case *ast.InterfaceDecl:
			n.S = sp
		case *ast.EnumDecl:
			n.S = sp
		}
		return d
	}
	if p.at(token.Function) {
		// `declare function foo(a: T): R` 只要签名无体。
		p.next()
		name := p.parseFuncName()
		// 可选泛型 `<T>`，后无 `(` 即回退。
		var generics []*ast.GenericParam
		if p.at(token.Lt) {
			save := p.saveCursor()
			nd := len(p.diags)
			g := p.parseGenericParams()
			if g != nil && p.at(token.LParen) {
				generics = g
			} else {
				p.restoreCursor(save)
				p.diags = p.diags[:nd]
			}
		}
		// 只收签名 `(params)[-> R]`，身后无 end。
		if _, ok := p.expect(token.LParen, "'('"); !ok {
			return &ast.EmptyStmt{S: start.Span}
		}
		params, hasVA, vaName, vaTy := p.parseParams()
		p.expect(token.RParen, "')'")
		var retTy ast.TypeNode
		if p.at(token.Arrow) {
			p.next()
			retTy = p.parseReturnType()
		} else if p.at(token.Colon) {
			p.next()
			retTy = p.parseType()
		}
		fn := &ast.FuncLit{Params: params, HasVarArg: hasVA, VarArgName: vaName, VarArgType: vaTy, ReturnType: retTy, Body: &ast.Block{}, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
		return &ast.FuncDefStmt{Name: name, Generics: generics, Func: fn, S: fn.S}
	}
	if p.at(token.MacroKw) {
		p.errorf(diag.ParseInvalidDeclare, p.cur().Span, "macros cannot be declared (they have no runtime value)")
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	p.errorf(diag.ParseInvalidDeclare, p.cur().Span, "expected declaration after 'declare'")
	p.synchronize()
	return &ast.EmptyStmt{S: start.Span}
}

// parseMacroDef 收两种宏：`macro N(p) = expr` 表达式宏，`macro N(p) do stmts end` 语句宏，尾 `...` 为变参。
// `self`/`super` 预留判死刑，重名 param 判死刑，`...` 必居末（否则 ParseMacroVarargLast）。
// 双形态分流：`=` 后 parseExpr 即表达式宏；`do` 后 inMacroBody++ 包 parseBlock（体内裸 Ident
// 可作语句占位，见 parseExprStmtOrAssign），`end` 封口，Ret 非空即 ParseMacroBodyReturn。
// 障碍是宏体内的 return 与块恢复交织：inMacroBody 旗让宏体内的语句级裸名合法化，待展开后复核。
// 取舍：签名错即 synchronize 回 EmptyStmt，不硬凑半截宏。不变量：IsExpr 与 Body/Block 互斥且必居其一。
// 排查现场：若语句宏误收 return 值，先看 inMacroBody 的 ++/-- 是否配对。
func (p *Parser) parseMacroDef() ast.Stmt {
	start := p.next() // macro
	name := p.parseIdent()
	if name == nil {
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	if _, ok := p.expect(token.LParen, "'('"); !ok {
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	var params []*ast.Ident
	hasVA := false
	if !p.at(token.RParen) {
		for {
			if p.at(token.DotDotDot) {
				dots := p.next()
				hasVA = true
				if !p.at(token.RParen) {
					p.errorf(diag.ParseMacroVarargLast, dots.Span, "'...' must be the last macro parameter")
				}
				break
			}
			n := p.parseIdent()
			if n == nil {
				break
			}
			if n.Name == "self" || n.Name == "super" {
				p.errorf(diag.ParseMacroParamReserved, n.Span(), "macro parameter cannot be named '%s'", n.Name)
			}
			params = append(params, n)
			if !p.at(token.Comma) {
				break
			}
			p.next()
		}
	}
	p.expect(token.RParen, "')'")
	// 重名形参如 `macro M(a,a)` 当场记账。
	seen := map[string]bool{}
	for _, n := range params {
		if seen[n.Name] {
			p.errorf(diag.ParseMacroParamDuplicate, n.Span(), "duplicate macro parameter '%s'", n.Name)
		}
		seen[n.Name] = true
	}
	if p.at(token.Assign) {
		p.next()
		body := p.parseExpr()
		if body == nil {
			p.synchronize()
			return &ast.EmptyStmt{S: start.Span}
		}
		return &ast.MacroDef{Name: name, Params: params, HasVarArg: hasVA, Body: body, IsExpr: true, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	if p.at(token.Do) {
		p.next()
		p.inMacroBody++
		body := p.parseBlock()
		p.inMacroBody--
		p.expect(token.End, "'end'")
		if body.Ret != nil {
			p.errorf(diag.ParseMacroBodyReturn, body.Ret.Span(), "statement macro body cannot return a value")
		}
		return &ast.MacroDef{Name: name, Params: params, HasVarArg: hasVA, Block: body, IsExpr: false, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	p.errorf(diag.ParseExpectedToken, p.cur().Span, "expected '=' or 'do' after macro parameters")
	p.synchronize()
	return &ast.EmptyStmt{S: start.Span}
}

// parseTypeDecl 解析 `type X<T> = T` / `interface X ... { }` / `enum X { }` 三形态，exported/declared 透传。
//
// 设计者视角：parseTypeDecl 是类型声明的“三岔口”——type 走别名路（泛型+`=`+parseType），
// interface 走继承+字段路（extends 逗号列、`{` 后 parseInterfaceField 循环、synchronizeInterface
// 局部恢复），enum 走成员路（AllowSoft 名+可选 `= expr`）。障碍是接口/枚举的分隔符
// `,`/`;` 可混用：循环首即跳过，让尾逗号合法。取舍：字段错只跳一格（synchronize 后 continue），
// 整表不作废。不变量：Exported/Declared 原样透传，S 从关键字到收尾符。
// 排查现场：若 interface 缺 `}` 后外层被吞，先看 expect(RBrace) 失败后是否继续了外层循环。
func (p *Parser) parseTypeDecl(exported, declared bool) ast.Stmt {
	switch p.cur().Kind {
	case token.TypeKw:
		start := p.next()
		name := p.parseIdent()
		if name == nil {
			p.synchronize()
			return &ast.EmptyStmt{S: start.Span}
		}
		var generics []*ast.GenericParam
		if p.at(token.Lt) {
			generics = p.parseGenericParams()
		}
		p.expect(token.Assign, "'='")
		ty := p.parseType()
		sp := token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}
		return &ast.TypeAliasDecl{Name: name, Generics: generics, Type: ty, Exported: exported, Declared: declared, S: sp}
	case token.InterfaceKw:
		start := p.next()
		name := p.parseIdent()
		if name == nil {
			p.synchronize()
			return &ast.EmptyStmt{S: start.Span}
		}
		var generics []*ast.GenericParam
		if p.at(token.Lt) {
			generics = p.parseGenericParams()
		}
		var extends []ast.TypeNode
		if p.at(token.ExtendsKw) {
			p.next()
			for {
				extends = append(extends, p.parseType())
				if !p.at(token.Comma) {
					break
				}
				p.next()
			}
		}
		p.expect(token.LBrace, "'{'")
		var fields []*ast.InterfaceField
		for !p.at(token.RBrace) && !p.at(token.EOF) {
			if p.at(token.Semi) || p.at(token.Comma) {
				p.next()
				continue
			}
			f := p.parseInterfaceField()
			if f == nil {
				p.synchronizeInterface()
				continue
			}
			fields = append(fields, f)
			if p.at(token.Comma) || p.at(token.Semi) {
				p.next()
			}
		}
		p.expect(token.RBrace, "'}'")
		return &ast.InterfaceDecl{Name: name, Generics: generics, Extends: extends, Fields: fields, Exported: exported, Declared: declared, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	case token.EnumKw:
		start := p.next()
		name := p.parseIdent()
		if name == nil {
			p.synchronize()
			return &ast.EmptyStmt{S: start.Span}
		}
		p.expect(token.LBrace, "'{'")
		var members []*ast.EnumMember
		for !p.at(token.RBrace) && !p.at(token.EOF) {
			if p.at(token.Semi) || p.at(token.Comma) {
				p.next()
				continue
			}
			mstart := p.cur()
			mname := p.parseIdentAllowSoft()
			if mname == nil {
				p.synchronizeInterface()
				continue
			}
			var val ast.Expr
			if p.at(token.Assign) {
				p.next()
				val = p.parseExpr()
			}
			members = append(members, &ast.EnumMember{Name: mname, Value: val, S: token.Span{Start: mstart.Span.Start, End: p.prevEnd(), File: p.file}})
			if p.at(token.Comma) || p.at(token.Semi) {
				p.next()
			}
		}
		p.expect(token.RBrace, "'}'")
		return &ast.EnumDecl{Name: name, Members: members, Exported: exported, Declared: declared, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	return nil
}

// parseClassDecl 收 `[abstract] class N<T> [extends B] [implements I,…] {成员}`，字段/方法/唯一 constructor，declare/abstract 无体。
// implements 多实现、`{` 后成员循环如 `{`，排查看 Span。
// `}` 封口，exported/declared/isLocal 三旗透传。障碍是 extends/implements 后的类型逗号列
// 与成员循环的逗号分隔同形：前者在 `{` 前收，后者在 `{` 后收，以 LBrace 为界绝不混。
// 取舍：成员错只跳一格（同步后 continue），整类不作废；`{` 缺席即同步回 Empty，不硬搭空类。
// 不变量：Members 保序，S 从 abstract/class 起到 `}`。排查现场：若 implements 列少一项，
// 先看逗号循环的 break 条件。
func (p *Parser) parseClassDecl(exported, declared, isLocal bool) ast.Stmt {
	start := p.cur()
	abstract := false
	if p.at(token.AbstractKw) {
		p.next()
		abstract = true
	}
	if _, ok := p.expect(token.ClassKw, "'class'"); !ok {
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	name := p.parseIdent()
	if name == nil {
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	var generics []*ast.GenericParam
	if p.at(token.Lt) {
		generics = p.parseGenericParams()
	}
	var extends ast.TypeNode
	if p.at(token.ExtendsKw) {
		p.next()
		extends = p.parseType()
	}
	var implements []ast.TypeNode
	if p.at(token.ImplementsKw) {
		p.next()
		for {
			implements = append(implements, p.parseType())
			if !p.at(token.Comma) {
				break
			}
			p.next()
		}
	}
	if _, ok := p.expect(token.LBrace, "'{'"); !ok {
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	var members []*ast.ClassMember
	for !p.at(token.RBrace) && !p.at(token.EOF) {
		if p.at(token.Comma) || p.at(token.Semi) {
			p.next()
			continue
		}
		m := p.parseClassMember(declared)
		if m == nil {
			p.synchronizeClassMember()
			continue
		}
		members = append(members, m)
		if p.at(token.Comma) || p.at(token.Semi) {
			p.next()
		}
	}
	p.expect(token.RBrace, "'}'")
	sp := token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}
	return &ast.ClassDecl{Name: name, Generics: generics, Extends: extends, Implements: implements, Members: members, Exported: exported, Declared: declared, Abstract: abstract, IsLocal: isLocal, S: sp}
}

// parseClassMember 解析单个类成员：修饰符 async/vis/static/readonly/abstract + 名 + 方法/字段双路。
//
// 设计者视角：parseClassMember 是成员的“安检+分诊”——前后两处 async 嗅探（`async name(`/`<`
// 且同行） guards `async: string` 字段不被误收；vis/static/readonly/abstract 循环收章，
// 重复章即时记账如 `(`/`<`，排查看 Span。
// （泛型试探+params+返回+declare/abstract 无体 vs parseFuncBlock 有体，缺体在 `,`/`;`/`}`
// 前即 MethodBodyMissing）；否则字段路（`[:T] [=init]`，无 type 无 init 即 FieldNeedsTypeOrInit
// 补 any，abstract 字段即 OnlyMethodsAbstract）。障碍有三：ctor 不许 async/不许返回注记、
// `constructor` 裸作字段名即 CtorNotMethod。取舍：方法泛型试探失败即回退按无泛型续，
// 错判成本一次回退。不变量：失败回 nil（调用方同步），成功 S 从首修饰/名到体尾。
// 排查现场：若 `static async fetch()` 的 async 丢失，先看第二嗅探（修饰后）是否生效。
func (p *Parser) parseClassMember(declared bool) *ast.ClassMember {
	start := p.cur()
	// 前置 `async greet()` 须后跟名+`(`/`<`，否则 `async: string` 即字段。
	isAsync := false
	if p.cur().Kind == token.Ident && p.cur().Literal == "async" &&
		(p.peek(1).Kind == token.Ident || isSoftKw(p.peek(1).Kind)) &&
		(p.peek(2).Kind == token.LParen || p.peek(2).Kind == token.Lt) &&
		p.peek(1).Span.Start.Line == p.cur().Span.Start.Line {
		isAsync = true
		p.next() // async
		start = p.cur()
	}
	vis := ast.Public
	visSeen := false
	isStatic, isReadonly, isAbstract := false, false, false
loop:
	for {
		switch {
		case p.at(token.PublicKw):
			if visSeen {
				p.errorf(diag.ParseDuplicateVisibility, p.cur().Span, "duplicate visibility modifier")
			}
			p.next()
			vis, visSeen = ast.Public, true
		case p.at(token.PrivateKw):
			if visSeen {
				p.errorf(diag.ParseDuplicateVisibility, p.cur().Span, "duplicate visibility modifier")
			}
			p.next()
			vis, visSeen = ast.Private, true
		case p.at(token.ProtectedKw):
			if visSeen {
				p.errorf(diag.ParseDuplicateVisibility, p.cur().Span, "duplicate visibility modifier")
			}
			p.next()
			vis, visSeen = ast.Protected, true
		case p.at(token.StaticKw):
			if isStatic {
				p.errorf(diag.ParseDuplicateModifier, p.cur().Span, "duplicate 'static' modifier")
			}
			p.next()
			isStatic = true
		case p.at(token.ReadonlyKw):
			if isReadonly {
				p.errorf(diag.ParseDuplicateModifier, p.cur().Span, "duplicate 'readonly' modifier")
			}
			p.next()
			isReadonly = true
		case p.at(token.AbstractKw):
			if isAbstract {
				p.errorf(diag.ParseDuplicateModifier, p.cur().Span, "duplicate 'abstract' modifier")
			}
			p.next()
			isAbstract = true
		default:
			break loop
		}
	}
	// `async` between modifiers and name (`static async fetch()`).
	if !isAsync && p.cur().Kind == token.Ident && p.cur().Literal == "async" &&
		(p.peek(1).Kind == token.Ident || isSoftKw(p.peek(1).Kind)) &&
		(p.peek(2).Kind == token.LParen || p.peek(2).Kind == token.Lt) &&
		p.peek(1).Span.Start.Line == p.cur().Span.Start.Line {
		isAsync = true
		p.next() // async
	}
	name := p.parseIdentAllowSoft()
	if name == nil {
		return nil
	}
	isCtor := name.Name == "constructor"
	if isAsync && isCtor {
		p.errorf(diag.ParseInvalidCtorSignature, name.Span(), "constructor must not be 'async'")
		isAsync = false
	}
	// 方法/构造 `名<T>(params)`，如 `get<T>(k: T)`。
	if p.at(token.LParen) || p.at(token.Lt) {
		var generics []*ast.GenericParam
		if p.at(token.Lt) {
			save := p.saveCursor()
			nd := len(p.diags)
			g := p.parseGenericParams()
			if g != nil && p.at(token.LParen) {
				generics = g
			} else {
				p.restoreCursor(save)
				p.diags = p.diags[:nd]
			}
		}
		if _, ok := p.expect(token.LParen, "'('"); !ok {
			return nil
		}
		params, hasVA, vaName, vaTy := p.parseParams()
		p.expect(token.RParen, "')'")
		var retTy ast.TypeNode
		if !isCtor {
			if p.at(token.Arrow) {
				p.next()
				retTy = p.parseReturnType()
			} else if p.at(token.Colon) {
				p.next()
				retTy = p.parseType()
			}
		} else if p.at(token.Arrow) || p.at(token.Colon) {
			sp := p.cur().Span
			p.errorf(diag.ParseInvalidCtorSignature, sp, "constructor must not declare a return type")
			p.next()
			_ = p.parseType()
		}
		kind := ast.ClassMethod
		if isCtor {
			kind = ast.ClassConstructor
		}
		m := &ast.ClassMember{Kind: kind, Name: name, Visibility: vis, IsStatic: isStatic, IsReadonly: isReadonly, IsAbstract: isAbstract, IsAsync: isAsync, Generics: generics, Params: params, HasVarArg: hasVA, VarArgName: vaName, VarArgType: vaTy, ReturnType: retTy, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
		if declared || isAbstract {
			m.S = token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}
			return m
		}
		if p.at(token.Comma) || p.at(token.Semi) || p.at(token.RBrace) {
			p.errorf(diag.ParseMethodBodyMissing, p.cur().Span, "method '%s' must have a body ending with 'end'", name.Name)
			m.Body = &ast.Block{S: token.Span{Start: p.prevEnd(), End: p.prevEnd(), File: p.file}}
			m.S = token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}
			return m
		}
		m.Body = p.parseFuncBlock()
		m.S = token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}
		return m
	}
	// Field: `name[: T] [= init]`.
	if isCtor {
		p.errorf(diag.ParseCtorNotMethod, name.Span(), "'constructor' must be declared as a method")
		return nil
	}
	var fty ast.TypeNode
	if p.at(token.Colon) {
		p.next()
		fty = p.parseType()
	}
	var init ast.Expr
	if p.at(token.Assign) {
		if declared {
			p.errorf(diag.ParseDeclareFieldInit, p.cur().Span, "field '%s' must not have an initializer in a 'declare' class", name.Name)
		}
		p.next()
		init = p.parseExpr()
	}
	if fty == nil && init == nil {
		p.errorf(diag.ParseFieldNeedsTypeOrInit, name.Span(), "field '%s' needs a type annotation or an initializer", name.Name)
		fty = &ast.NamedType{Name: "any", S: name.Span()}
	}
	if isAbstract {
		p.errorf(diag.ParseOnlyMethodsAbstract, start.Span, "only methods can be 'abstract'")
	}
	return &ast.ClassMember{Kind: ast.ClassField, Name: name, Visibility: vis, IsStatic: isStatic, IsReadonly: isReadonly, FieldType: fty, Init: init, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

// synchronizeClassMember 在类体内推进到下一成员起点（Ident/软关键字/`}`），恢复成员级错误。
//
// 设计者视角：它是类大厅的“叫号机”——坏成员后吃到 `,`/`;`（消费即停）或下个像名字的
// Token（Ident/软关键字即停，不消费）再开工，避免把整类吞掉。障碍是修饰符亦为软关键字：
// 取舍是见软关键字即停（宁早不停晚），残修饰留给下轮 parseClassMember 再判。
// 不变量：EOF/RBrace 前必停且不消费 `}`。排查现场：若坏成员后下个好成员仍丢失，
// 先看停机条件是否漏了 isSoftKw。
func (p *Parser) synchronizeClassMember() {
	for {
		k := p.cur().Kind
		if k == token.EOF || k == token.RBrace {
			return
		}
		if k == token.Comma || k == token.Semi {
			p.next()
			return
		}
		if k == token.Ident || isSoftKw(k) {
			return
		}
		p.next()
	}
}

// parseInterfaceField 解析单个接口字段：`[readonly] name[?] [: T | <T>(params): R]`（方法糖与属性双形态）。
//
// 设计者视角：parseInterfaceField 是接口的“柜台”——readonly 章先收，`?` 可选后收，
// 后接 `(`/`<` 即方法糖（泛型试探+parseFuncTypeParams+`:`/`->` 返回，包成 FuncType），
// 否则 expect(':') 收属性类型。障碍是方法泛型 `<` 与比较同形：试探后无 '(' 即回退按
// 属性续，零副作用。取舍：属性缺 ':' 只记账不停工（ty 为 nil 仍成 Field），让后文继续。
// 不变量：失败回 nil（调用方同步），成功 S 从 readonly/名到类型尾。
// 排查现场：若 `m<T>(x: T): T` 的泛型丢失，先看试探后 at(LParen) 是否为真。
func (p *Parser) parseInterfaceField() *ast.InterfaceField {
	start := p.cur()
	readonly := false
	if p.at(token.ReadonlyKw) {
		p.next()
		readonly = true
	}
	name := p.parseIdentAllowSoft()
	if name == nil {
		// 字符串键方法糖不支持，此处只收 Ident 名。
		return nil
	}
	optional := false
	if p.at(token.Question) {
		p.next()
		optional = true
	}
	// 方法糖 `名<T>(params)[: R|-> R]`，如 `get<T>(k: T): T`。
	if p.at(token.LParen) || p.at(token.Lt) {
		// 泛型方法 `<T>` 留给显式实参。
		var mgen []*ast.GenericParam
		if p.at(token.Lt) {
			save := p.saveCursor()
			nd := len(p.diags)
			mgen = p.parseGenericParams()
			if !p.at(token.LParen) {
				p.restoreCursor(save)
				p.diags = p.diags[:nd]
				mgen = nil
			}
		}
		if _, ok := p.expect(token.LParen, "'('"); !ok {
			return nil
		}
		params, hasVA, vaTy := p.parseFuncTypeParams()
		p.expect(token.RParen, "')'")
		var rets []ast.TypeNode
		if p.at(token.Colon) {
			p.next()
			rets = []ast.TypeNode{p.parseType()}
		} else if p.at(token.Arrow) {
			p.next()
			rt := p.parseReturnType()
			rets = returnTypeToList(rt)
		}
		ft := &ast.FuncType{Params: params, HasVarArg: hasVA, VarArgType: vaTy, Returns: rets, Generics: mgen, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
		return &ast.InterfaceField{Name: name, Type: ft, Optional: optional, Readonly: readonly, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	p.expect(token.Colon, "':'")
	ty := p.parseType()
	return &ast.InterfaceField{Name: name, Type: ty, Optional: optional, Readonly: readonly, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

// returnTypeToList 把 `->` 返回类型摊平成 []TypeNode：nil 即空，Tuple 拆元，void 即空，余者单包。
//
// 设计者视角：它是函数类型 Returns 的“归一化漏斗”——parseReturnType 产出的三种方言
// （nil/Tuple/void/单体）在此统一成切片，checker 遍历 Returns 即得定长/多值，无需二次
// switch。障碍是 `void` 有名无实：NamedType(void) 必须摊成空片，否则会被当一个返回值。
// 取舍：Tuple 拆元（多返回），Paren 不拆（括号语义），泾渭分明。不变量：纯函数，无副作用。
// 排查现场：若 `-> ()` 被当一个返回，先看 void 名的匹配是否含 Args 非空的误伤。
func returnTypeToList(rt ast.TypeNode) []ast.TypeNode {
	if rt == nil {
		return nil
	}
	if tup, ok := rt.(*ast.TupleType); ok {
		return tup.Elems
	}
	if named, ok := rt.(*ast.NamedType); ok && named.Name == "void" && len(named.Args) == 0 {
		return nil
	}
	return []ast.TypeNode{rt}
}
