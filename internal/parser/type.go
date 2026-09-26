package parser

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
)

// ---------- types ----------

func (p *Parser) parseType() ast.TypeNode { return p.parseConditional() }

// parseConditional parses `Check extends Extends ? True : False`
// (right-associative on both branches, like TS). The `?` collides with
// the Optional postfix `T?`; isConditionalQ disambiguates.
func (p *Parser) parseConditional() ast.TypeNode {
	check := p.parseUnion()
	if check == nil || !p.at(token.ExtendsKw) {
		return check
	}
	p.next() // extends
	p.extendsDepth++
	ext := p.parseUnion()
	p.extendsDepth--
	if ext == nil {
		return check
	}
	if _, ok := p.expect(token.Question, "'?'"); !ok {
		return check
	}
	trueBranch := p.parseConditional()
	if trueBranch == nil {
		return check
	}
	if _, ok := p.expect(token.Colon, "':'"); !ok {
		return check
	}
	falseBranch := p.parseConditional()
	if falseBranch == nil {
		return check
	}
	return &ast.ConditionalType{Check: check, Extends: ext, True: trueBranch, False: falseBranch, S: token.Span{Start: check.Span().Start, End: p.prevEnd(), File: p.file}}
}

// isConditionalQ reports whether the current `?` opens conditional branches
// (`? True :`) rather than forming Optional `T?`. Speculative and side
// effect free: restores position and diagnostics either way. A depth trip
// inside speculation just means "not a conditional" (the real parse takes
// the shallower Optional path).
func (p *Parser) isConditionalQ() (ok bool) {
	if !p.at(token.Question) {
		return false
	}
	// Fast path: `? True :` requires True to start a type. When the token
	// after `?` cannot open a type (e.g. `|` in `A?|B?`, `,`/`]`/`)`/EOF`),
	// this is plain Optional — skip the full speculative parse entirely.
	// This turns the common `A?|B?|C?` chain from O(n^2) into O(n).
	switch p.peek(1).Kind {
	case token.Ident, token.LParen, token.LBrack, token.LBrace,
		token.String, token.Number, token.True, token.False, token.Nil,
		token.DotDotDot, token.KeyofKw, token.TypeofKw, token.InferKw:
		// May open a type; fall through to speculation.
	default:
		return false
	}
	saveCur, saveDiags := p.saveCursor(), len(p.diags)
	saveDepth := p.depth
	defer func() {
		if r := recover(); r != nil {
			if _, isBail := r.(depthBailout); !isBail {
				panic(r)
			}
			ok = false
		}
		p.restoreCursor(saveCur)
		p.diags = p.diags[:saveDiags]
		p.depth = saveDepth
	}()
	p.next() // ?
	branch := p.parseType()
	ok = branch != nil && p.at(token.Colon)
	return ok
}

func (p *Parser) parseUnion() ast.TypeNode {
	first := p.parseIntersection()
	if first == nil {
		return nil
	}
	if !p.at(token.Pipe) {
		return first
	}
	types := []ast.TypeNode{first}
	for p.at(token.Pipe) {
		p.next()
		types = append(types, p.parseIntersection())
	}
	return &ast.UnionType{Types: types, S: token.Span{Start: first.Span().Start, End: p.prevEnd(), File: p.file}}
}

func (p *Parser) parseIntersection() ast.TypeNode {
	first := p.parseTypePostfix()
	if first == nil {
		return nil
	}
	if !p.at(token.Amp) {
		return first
	}
	types := []ast.TypeNode{first}
	for p.at(token.Amp) {
		p.next()
		types = append(types, p.parseTypePostfix())
	}
	return &ast.IntersectionType{Types: types, S: token.Span{Start: first.Span().Start, End: p.prevEnd(), File: p.file}}
}

func (p *Parser) parseTypePostfix() ast.TypeNode {
	t := p.parsePrimaryType()
	if t == nil {
		return nil
	}
	for {
		if p.at(token.Question) {
			// `? True :` belongs to an enclosing conditional, not Optional.
			if p.isConditionalQ() {
				break
			}
			q := p.next()
			t = &ast.OptionalType{Elem: t, S: token.Span{Start: t.Span().Start, End: q.Span.End, File: p.file}}
			continue
		}
		if p.at(token.LBrack) && p.peek(1).Kind == token.RBrack {
			p.next()
			p.next()
			t = &ast.ArrayType{Elem: t, S: token.Span{Start: t.Span().Start, End: p.prevEnd(), File: p.file}}
			continue
		}
		if p.at(token.LBrack) {
			// Indexed access `T[K]` (empty brackets stay Array above).
			p.next() // [
			idx := p.parseType()
			p.expect(token.RBrack, "']'")
			if idx == nil {
				break
			}
			t = &ast.IndexedAccessType{Obj: t, Index: idx, S: token.Span{Start: t.Span().Start, End: p.prevEnd(), File: p.file}}
			continue
		}
		break
	}
	return t
}

func (p *Parser) parsePrimaryType() ast.TypeNode {
	c := p.cur()
	p.enterDepth(c.Span)
	defer p.leaveDepth()
	switch c.Kind {
	case token.LParen:
		return p.parseParenOrFuncType()
	case token.LBrack:
		start := p.next()
		var elems []ast.TypeNode
		if !p.at(token.RBrack) {
			for {
				elems = append(elems, p.parseType())
				if !p.at(token.Comma) {
					break
				}
				p.next()
			}
		}
		p.expect(token.RBrack, "']'")
		return &ast.TupleType{Elems: elems, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	case token.LBrace:
		return p.parseTableType()
	case token.String, token.Number, token.True, token.False:
		p.next()
		return &ast.LiteralType{Value: c.Literal, S: c.Span}
	case token.Nil:
		p.next()
		return &ast.NamedType{Name: "nil", S: c.Span}
	case token.DotDotDot:
		// `...T` rest marker (tuple patterns, vararg positions).
		// Takes a full postfix type so `...unknown[]` means rest-of-unknown.
		p.next()
		elem := p.parseTypePostfix()
		return &ast.NamedType{Name: "...", Args: []ast.TypeNode{elem}, S: token.Span{Start: c.Span.Start, End: p.prevEnd(), File: p.file}}
	case token.KeyofKw:
		start := p.next()
		elem := p.parsePrimaryType()
		return &ast.KeyofType{Elem: elem, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	case token.TypeofKw:
		start := p.next()
		p.expect(token.LParen, "'('")
		e := p.parseExpr()
		p.expect(token.RParen, "')'")
		return &ast.TypeofType{Expr: e, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	case token.InferKw:
		start := p.next()
		name := p.parseIdent()
		if name == nil {
			return &ast.NamedType{Name: "any", S: start.Span}
		}
		return &ast.InferType{Name: name, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	case token.Ident:
		return p.parseNamedType()
	default:
		p.errorf(diag.ParseExpectedType, c.Span, "expected type, got %s", c.Kind)
		// Synthesize any to continue.
		p.next()
		return &ast.NamedType{Name: "any", S: c.Span}
	}
}

func (p *Parser) parseNamedType() ast.TypeNode {
	first := p.next()
	name := first.Literal
	// Qualified a.b.C
	for p.at(token.Dot) && p.peek(1).Kind == token.Ident {
		p.next()
		n := p.next()
		name += "." + n.Literal
	}
	var args []ast.TypeNode
	if p.at(token.Lt) {
		p.next() // <
		if !p.atGt() && p.cur().Kind != token.EOF {
			for {
				args = append(args, p.parseType())
				if p.at(token.Comma) {
					p.next()
					continue
				}
				break
			}
		}
		p.consumeGt()
	}
	return &ast.NamedType{Name: name, Args: args, S: token.Span{Start: first.Span.Start, End: p.prevEnd(), File: p.file}}
}

func (p *Parser) parseParenOrFuncType() ast.TypeNode {
	start := p.next() // (
	// Empty () => void-ish unit.
	if p.at(token.RParen) {
		p.next()
		if p.at(token.Arrow) {
			p.next()
			ret := p.parseReturnType()
			return &ast.FuncType{Returns: returnTypeToList(ret), S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
		}
		return &ast.ParenType{Elem: &ast.NamedType{Name: "void", S: start.Span}, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	// Tentatively parse as func params.
	save := p.saveCursor()
	ndiags := len(p.diags)
	params, hasVA, vaTy, ok := p.tryParseFuncTypeParams()
	if ok && p.at(token.RParen) {
		p.next()
		if p.at(token.Arrow) {
			p.next()
			ret := p.parseReturnType()
			return &ast.FuncType{Params: params, HasVarArg: hasVA, VarArgType: vaTy, Returns: returnTypeToList(ret), S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
		}
		// Not a func type; fall through to plain types. If params were all unnamed, we can reuse.
		allUnnamed := !hasVA
		var elems []ast.TypeNode
		for _, pr := range params {
			if pr.Name != nil || pr.Optional {
				allUnnamed = false
				break
			}
			elems = append(elems, pr.Type)
		}
		if allUnnamed {
			if len(elems) == 1 {
				return &ast.ParenType{Elem: elems[0], S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
			}
			return &ast.TupleType{Elems: elems, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
		}
	}
	// Backtrack and parse as plain type list.
	p.restoreCursor(save)
	p.diags = p.diags[:ndiags]
	var elems []ast.TypeNode
	for {
		elems = append(elems, p.parseType())
		if !p.at(token.Comma) {
			break
		}
		p.next()
	}
	p.expect(token.RParen, "')'")
	if p.at(token.Arrow) {
		// `(A, B) -> R` with unnamed params.
		p.next()
		ret := p.parseReturnType()
		var params []*ast.FuncTypeParam
		for _, e := range elems {
			params = append(params, &ast.FuncTypeParam{Type: e, S: e.Span()})
		}
		return &ast.FuncType{Params: params, Returns: returnTypeToList(ret), S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	if len(elems) == 1 {
		return &ast.ParenType{Elem: elems[0], S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	return &ast.TupleType{Elems: elems, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

func (p *Parser) tryParseFuncTypeParams() ([]*ast.FuncTypeParam, bool, ast.TypeNode, bool) {
	var params []*ast.FuncTypeParam
	hasVA := false
	var vaTy ast.TypeNode
	for !p.at(token.RParen) && !p.at(token.EOF) {
		if p.at(token.DotDotDot) {
			p.next()
			hasVA = true
			// ...name? : T  or ...T
			if p.at(token.Ident) || isSoftKw(p.cur().Kind) {
				// Could be `...T` (unnamed vararg of type T) or `...rest: T`.
				// Lookahead: if next is Colon/Comma/RParen, it's a name.
				n := p.cur()
				if p.peek(1).Kind == token.Colon || p.peek(1).Kind == token.Comma || p.peek(1).Kind == token.RParen {
					p.next()
					lit := n.Literal
					if lit == "" {
						lit = n.Kind.String()
					}
					name := &ast.Ident{Name: lit, S: n.Span}
					var ty ast.TypeNode
					if p.at(token.Colon) {
						p.next()
						ty = p.parseType()
					}
					params = append(params, &ast.FuncTypeParam{Name: name, Type: ty, IsVarArg: true, S: tySpan(name, ty)})
					_ = vaTy
				} else {
					ty := p.parseType()
					vaTy = ty
					params = append(params, &ast.FuncTypeParam{Type: ty, IsVarArg: true, S: ty.Span()})
				}
			} else {
				ty := p.parseType()
				vaTy = ty
				params = append(params, &ast.FuncTypeParam{Type: ty, IsVarArg: true, S: ty.Span()})
			}
			break
		}
		// name: T  |  name?: T  |  T (unnamed)
		if p.at(token.Ident) || isSoftKw(p.cur().Kind) {
			// Lookahead for Colon or Question+Colon => named.
			isNamed := false
			if p.peek(1).Kind == token.Colon || p.peek(1).Kind == token.Question {
				isNamed = true
			}
			if isNamed {
				n := p.next()
				name := &ast.Ident{Name: n.Literal, S: n.Span}
				optional := false
				if p.at(token.Question) {
					p.next()
					optional = true
				}
				if _, ok := p.expect(token.Colon, "':'"); !ok {
					return nil, false, nil, false
				}
				ty := p.parseType()
				params = append(params, &ast.FuncTypeParam{Name: name, Type: ty, Optional: optional, S: tySpan(name, ty)})
			} else {
				// Unnamed: parse a full type. But an Ident alone could be either a name without colon (invalid) or a type.
				// Treat as type.
				ty := p.parseType()
				params = append(params, &ast.FuncTypeParam{Type: ty, S: ty.Span()})
			}
		} else {
			ty := p.parseType()
			if ty == nil {
				return nil, false, nil, false
			}
			params = append(params, &ast.FuncTypeParam{Type: ty, S: ty.Span()})
		}
		if !p.at(token.Comma) {
			break
		}
		p.next()
	}
	return params, hasVA, vaTy, true
}

func tySpan(name *ast.Ident, ty ast.TypeNode) token.Span {
	if ty == nil {
		return name.Span()
	}
	return token.Span{Start: name.Span().Start, End: ty.Span().End, File: name.Span().File}
}

func (p *Parser) parseFuncTypeParams() ([]*ast.FuncTypeParam, bool, ast.TypeNode) {
	params, hasVA, vaTy, _ := p.tryParseFuncTypeParams()
	return params, hasVA, vaTy
}

func (p *Parser) parseTableType() ast.TypeNode {
	start := p.next() // {
	// Mapped type: `{ [K in Keys]?: V }` (optional readonly prefix).
	// Committed once `[Ident in` is seen; errors after that are real.
	if p.isMappedStart() {
		return p.parseMappedType(start)
	}
	var fields []*ast.TableTypeField
	for !p.at(token.RBrace) && !p.at(token.EOF) {
		if p.at(token.Comma) || p.at(token.Semi) {
			p.next()
			continue
		}
		f := p.parseTableTypeField()
		if f == nil {
			p.synchronizeTableType()
			continue
		}
		fields = append(fields, f)
		if p.at(token.Comma) || p.at(token.Semi) {
			p.next()
		}
	}
	p.expect(token.RBrace, "'}'")
	return &ast.TableType{Fields: fields, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

// isMappedStart peeks `[K in` (optional `+`/`-` + readonly prefix)
// without consuming.
func (p *Parser) isMappedStart() bool {
	i := 0
	if p.peek(i).Kind == token.Plus || p.peek(i).Kind == token.Minus {
		i++
	}
	if p.peek(i).Kind == token.ReadonlyKw {
		i++
	}
	return p.peek(i).Kind == token.LBrack &&
		p.peek(i+1).Kind == token.Ident &&
		p.peek(i+2).Kind == token.In
}

func (p *Parser) parseMappedType(start token.Token) ast.TypeNode {
	readonly, roMinus := false, false
	if p.at(token.Plus) || p.at(token.Minus) {
		neg := p.at(token.Minus)
		p.next()
		if _, ok := p.expect(token.ReadonlyKw, "'readonly'"); !ok {
			return &ast.NamedType{Name: "any", S: start.Span}
		}
		if neg {
			roMinus = true
		} else {
			readonly = true
		}
	} else if p.at(token.ReadonlyKw) {
		p.next()
		readonly = true
	}
	p.next() // [
	param := p.parseIdent()
	p.expect(token.In, "'in'")
	constraint := p.parseType()
	if _, ok := p.expect(token.RBrack, "']'"); !ok {
		return &ast.NamedType{Name: "any", S: start.Span}
	}
	optional, optMinus := false, false
	if p.at(token.Plus) || p.at(token.Minus) {
		neg := p.at(token.Minus)
		p.next()
		if _, ok := p.expect(token.Question, "'?'"); !ok {
			return &ast.NamedType{Name: "any", S: start.Span}
		}
		if neg {
			optMinus = true
		} else {
			optional = true
		}
	} else if p.at(token.Question) {
		// `?` here is the optional marker, never a conditional branch:
		// a `[K in T] ? ...` conditional would need `extends` first.
		p.next()
		optional = true
	}
	p.expect(token.Colon, "':'")
	value := p.parseType()
	p.expect(token.RBrace, "'}'")
	if param == nil || constraint == nil || value == nil {
		return &ast.NamedType{Name: "any", S: start.Span}
	}
	return &ast.MappedType{Param: param, Constraint: constraint, Optional: optional, OptMinus: optMinus, Readonly: readonly, RoMinus: roMinus, Value: value, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

func (p *Parser) parseTableTypeField() *ast.TableTypeField {
	start := p.cur()
	if p.at(token.ReadonlyKw) {
		p.next()
		name := p.parseIdentAllowSoft()
		if name == nil {
			return nil
		}
		optional := false
		if p.at(token.Question) {
			p.next()
			optional = true
		}
		p.expect(token.Colon, "':'")
		ty := p.parseType()
		return &ast.TableTypeField{Name: name.Name, NameSpan: name.Span(), Type: ty, Optional: optional, Readonly: true, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	// Indexer [K]: V
	if p.at(token.LBrack) {
		p.next()
		k := p.parseType()
		p.expect(token.RBrack, "']'")
		p.expect(token.Colon, "':'")
		v := p.parseType()
		return &ast.TableTypeField{IsIndexer: true, IndexKey: k, IndexVal: v, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	// Method shorthand or property.
	if (p.at(token.Ident) || isSoftKw(p.cur().Kind)) && (p.peek(1).Kind == token.LParen || p.peek(1).Kind == token.Lt) {
		name := p.next()
		lit := name.Literal
		if lit == "" {
			lit = name.Kind.String()
		}
		// Optional generics on methods: keep for explicit type arguments.
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
		p.next() // (
		params, hasVA, vaTy := p.parseFuncTypeParams()
		p.expect(token.RParen, "')'")
		var rets []ast.TypeNode
		if p.at(token.Colon) {
			p.next()
			rets = []ast.TypeNode{p.parseType()}
		} else if p.at(token.Arrow) {
			p.next()
			rets = returnTypeToList(p.parseReturnType())
		}
		ft := &ast.FuncType{Params: params, HasVarArg: hasVA, VarArgType: vaTy, Returns: rets, Generics: mgen, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
		return &ast.TableTypeField{Name: lit, NameSpan: name.Span, Type: ft, IsMethod: true, MethodSig: ft, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	name := p.parseIdentAllowSoft()
	if name == nil {
		return nil
	}
	optional := false
	if p.at(token.Question) {
		p.next()
		optional = true
	}
	if p.at(token.LParen) {
		p.next()
		params, hasVA, vaTy := p.parseFuncTypeParams()
		p.expect(token.RParen, "')'")
		var rets []ast.TypeNode
		if p.at(token.Colon) {
			p.next()
			rets = []ast.TypeNode{p.parseType()}
		} else if p.at(token.Arrow) {
			p.next()
			rets = returnTypeToList(p.parseReturnType())
		}
		ft := &ast.FuncType{Params: params, HasVarArg: hasVA, VarArgType: vaTy, Returns: rets, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
		return &ast.TableTypeField{Name: name.Name, NameSpan: name.Span(), Type: ft, Optional: optional, IsMethod: true, MethodSig: ft, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	}
	p.expect(token.Colon, "':'")
	ty := p.parseType()
	return &ast.TableTypeField{Name: name.Name, NameSpan: name.Span(), Type: ty, Optional: optional, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

// tryParseTypeArgs parses `<T, ...>` explicit generic arguments.
// Returns ok=false without consuming on failure (caller restores).
func (p *Parser) tryParseTypeArgs() ([]ast.TypeNode, bool) {
	if !p.at(token.Lt) {
		return nil, false
	}
	p.next() // <
	var out []ast.TypeNode
	if !p.atGt() && p.cur().Kind != token.EOF {
		for {
			t := p.parseType()
			if t == nil {
				return nil, false
			}
			out = append(out, t)
			if p.at(token.Comma) {
				p.next()
				continue
			}
			break
		}
	}
	if !p.consumeGt() {
		return nil, false
	}
	return out, true
}

func (p *Parser) parseGenericParams() []*ast.GenericParam {
	start := p.next() // <
	var out []*ast.GenericParam
	for !p.atGt() && !p.at(token.EOF) {
		n := p.parseIdent()
		if n == nil {
			break
		}
		var constraint ast.TypeNode
		var def ast.TypeNode
		if p.at(token.ExtendsKw) {
			p.next()
			constraint = p.parseType()
		}
		if p.at(token.Assign) {
			p.next()
			def = p.parseType()
		}
		out = append(out, &ast.GenericParam{Name: n, Constraint: constraint, Default: def, S: token.Span{Start: n.Span().Start, End: p.prevEnd(), File: p.file}})
		if p.at(token.Comma) {
			p.next()
			continue
		}
		break
	}
	p.consumeGt()
	_ = start
	return out
}
