package checker

import (
	"strings"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// Flow narrowing for variables and member paths.

// ---------- narrowing ----------

// narrowKind selects the type-level refinement of one narrowOp.
type narrowKind int

const (
	narrowNil narrowKind = iota
	narrowTypeof
	narrowTruthy
)

// narrowOp is one root refinement. Ops apply sequentially against the
// current (possibly already shadowed) binding, so `and`-chained refinements
// of the same root compose instead of clobbering each other.
type narrowOp struct {
	root   string
	fields []string
	kind   narrowKind
	isNil  bool   // narrowNil: narrow the member to nil
	lit    string // narrowTypeof: `type()` literal
	toType bool   // narrowTypeof: narrow towards (vs away from) lit
	truthy bool   // narrowTruthy: Lua truthiness branch
}

// applyNarrowing installs shadowed (narrowed) variable types in the current scope for a branch.
func (c *Checker) applyNarrowing(cond ast.Expr, truthy bool) {
	for _, op := range c.narrowCond(cond, truthy) {
		sym := c.cur().lookupVar(op.root)
		if sym == nil {
			continue
		}
		var newT types.Type
		var ok bool
		switch op.kind {
		case narrowNil:
			newT, ok = refineRootType(sym.Type, op.fields, func(cur types.Type) (types.Type, bool) {
				return narrowNilOfType(cur, op.isNil)
			})
		case narrowTypeof:
			newT, ok = refineRootType(sym.Type, op.fields, func(cur types.Type) (types.Type, bool) {
				return narrowTypeofOfType(cur, op.lit, op.toType)
			})
		case narrowTruthy:
			newT, ok = refineRootType(sym.Type, op.fields, func(cur types.Type) (types.Type, bool) {
				return narrowTruthyOfType(cur, op.truthy)
			})
		}
		if !ok {
			continue
		}
		// Shadow in current scope.
		c.cur().vars[op.root] = &Symbol{Name: op.root, Kind: sym.Kind, Type: newT, Span: sym.Span, IsConst: sym.IsConst, IsBuiltin: sym.IsBuiltin, SameAs: symRoot(sym)}
	}
}

func (c *Checker) narrowCond(cond ast.Expr, truthy bool) []narrowOp {
	// Strip parens and `not`.
	if p, ok := cond.(*ast.ParenExpr); ok {
		return c.narrowCond(p.E, truthy)
	}
	if u, ok := cond.(*ast.UnOpExpr); ok && u.Op == token.Not {
		return c.narrowCond(u.X, !truthy)
	}
	if b, ok := cond.(*ast.BinOpExpr); ok {
		// `x == nil` / `x ~= nil`
		if b.Op == token.Eq || b.Op == token.Ne {
			isEq := b.Op == token.Eq
			wantTruthy := isEq == truthy // e.g. `x == nil` true => x is nil.
			if id, ok := asIdent(b.L); ok && isNilLit(b.R) {
				return []narrowOp{{root: id, kind: narrowNil, isNil: wantTruthy}}
			}
			if id, ok := asIdent(b.R); ok && isNilLit(b.L) {
				return []narrowOp{{root: id, kind: narrowNil, isNil: wantTruthy}}
			}
			// Member paths (`node.next == nil`): refine the root.
			if root, fields, ok := narrowPath(b.L); ok && len(fields) > 0 && isNilLit(b.R) {
				return []narrowOp{{root: root, fields: fields, kind: narrowNil, isNil: wantTruthy}}
			}
			if root, fields, ok := narrowPath(b.R); ok && len(fields) > 0 && isNilLit(b.L) {
				return []narrowOp{{root: root, fields: fields, kind: narrowNil, isNil: wantTruthy}}
			}
			// `type(x) == "s"` / `~=`.
			if tn, lit, ok := asTypeofCompare(b); ok {
				isEqualOp := b.Op == token.Eq
				narrowTo := (isEqualOp == truthy)
				return []narrowOp{{root: tn, kind: narrowTypeof, lit: lit, toType: narrowTo}}
			}
			if root, fields, lit, ok := asTypeofPathCompare(b); ok {
				isEqualOp := b.Op == token.Eq
				narrowTo := (isEqualOp == truthy)
				return []narrowOp{{root: root, fields: fields, kind: narrowTypeof, lit: lit, toType: narrowTo}}
			}
		}
		// `a and b`: in true branch both hold; in false unknown.
		if b.Op == token.And && truthy {
			return append(c.narrowCond(b.L, true), c.narrowCond(b.R, true)...)
		}
		if b.Op == token.Or && !truthy {
			return append(c.narrowCond(b.L, false), c.narrowCond(b.R, false)...)
		}
	}
	// Truthiness: `if x then` / `if node.next then` (only nil/false are falsy).
	if root, fields, ok := narrowPath(cond); ok {
		return []narrowOp{{root: root, fields: fields, kind: narrowTruthy, truthy: truthy}}
	}
	return nil
}

// narrowPath decomposes a narrowable lvalue (`x`, `x.y`, `x["y"]`) into
// its root variable and field chain. Anything else reports ok=false.
func narrowPath(e ast.Expr) (root string, fields []string, ok bool) {
	switch n := e.(type) {
	case *ast.Ident:
		return n.Name, nil, true
	case *ast.ParenExpr:
		return narrowPath(n.E)
	case *ast.FieldExpr:
		r, fs, ok := narrowPath(n.Obj)
		if !ok {
			return "", nil, false
		}
		return r, append(fs, n.Field.Name), true
	case *ast.IndexExpr:
		sl, isStr := n.Key.(*ast.StringLit)
		if !isStr {
			return "", nil, false
		}
		s, _ := parseStringLiteral(sl.Literal)
		s = strings.Trim(s, `"'`)
		r, fs, ok := narrowPath(n.Obj)
		if !ok {
			return "", nil, false
		}
		return r, append(fs, s), true
	}
	return "", nil, false
}

// refineRootType rebuilds t with the member at fields narrowed by refine
// (which maps the effective member type to its narrowed form, as field
// access would observe it). Only plain objects and class instances are
// rebuilt; maps, enums, primitives, or a missing field report ok=false
// and the condition simply does not narrow.
func refineRootType(t types.Type, fields []string, refine func(types.Type) (types.Type, bool)) (types.Type, bool) {
	if len(fields) == 0 {
		return refine(t)
	}
	switch x := t.(type) {
	case *types.Union:
		var rs []types.Type
		changed := false
		for _, m := range x.Types {
			r, ok := refineRootType(m, fields, refine)
			if !ok {
				rs = append(rs, m)
				continue
			}
			changed = true
			rs = append(rs, r)
		}
		if !changed {
			return t, false
		}
		return types.NewUnion(rs), true
	case *types.Object:
		f, ok := x.Fields[fields[0]]
		if !ok {
			return t, false
		}
		ft, opt, ok := refineMemberField(f.Type, f.Optional, fields[1:], refine)
		if !ok {
			return t, false
		}
		cp := *x
		cp.Fields = make(map[string]types.Field, len(x.Fields))
		for k, v := range x.Fields {
			cp.Fields[k] = v
		}
		nf := f
		nf.Type, nf.Optional = ft, opt
		cp.Fields[fields[0]] = nf
		return &cp, true
	case *types.Class:
		cf, ok := x.Fields[fields[0]]
		if !ok {
			return t, false
		}
		ft, opt, ok := refineMemberField(cf.Type, cf.Optional, fields[1:], refine)
		if !ok {
			return t, false
		}
		cp := *x
		cp.Fields = make(map[string]types.ClassField, len(x.Fields))
		for k, v := range x.Fields {
			cp.Fields[k] = v
		}
		ncf := cf
		ncf.Type, ncf.Optional = ft, opt
		cp.Fields[fields[0]] = ncf
		return &cp, true
	}
	return t, false
}

// refineMemberField narrows one field's effective access type (adding `nil`
// for optional fields, as field access observes), folding the result back
// into (type, optional) storage form.
func refineMemberField(ft types.Type, optional bool, rest []string, refine func(types.Type) (types.Type, bool)) (types.Type, bool, bool) {
	eff := ft
	if optional {
		eff = types.NewUnion([]types.Type{ft, types.Nil})
	}
	sub, ok := refineRootType(eff, rest, refine)
	if !ok {
		return nil, false, false
	}
	if !optional {
		return sub, false, true
	}
	stripped, hadNil := types.RemoveNil(sub)
	if !hadNil {
		return sub, false, true
	}
	return stripped, true, true
}

func asIdent(e ast.Expr) (string, bool) {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name, true
	}
	return "", false
}

func isNilLit(e ast.Expr) bool {
	_, ok := e.(*ast.NilLit)
	return ok
}

// matchTypeofCall matches `type(arg) == "lit"` shapes, returning the
// inspected argument expression and the literal text.
func matchTypeofCall(call, lit ast.Expr) (ast.Expr, string, bool) {
	ce, ok := call.(*ast.CallExpr)
	if !ok {
		return nil, "", false
	}
	fn, ok := ce.Func.(*ast.Ident)
	if !ok || fn.Name != "type" || len(ce.Args) != 1 {
		return nil, "", false
	}
	sl, ok := lit.(*ast.StringLit)
	if !ok {
		return nil, "", false
	}
	s, _ := parseStringLiteral(sl.Literal)
	// Strip quotes if our fallback left them.
	s = strings.Trim(s, `"'`)
	// Long-string edge: ignore.
	return ce.Args[0], s, true
}

// asTypeofCompare matches `type(x) == "lit"` forms, returning (varname, literal, true).
func asTypeofCompare(b *ast.BinOpExpr) (string, string, bool) {
	if arg, s, ok := matchTypeofCall(b.L, b.R); ok {
		if name, isId := asIdent(arg); isId {
			return name, s, true
		}
	}
	if arg, s, ok := matchTypeofCall(b.R, b.L); ok {
		if name, isId := asIdent(arg); isId {
			return name, s, true
		}
	}
	return "", "", false
}

// asTypeofPathCompare matches `type(path.to.member) == "lit"` forms.
func asTypeofPathCompare(b *ast.BinOpExpr) (string, []string, string, bool) {
	if arg, s, ok := matchTypeofCall(b.L, b.R); ok {
		if root, fields, isPath := narrowPath(arg); isPath && len(fields) > 0 {
			return root, fields, s, true
		}
	}
	if arg, s, ok := matchTypeofCall(b.R, b.L); ok {
		if root, fields, isPath := narrowPath(arg); isPath && len(fields) > 0 {
			return root, fields, s, true
		}
	}
	return "", nil, "", false
}

// narrowNilOfType narrows a type by nil-ness (shared by variable and
// member narrowing).
func narrowNilOfType(cur types.Type, isNil bool) (types.Type, bool) {
	if isNil {
		return types.Nil, true
	}
	stripped, hadNil := types.RemoveNil(cur)
	if !hadNil {
		return nil, false
	}
	if stripped.Kind() == types.KNever {
		stripped = types.Never
	}
	return stripped, true
}

// narrowTypeofOfType narrows a type by a `type(x)` literal comparison
// (shared by variable and member narrowing).
func narrowTypeofOfType(cur types.Type, lit string, toType bool) (types.Type, bool) {
	prim := typeofLiteralToType(lit)
	if prim == nil {
		return nil, false
	}
	if toType {
		// Intersect? If orig is union containing prim, narrow to prim (or member).
		// If orig is any, narrow to prim.
		if isAnyLike(cur) {
			return prim, true
		}
		// If prim assignable to orig, narrow to prim; else if orig assignable to prim? Keep prim? Else unreachable (Never) — still narrow to prim to surface downstream? Use prim.
		return prim, true
	}
	// Exclude: remove prim from union.
	if u, ok := cur.(*types.Union); ok {
		var rest []types.Type
		for _, m := range u.Types {
			if m.String() == prim.String() {
				continue
			}
			// Literal members of that base? e.g. orig `"a" | "b"`, prim string => all removed => Never. Keep logic: if prim is base and member is literal of base, remove.
			if litM, ok := m.(*types.Literal); ok && litM.Base.String() == prim.String() {
				continue
			}
			rest = append(rest, m)
		}
		if len(rest) != len(u.Types) {
			return types.NewUnion(rest), true
		}
		return nil, false
	}
	if cur.String() == prim.String() {
		// `type(x) ~= "string"` but x: string => unreachable else? Narrow to Never.
		return types.Never, true
	}
	return nil, false
}

// narrowTruthyOfType narrows a type by Lua truthiness (only nil and false
// are falsy). Reports false when nothing changes.
func narrowTruthyOfType(cur types.Type, truthy bool) (types.Type, bool) {
	if isAnyLike(cur) {
		return nil, false
	}
	var ms []types.Type
	if u, ok := cur.(*types.Union); ok {
		ms = u.Types
	} else {
		ms = []types.Type{cur}
	}
	var keep []types.Type
	for _, m := range ms {
		switch {
		case m.Kind() == types.KNil:
			if !truthy {
				keep = append(keep, m)
			}
		case isFalseLit(m):
			if !truthy {
				keep = append(keep, m)
			}
		case isTrueLit(m):
			if truthy {
				keep = append(keep, m)
			}
		case m.Kind() == types.KBoolean:
			if truthy {
				keep = append(keep, &types.Literal{Value: "true", Base: types.Boolean})
			} else {
				keep = append(keep, &types.Literal{Value: "false", Base: types.Boolean})
			}
		default:
			if truthy {
				keep = append(keep, m)
			}
		}
	}
	if len(keep) == len(ms) {
		return nil, false
	}
	return types.NewUnion(keep), true
}

func isFalseLit(t types.Type) bool {
	l, ok := t.(*types.Literal)
	return ok && l.Base == types.Boolean && l.Value == "false"
}

func isTrueLit(t types.Type) bool {
	l, ok := t.(*types.Literal)
	return ok && l.Base == types.Boolean && l.Value == "true"
}

func typeofLiteralToType(s string) types.Type {
	switch s {
	case "nil":
		return types.Nil
	case "boolean":
		return types.Boolean
	case "number":
		return types.Number
	case "string":
		return types.String
	case "table":
		return types.Table
	case "function":
		return types.Function
	case "thread":
		return types.Thread
	case "userdata":
		return types.Userdata
	}
	return nil
}
