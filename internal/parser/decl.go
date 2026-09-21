package parser

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/token"
)

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
		// Fix span to include export.
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
		// export function foo() ... end  (sugar for global)
		p.next()
		name := p.parseFuncName()
		fn := p.parseFuncBody(name != nil && name.Method != nil)
		sp := token.Span{Start: start.Span.Start, End: fn.Span().End, File: p.file}
		return &ast.FuncDefStmt{Name: name, Func: fn, S: sp}
	}
	if p.at(token.Local) {
		// export local ... : mark exported where applicable.
		st := p.parseLocal()
		if lf, ok := st.(*ast.LocalFuncDef); ok {
			lf.Exported = true
			lf.S.Start = start.Span.Start
		}
		return st
	}
	if p.at(token.MacroKw) {
		p.errorf("TL2005", p.cur().Span, "macros cannot be exported (they expand within their file)")
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	p.errorf("TL2005", p.cur().Span, "expected declaration after 'export'")
	p.synchronize()
	return &ast.EmptyStmt{S: start.Span}
}

func (p *Parser) parseDeclare() ast.Stmt {
	start := p.next()
	// declare only applies to type-space decls (no runtime emit).
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
		// declare function foo(a: T): R  (no body)
		p.next()
		name := p.parseFuncName()
		// Optional generics.
		var generics []*ast.GenericParam
		if p.at(token.Lt) {
			save := p.pos
			nd := len(p.diags)
			g := p.parseGenericParams()
			if g != nil && p.at(token.LParen) {
				generics = g
			} else {
				p.pos = save
				p.diags = p.diags[:nd]
			}
		}
		// Parse signature only: (params) [-> ret], no body.
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
		p.errorf("TL2006", p.cur().Span, "macros cannot be declared (they have no runtime value)")
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	p.errorf("TL2006", p.cur().Span, "expected declaration after 'declare'")
	p.synchronize()
	return &ast.EmptyStmt{S: start.Span}
}

// parseMacroDef parses:
//   macro Name(p1, p2) = expr            (expression macro)
//   macro Name(p1, p2) do stmts end      (statement macro)
// A trailing `...` collects extra call arguments (variadic macro).
// Parameters are strict identifiers; `self`/`super` are reserved.
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
					p.errorf("TL2041", dots.Span, "'...' must be the last macro parameter")
				}
				break
			}
			n := p.parseIdent()
			if n == nil {
				break
			}
			if n.Name == "self" || n.Name == "super" {
				p.errorf("TL2038", n.Span(), "macro parameter cannot be named '%s'", n.Name)
			}
			params = append(params, n)
			if !p.at(token.Comma) {
				break
			}
			p.next()
		}
	}
	p.expect(token.RParen, "')'")
	// Duplicate parameter names shadow confusingly; reject outright.
	seen := map[string]bool{}
	for _, n := range params {
		if seen[n.Name] {
			p.errorf("TL2039", n.Span(), "duplicate macro parameter '%s'", n.Name)
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
			p.errorf("TL2040", body.Ret.Span(), "statement macro body cannot return a value")
		}
		return &ast.MacroDef{Name: name, Params: params, HasVarArg: hasVA, Block: body, IsExpr: false, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	p.errorf("TL2001", p.cur().Span, "expected '=' or 'do' after macro parameters")
	p.synchronize()
	return &ast.EmptyStmt{S: start.Span}
}

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

// parseClassDecl parses:
//   [abstract] class Name<T> [extends Base] [implements I, ...] { members }
// Members are fields (`[mods] name[: T] [= init]`), methods
// (`[mods] name<T>(params)[: R | -> R] body end`) and at most one
// `constructor(params) body end`. In `declare` classes and on `abstract`
// members there is no body.
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

func (p *Parser) parseClassMember(declared bool) *ast.ClassMember {
	start := p.cur()
	vis := ast.Public
	visSeen := false
	isStatic, isReadonly, isAbstract := false, false, false
loop:
	for {
		switch {
		case p.at(token.PublicKw):
			if visSeen {
				p.errorf("TL2030", p.cur().Span, "duplicate visibility modifier")
			}
			p.next()
			vis, visSeen = ast.Public, true
		case p.at(token.PrivateKw):
			if visSeen {
				p.errorf("TL2030", p.cur().Span, "duplicate visibility modifier")
			}
			p.next()
			vis, visSeen = ast.Private, true
		case p.at(token.ProtectedKw):
			if visSeen {
				p.errorf("TL2030", p.cur().Span, "duplicate visibility modifier")
			}
			p.next()
			vis, visSeen = ast.Protected, true
		case p.at(token.StaticKw):
			if isStatic {
				p.errorf("TL2031", p.cur().Span, "duplicate 'static' modifier")
			}
			p.next()
			isStatic = true
		case p.at(token.ReadonlyKw):
			if isReadonly {
				p.errorf("TL2031", p.cur().Span, "duplicate 'readonly' modifier")
			}
			p.next()
			isReadonly = true
		case p.at(token.AbstractKw):
			if isAbstract {
				p.errorf("TL2031", p.cur().Span, "duplicate 'abstract' modifier")
			}
			p.next()
			isAbstract = true
		default:
			break loop
		}
	}
	name := p.parseIdentAllowSoft()
	if name == nil {
		return nil
	}
	isCtor := name.Name == "constructor"
	// Method or constructor: `name<T>(params)`.
	if p.at(token.LParen) || p.at(token.Lt) {
		var generics []*ast.GenericParam
		if p.at(token.Lt) {
			save := p.pos
			nd := len(p.diags)
			g := p.parseGenericParams()
			if g != nil && p.at(token.LParen) {
				generics = g
			} else {
				p.pos = save
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
			p.errorf("TL2032", sp, "constructor must not declare a return type")
			p.next()
			_ = p.parseType()
		}
		kind := ast.ClassMethod
		if isCtor {
			kind = ast.ClassConstructor
		}
		m := &ast.ClassMember{Kind: kind, Name: name, Visibility: vis, IsStatic: isStatic, IsReadonly: isReadonly, IsAbstract: isAbstract, Generics: generics, Params: params, HasVarArg: hasVA, VarArgName: vaName, VarArgType: vaTy, ReturnType: retTy, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
		if declared || isAbstract {
			m.S = token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}
			return m
		}
		if p.at(token.Comma) || p.at(token.Semi) || p.at(token.RBrace) {
			p.errorf("TL2033", p.cur().Span, "method '%s' must have a body ending with 'end'", name.Name)
			m.Body = &ast.Block{S: token.Span{Start: p.prevEnd(), End: p.prevEnd(), File: p.file}}
			m.S = token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}
			return m
		}
		m.Body = p.parseBlock()
		p.expect(token.End, "'end'")
		m.S = token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}
		return m
	}
	// Field: `name[: T] [= init]`.
	if isCtor {
		p.errorf("TL2034", name.Span(), "'constructor' must be declared as a method")
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
			p.errorf("TL2035", p.cur().Span, "field '%s' must not have an initializer in a 'declare' class", name.Name)
		}
		p.next()
		init = p.parseExpr()
	}
	if fty == nil && init == nil {
		p.errorf("TL2036", name.Span(), "field '%s' needs a type annotation or an initializer", name.Name)
		fty = &ast.NamedType{Name: "any", S: name.Span()}
	}
	if isAbstract {
		p.errorf("TL2037", start.Span, "only methods can be 'abstract'")
	}
	return &ast.ClassMember{Kind: ast.ClassField, Name: name, Visibility: vis, IsStatic: isStatic, IsReadonly: isReadonly, FieldType: fty, Init: init, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

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

func (p *Parser) parseInterfaceField() *ast.InterfaceField {
	start := p.cur()
	readonly := false
	if p.at(token.ReadonlyKw) {
		p.next()
		readonly = true
	}
	name := p.parseIdentAllowSoft()
	if name == nil {
		// Support method shorthand with string keys? No.
		return nil
	}
	optional := false
	if p.at(token.Question) {
		p.next()
		optional = true
	}
	// Method shorthand: name<T>(params) [: ret | -> ret]
	if p.at(token.LParen) || p.at(token.Lt) {
		// Generic methods: keep generics for explicit type arguments.
		var mgen []*ast.GenericParam
		if p.at(token.Lt) {
			save := p.pos
			nd := len(p.diags)
			mgen = p.parseGenericParams()
			if !p.at(token.LParen) {
				p.pos = save
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
