package checker

import (
	"strconv"
	"strings"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// Expression checking, table literals, and operators.

// ---------- expressions ----------

func (c *Checker) checkExprListMulti(exprs []ast.Expr) []types.Type {
	if len(exprs) == 0 {
		return nil
	}
	var out []types.Type
	for i, e := range exprs {
		if i == len(exprs)-1 {
			out = append(out, c.checkExprMulti(e)...)
		} else {
			out = append(out, c.checkExprSingle(e))
		}
	}
	return out
}

// checkExprSingle returns the first value of an expression.
func (c *Checker) checkExprSingle(e ast.Expr) types.Type {
	ms := c.checkExprMulti(e)
	if len(ms) == 0 {
		return types.Nil
	}
	return ms[0]
}

func (c *Checker) checkExpr(e ast.Expr) types.Type {
	return c.checkExprSingle(e)
}

// checkExprMulti expands multi-return calls on the last position.
func (c *Checker) checkExprMulti(e ast.Expr) []types.Type {
	switch n := e.(type) {
	case *ast.CallExpr:
		// `require("dotted.path")` resolves through the driver's module
		// types (any when unresolved or shadowed by a local).
		if id, ok := n.Func.(*ast.Ident); ok && id.Name == "require" && len(n.Args) == 1 {
			if lit, ok := n.Args[0].(*ast.StringLit); ok && !isShadowedRequire(c) {
				c.checkExpr(n.Func) // record the builtin use
				return []types.Type{c.requirePathType(lit)}
			}
		}
		// Mark unshadowed `error(...)` calls so the emitter can run
		// errdefers around them (normal checking continues).
		if id, ok := n.Func.(*ast.Ident); ok && id.Name == "error" {
			if s := c.cur().lookupVar("error"); s == nil || s.IsBuiltin {
				n.ErrorCall = true
			}
		}
		// `Point.new(...)` gets instantiation checks (abstract/private).
		if fe, ok := n.Func.(*ast.FieldExpr); ok && fe.Field.Name == "new" {
			if id, ok := fe.Obj.(*ast.Ident); ok {
				if s := c.cur().lookupVar(id.Name); s != nil {
					if cv, ok := s.Type.(*types.ClassValue); ok {
						c.recordUse(id.Span(), s)
						return c.checkClassInstantiate(cv, n.Args, n.Span(), c.resolveTypeArgs(n.TypeArgs))
					}
				}
			}
		}
		if id, ok := n.Func.(*ast.Ident); ok && id.Name == "super" && c.cur().lookupVar("super") == nil {
			return c.checkSuperCall(n)
		}
		return c.checkCall(n.Func.Span(), c.checkExpr(n.Func), n.Args, n.Span(), c.resolveTypeArgs(n.TypeArgs))
	case *ast.MethodCallExpr:
		if id, ok := n.Obj.(*ast.Ident); ok && id.Name == "super" && c.cur().lookupVar("super") == nil {
			return c.checkSuperMethod(n)
		}
		// `Point:new(...)` routes to instantiation checks.
		if id, ok := n.Obj.(*ast.Ident); ok && n.Method.Name == "new" {
			if s := c.cur().lookupVar(id.Name); s != nil {
				if cv, ok := s.Type.(*types.ClassValue); ok {
					c.recordUse(id.Span(), s)
					return c.checkClassInstantiate(cv, n.Args, n.Span(), c.resolveTypeArgs(n.TypeArgs))
				}
			}
		}
		ot := c.checkExpr(n.Obj)
		mf := c.lookupMethodField(ot, n.Method.Name, n.Span())
		if !c.checkAccess(mf.Access, mf.Owner, n.Span(), "method '"+n.Method.Name+"'") {
			return []types.Type{types.Any}
		}
		if _, ok := lookupField(ot, n.Method.Name); ok {
			c.recordMemberUse(n.Method.Span(), ot, n.Method.Name)
		}
		return c.checkCall(n.Span(), mf.Type, n.Args, n.Span(), c.resolveTypeArgs(n.TypeArgs))
	case *ast.VarArg:
		vt := c.currentVarArg()
		if vt == nil {
			c.errorf("TL3200", n.Span(), "'...' used outside vararg function")
			return []types.Type{types.Any}
		}
		// `...` expands to multiple values of vt. Represent as single for non-last? For multi, return [vt] (one-or-more modeled as one).
		// To keep sound, return single vt; callers in multi context treat as one. (Full variadic multi is unbounded; model as one.)
		return []types.Type{vt}
	default:
		return []types.Type{c.checkExprOne(e)}
	}
}

func (c *Checker) currentVarArg() types.Type {
	if len(c.varargStack) == 0 {
		return nil
	}
	return c.varargStack[len(c.varargStack)-1]
}

func (c *Checker) checkExprOne(e ast.Expr) types.Type {
	if e == nil {
		// Recovery artifact (parser already reported); never crash.
		return types.Any
	}
	switch n := e.(type) {
	case *ast.Ident:
		if s := c.cur().lookupVar(n.Name); s != nil {
			c.recordUse(n.Span(), s)
			return s.Type
		}
		if n.Name == "self" {
			c.errorf("TL3201", n.Span(), "'self' is only available inside methods")
			return types.Any
		}
		if n.Name == "super" {
			c.errorf("TL3301", n.Span(), "'super' can only be used inside methods of a derived class")
			return types.Any
		}
		c.errorf("TL3201", n.Span(), "undefined variable '%s'", n.Name)
		return types.Any
	case *ast.NumberLit:
		base := types.Type(types.Integer)
		if strings.ContainsAny(n.Literal, ".eEpP") {
			// Hex integers with p exponent are floats? Keep float.
			base = types.Float
		}
		return &types.Literal{Value: n.Literal, Base: base}
	case *ast.StringLit:
		return &types.Literal{Value: n.Literal, Base: types.String}
	case *ast.BoolLit:
		return &types.Literal{Value: strconv.FormatBool(n.Value), Base: types.Boolean}
	case *ast.NilLit:
		return types.Nil
	case *ast.VarArg:
		vt := c.currentVarArg()
		if vt == nil {
			c.errorf("TL3200", n.Span(), "'...' used outside vararg function")
			return types.Any
		}
		return vt
	case *ast.ParenExpr:
		return c.checkExpr(n.E)
	case *ast.TableLit:
		return c.checkTable(n)
	case *ast.FuncLit:
		ft := c.funcTypeFromLit(n, n.Generics)
		c.checkFuncBody(n, n.Generics, ft)
		return ft
	case *ast.IndexExpr:
		ot := c.checkExpr(n.Obj)
		kt := c.checkExpr(n.Key)
		if r := indexResult(ot, kt); r != nil {
			c.recordStringIndex(n.Key, ot)
			c.recordStructIndex(n.Key, n.Obj)
			return r
		}
		if isAnyLike(ot) {
			return types.Any
		}
		c.errorf("TL3202", n.Span(), "cannot index type '%s'", ot.String())
		return types.Any
	case *ast.FieldExpr:
		ot := c.checkExpr(n.Obj)
		if f, ok := lookupField(ot, n.Field.Name); ok {
			if !c.checkAccess(f.Access, f.Owner, n.Span(), "field '"+n.Field.Name+"'") {
				return types.Any
			}
			c.recordMemberUse(n.Field.Span(), ot, n.Field.Name)
			if root := structRoot(c, n.Obj); root != nil {
				c.recordStructUse(n.Field.Span(), root, n.Field.Name)
			}
			if f.Optional {
				return types.NewUnion([]types.Type{f.Type, types.Nil})
			}
			return f.Type
		}
		if isAnyLike(ot) {
			return types.Any
		}
		// Enum member access: Color.Red
		if en, ok := ot.(*types.Enum); ok {
			_ = en
		}
		// If obj is an enum value object (runtime table), fields were defined.
		c.errorf("TL3203", n.Span(), "type '%s' has no field '%s'", ot.String(), n.Field.Name)
		return types.Any
	case *ast.CallExpr:
		return c.checkExprSingle(n) // unreachable (handled in multi) but keep.
	case *ast.MethodCallExpr:
		return c.checkExprSingle(n)
	case *ast.BinOpExpr:
		return c.checkBinOp(n)
	case *ast.UnOpExpr:
		return c.checkUnOp(n)
	case *ast.CastExpr:
		srcT := c.checkExpr(n.E)
		dstT := c.resolveType(n.T)
		if !types.IsAssignable(srcT, dstT, c.opt.toTypes()) && !types.IsAssignable(dstT, srcT, c.opt.toTypes()) {
			c.errorf("TL3204", n.Span(), "conversion from '%s' to '%s' may be a mistake", srcT.String(), dstT.String())
		}
		return dstT
	case *ast.NonNullExpr:
		t := c.checkExpr(n.E)
		stripped, hadNil := types.RemoveNil(t)
		if !hadNil && !isAnyLike(t) {
			// Warn? `!` on non-nullable is unnecessary but not an error. Keep silent to avoid noise? Emit warning as error for now? Skip.
		}
		if hadNil {
			// stripped could be Never if it was just nil: `nil!` => never (likely bug).
			if stripped.Kind() == types.KNever {
				c.errorf("TL3205", n.Span(), "non-null assertion on 'nil'")
				return types.Never
			}
			return stripped
		}
		return t
	}
	c.errorf("TL3200", e.Span(), "unsupported expression %T", e)
	return types.Any
}

func (c *Checker) checkTable(n *ast.TableLit) types.Type {
	// Collect.
	var listElems []types.Type
	fields := map[string]types.Field{}
	var indexers []types.Indexer
	hasNamed := false
	for _, f := range n.Fields {
		switch f.Kind {
		case ast.ExprField:
			listElems = append(listElems, c.checkExpr(f.Val))
		case ast.NamedField:
			hasNamed = true
			fields[f.Key.Name] = types.Field{Type: c.checkExpr(f.Val)}
		case ast.IndexedField:
			// If key is string/number literal, treat as named.
			if lit := literalKey(f.KeyX); lit != nil {
				fields[*lit] = types.Field{Type: c.checkExpr(f.Val)}
				hasNamed = true
			} else {
				kt := c.checkExpr(f.KeyX)
				vt := c.checkExpr(f.Val)
				indexers = append(indexers, types.Indexer{Key: kt, Value: vt})
				hasNamed = true
			}
		}
	}
	if !hasNamed {
		if len(listElems) == 0 {
			return &types.Object{Fields: map[string]types.Field{}}
		}
		// Pure list: keep positional info as Tuple so `{"a", 1}` fits `[string, number]`.
		// Unannotated inference converts to Array (see checkLocalAssign).
		return &types.Tuple{Elems: listElems}
	}
	// Mixed or pure named: object with optional array indexer for list part.
	obj := &types.Object{Fields: fields, Indexers: indexers}
	if len(listElems) > 0 {
		elem := types.NewUnion(listElems)
		obj.Indexers = append(obj.Indexers, types.Indexer{Key: types.Integer, Value: elem})
	}
	return obj
}

func literalKey(e ast.Expr) *string {
	switch n := e.(type) {
	case *ast.StringLit:
		if s, _, _, ok := unquoteKey(n.Literal); ok {
			return &s
		}
		s, err := parseStringLiteral(n.Literal)
		if err == nil {
			return &s
		}
		// Fallback: raw.
		raw := strings.Trim(n.Literal, `"'`)
		return &raw
	case *ast.NumberLit:
		s := n.Literal
		return &s
	}
	return nil
}

// unquoteKey splits a string literal into content plus delimiter widths.
// Handles "...", '...', and long brackets [[...]], [=[...]=] (any level).
// Escapes stay raw (source-faithful, matching literalKey).
func unquoteKey(lit string) (string, int, int, bool) {
	if len(lit) >= 2 {
		q := lit[0]
		if (q == '"' || q == '\'') && lit[len(lit)-1] == q {
			return lit[1 : len(lit)-1], 1, 1, true
		}
		if q == '[' {
			i := 1
			for i < len(lit) && lit[i] == '=' {
				i++
			}
			if i < len(lit) && lit[i] == '[' {
				open := i + 1
				close := "]" + strings.Repeat("=", i-1) + "]"
				if strings.HasSuffix(lit, close) && len(lit) >= open+len(close) {
					return lit[open : len(lit)-len(close)], open, len(close), true
				}
			}
		}
	}
	return "", 0, 0, false
}

// stringKeyName splits a quoted `"name"` literal into its member name and
// inner span (quotes excluded, so rename edits preserve them).
// Long strings and non-literals report false.
func stringKeyName(sl *ast.StringLit) (string, token.Span, bool) {
	if sl == nil {
		return "", token.Span{}, false
	}
	name, open, close, ok := unquoteKey(sl.Literal)
	if !ok {
		return "", token.Span{}, false
	}
	inner := sl.Span()
	inner.Start.Offset += open
	inner.Start.Column += open
	inner.End.Offset -= close
	inner.End.Column -= close
	if inner.End.Offset <= inner.Start.Offset {
		return "", token.Span{}, false
	}
	return name, inner, true
}

func parseStringLiteral(lit string) (string, error) {
	if len(lit) >= 2 && (lit[0] == '"' || lit[0] == '\'') {
		// Use strconv-ish unquoting for simple cases; Lua escapes differ, so best-effort.
		inner := lit[1 : len(lit)-1]
		return inner, nil
	}
	if strings.HasPrefix(lit, "[") {
		// Long string: strip brackets.
		start := strings.Index(lit, "[")
		_ = start
		return lit, nil
	}
	return lit, nil
}

// recordTableKeys records named keys of a table literal checked against a
// nominal object type (interface implementations), recursing into nested
// literals with the member's declared type.
func (c *Checker) recordTableKeys(lit *ast.TableLit, want types.Type) {
	if c.Index == nil || lit == nil {
		return
	}
	obj, ok := want.(*types.Object)
	if !ok || obj.Name == "" {
		return
	}
	e := c.cur().lookupType(obj.Name)
	if e == nil {
		return
	}
	for _, f := range lit.Fields {
		var name string
		var sp token.Span
		var val ast.Expr
		switch f.Kind {
		case ast.NamedField:
			if f.Key == nil {
				continue
			}
			name, sp, val = f.Key.Name, f.Key.Span(), f.Val
		case ast.IndexedField:
			sl, ok := f.KeyX.(*ast.StringLit)
			if !ok {
				continue
			}
			nm, inner, ok := stringKeyName(sl)
			if !ok {
				continue
			}
			name, sp, val = nm, inner, f.Val
		default:
			continue
		}
		mf, ok := lookupNamedField(obj, name)
		if !ok {
			continue
		}
		c.Index.Uses = append(c.Index.Uses, SymUse{Span: sp,
			Member: &MemberRef{Owner: e, OwnerName: e.name, Name: name}})
		if sub, ok := val.(*ast.TableLit); ok {
			c.recordTableKeys(sub, mf.Type)
		}
	}
}

func (c *Checker) checkBinOp(n *ast.BinOpExpr) types.Type {
	l := c.checkExpr(n.L)
	r := c.checkExpr(n.R)
	anyLike := isAnyLike(l) || isAnyLike(r)
	switch n.Op {
	case token.Plus, token.Minus, token.Star, token.Slash, token.DoubleSlash, token.Percent, token.Caret:
		if !anyLike {
			if !isNumberLike(l) || !isNumberLike(r) {
				c.errorf("TL3210", n.Span(), "arithmetic on non-numbers ('%s', '%s')", l.String(), r.String())
			}
		}
		// Integer preservation: if both integer and op is not / ^, result integer.
		if isIntegerLike(l) && isIntegerLike(r) && n.Op != token.Slash && n.Op != token.Caret {
			return types.Integer
		}
		return types.Number
	case token.Amp, token.Pipe, token.Tilde, token.ShiftL, token.ShiftR:
		if !anyLike && (!isIntegerLike(l) || !isIntegerLike(r)) {
			c.errorf("TL3211", n.Span(), "bitwise operation requires integers ('%s', '%s')", l.String(), r.String())
		}
		return types.Integer
	case token.DotDot:
		if !anyLike && (!isConcatable(l) || !isConcatable(r)) {
			c.errorf("TL3212", n.Span(), "concatenation requires strings/numbers ('%s', '%s')", l.String(), r.String())
		}
		return types.String
	case token.Eq, token.Ne:
		return types.Boolean
	case token.Lt, token.Le, token.Gt, token.Ge:
		if !anyLike && !isComparable(l, r) {
			c.errorf("TL3213", n.Span(), "comparison of incompatible types ('%s', '%s')", l.String(), r.String())
		}
		return types.Boolean
	case token.And, token.Or:
		// `and`/`or`: result is union (simplified).
		if n.Op == token.Or {
			// a or b: if a is falsy, b. Type = NonFalsy(a) | b. Simplify to union minus false/nil? Keep union.
			return types.NewUnion([]types.Type{l, r})
		}
		return types.NewUnion([]types.Type{l, r})
	}
	c.errorf("TL3214", n.Span(), "unsupported operator '%s'", n.Op)
	return types.Any
}

func (c *Checker) checkUnOp(n *ast.UnOpExpr) types.Type {
	x := c.checkExpr(n.X)
	switch n.Op {
	case token.Not:
		return types.Boolean
	case token.Hash:
		if !isAnyLike(x) && !isLengthable(x) {
			c.errorf("TL3220", n.Span(), "length operator on '%s'", x.String())
		}
		return types.Integer
	case token.Minus:
		if !isAnyLike(x) && !isNumberLike(x) {
			c.errorf("TL3221", n.Span(), "unary minus on '%s'", x.String())
		}
		if isIntegerLike(x) {
			return types.Integer
		}
		return types.Number
	case token.Tilde:
		if !isAnyLike(x) && !isIntegerLike(x) {
			c.errorf("TL3222", n.Span(), "bitwise not on '%s'", x.String())
		}
		return types.Integer
	}
	return types.Any
}
