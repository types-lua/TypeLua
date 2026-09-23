package checker

import (
	"strconv"
	"strings"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// Type-entry instantiation with memoization and recursive shells.

// maxTypeDepth caps generic instantiation nesting (see typeDepth).
const maxTypeDepth = 64

func (c *Checker) resolveTypeEntryWithArgs(e *typeEntry, args []types.Type, useSpan token.Span) types.Type {
	// Enum: no params.
	if e.enumDecl != nil {
		if e.enumType != nil {
			return e.enumType
		}
		if e.resolving {
			c.errorf(diag.TypeRecursionLimit, useSpan, "cyclic enum '%s'", e.name)
			return types.Any
		}
		e.resolving = true
		et := c.buildEnum(e.enumDecl)
		e.enumType = et
		e.resolving = false
		return et
	}
	// Classes: definition is built once (generic placeholders intact);
	// every reference substitutes arguments (defaults/unknown filled).
	if e.classDecl != nil {
		def := c.ensureClassDef(e)
		if def == nil {
			return types.Any
		}
		if len(e.params) == 0 {
			if len(args) > 0 {
				c.errorf(diag.TypeArgCount, useSpan, "type '%s' takes no type arguments, got %d", e.name, len(args))
			}
			return def.inst
		}
		sub := c.classSubFromTypes(e, args, useSpan)
		if inst, ok := types.Substitute(def.inst, sub).(*types.Class); ok {
			return inst
		}
		return def.inst
	}
	// Build substitution for generic params.
	sub := map[string]types.Type{}
	if len(e.params) > 0 {
		c.typeDepth++
		if c.typeDepth > maxTypeDepth {
			c.typeDepth--
			if !c.depthReported[e] {
				if c.depthReported == nil {
					c.depthReported = map[*typeEntry]bool{}
				}
				c.depthReported[e] = true
				c.errorf(diag.TypeRecursionLimit, useSpan, "type instantiation too deep (possible infinite expansion of '%s')", e.name)
			}
			return types.Any
		}
		defer func() { c.typeDepth-- }()
		if len(args) > len(e.params) {
			c.errorf(diag.TypeArgCount, useSpan, "type '%s' expects %d argument(s), got %d", e.name, len(e.params), len(args))
		}
		for i, p := range e.params {
			var a types.Type
			if i < len(args) {
				a = args[i]
			} else {
				// Default or unknown.
				if p.Default != nil {
					a = c.resolveType(p.Default)
				} else {
					a = types.Unknown
				}
			}
			// Check constraint.
			if p.Constraint != nil {
				con := c.resolveType(p.Constraint)
				if !types.IsAssignable(a, con, c.opt.toTypes()) {
					c.errorf(diag.TypeArgConstraint, useSpan, "type argument '%s' does not satisfy constraint '%s'", a.String(), con.String())
				}
			}
			sub[p.Name.Name] = a
		}
		// Memoize.
		key := typesKey(args)
		if cached, ok := e.cacheInst[key]; ok {
			return cached
		}
		if _, busy := e.inflight[key]; busy {
			// Recursive reference: share an in-progress shell, filled
			// when the outer build completes (mutual recursion included).
			sh := &types.Object{Name: e.name, Fields: map[string]types.Field{}}
			e.inflight[key] = sh
			return sh
		}
		if e.inflight == nil {
			e.inflight = map[string]*types.Object{}
		}
		e.inflight[key] = nil
		e.resolving = true
		var res types.Type
		if e.alias != nil {
			res = c.resolveTypeWithSub(e.alias.Type, sub)
		} else if e.iface != nil {
			res = c.buildInterface(e.iface, sub)
		} else {
			res = types.Any
		}
		e.resolving = false
		res = c.fillShell(e, key, res, useSpan)
		e.cacheInst[key] = res
		return res
	}
	// Non-generic: cache.
	if e.cached != nil {
		return e.cached
	}
	if _, busy := e.inflight[""]; busy {
		sh := &types.Object{Name: e.name, Fields: map[string]types.Field{}}
		e.inflight[""] = sh
		return sh
	}
	if e.inflight == nil {
		e.inflight = map[string]*types.Object{}
	}
	e.inflight[""] = nil
	e.resolving = true
	var res types.Type
	if e.alias != nil {
		res = c.resolveTypeWithSub(e.alias.Type, sub)
	} else if e.iface != nil {
		res = c.buildInterface(e.iface, sub)
	} else {
		res = types.Any
	}
	e.resolving = false
	res = c.fillShell(e, "", res, useSpan)
	e.cached = res
	return res
}

// fillShell completes a recursive build: if a cycle created a shell for key
// during the build, the finished object is copied into it (preserving the
// shared pointer) and the shell is returned. Direct self-aliases
// (`type A = A`) and non-object results keep the TL3106 error.
func (c *Checker) fillShell(e *typeEntry, key string, res types.Type, useSpan token.Span) types.Type {
	sh, ok := e.inflight[key]
	delete(e.inflight, key)
	if !ok || sh == nil {
		return res
	}
	if res == types.Type(sh) {
		c.errorf(diag.TypeRecursionLimit, useSpan, "cyclic type alias '%s'", e.name)
		return res
	}
	robj, ok := res.(*types.Object)
	if !ok {
		c.errorf(diag.TypeRecursionLimit, useSpan, "cyclic type alias '%s'", e.name)
		return res
	}
	for k, v := range robj.Fields {
		sh.Fields[k] = v
	}
	sh.Indexers = robj.Indexers
	return sh
}

func typesKey(args []types.Type) string {
	ps := make([]string, len(args))
	for i, a := range args {
		ps[i] = a.String()
	}
	return strings.Join(ps, "|")
}

func (c *Checker) buildInterface(n *ast.InterfaceDecl, sub map[string]types.Type) types.Type {
	obj := &types.Object{Name: n.Name.Name, Fields: map[string]types.Field{}}
	// Extends: merge base fields.
	for _, b := range n.Extends {
		bt := c.resolveTypeWithSub(b, sub)
		if bo, ok := bt.(*types.Object); ok {
			for k, v := range bo.Fields {
				obj.Fields[k] = v
			}
			obj.Indexers = append(obj.Indexers, bo.Indexers...)
		} else if isAnyLike(bt) {
			continue
		} else {
			c.errorf(diag.InterfaceExtendsNonObject, b.Span(), "interface can only extend object types, got '%s'", bt.String())
		}
	}
	for _, f := range n.Fields {
		var ft types.Type
		// Method shorthand fields store FuncType in Type already.
		ft = c.resolveTypeWithSub(f.Type, sub)
		if _, exists := obj.Fields[f.Name.Name]; exists {
			c.errorf(diag.DuplicateField, f.Span(), "duplicate field '%s'", f.Name.Name)
		}
		obj.Fields[f.Name.Name] = types.Field{Type: ft, Optional: f.Optional, Readonly: f.Readonly}
	}
	return obj
}

func (c *Checker) buildEnum(n *ast.EnumDecl) *types.Enum {
	members := map[string]types.Type{}
	var underlying types.Type
	autoNum := 0
	autoAllowed := true
	seenVals := map[string]string{} // value string -> member name
	for _, m := range n.Members {
		var mt types.Type
		if m.Value == nil {
			if !autoAllowed {
				c.errorf(diag.EnumMemberNeedsValue, m.Span(), "enum member '%s' must have an explicit value (mixed string enum)", m.Name.Name)
				mt = types.Any
			} else {
				mt = &types.Literal{Value: strconv.Itoa(autoNum), Base: types.Integer}
				autoNum++
			}
		} else {
			switch v := m.Value.(type) {
			case *ast.StringLit:
				mt = &types.Literal{Value: v.Literal, Base: types.String}
				autoAllowed = false
			case *ast.NumberLit:
				// Determine integer vs float.
				base := types.Type(types.Integer)
				if strings.ContainsAny(v.Literal, ".eEpP") {
					base = types.Float
				}
				mt = &types.Literal{Value: v.Literal, Base: base}
				// Update auto counter if integer.
				if iv, err := strconv.Atoi(strings.ReplaceAll(v.Literal, "_", "")); err == nil {
					autoNum = iv + 1
				} else {
					autoAllowed = false
				}
			case *ast.BoolLit:
				mt = &types.Literal{Value: strconv.FormatBool(v.Value), Base: types.Boolean}
				autoAllowed = false
			case *ast.UnOpExpr:
				// Negative number: -N
				if num, ok := v.X.(*ast.NumberLit); ok {
					base := types.Type(types.Integer)
					if strings.ContainsAny(num.Literal, ".eEpP") {
						base = types.Float
					}
					mt = &types.Literal{Value: "-" + num.Literal, Base: base}
				} else {
					c.errorf(diag.EnumMemberNotLiteral, m.Span(), "enum member '%s' must be a literal", m.Name.Name)
					mt = types.Any
				}
			default:
				c.errorf(diag.EnumMemberNotLiteral, m.Span(), "enum member '%s' must be a literal", m.Name.Name)
				mt = types.Any
			}
		}
		if prev, ok := seenVals[mt.String()]; ok {
			c.errorf(diag.DuplicateEnumValue, m.Span(), "duplicate enum value for '%s' (already used by '%s')", m.Name.Name, prev)
		} else {
			seenVals[mt.String()] = m.Name.Name
		}
		if _, ok := members[m.Name.Name]; ok {
			c.errorf(diag.DuplicateEnumMember, m.Span(), "duplicate enum member '%s'", m.Name.Name)
		}
		members[m.Name.Name] = mt
		if lit, ok := mt.(*types.Literal); ok {
			if underlying == nil {
				underlying = lit.Base
			} else if underlying.String() != lit.Base.String() {
				// Mixed: underlying becomes union.
				underlying = types.NewUnion([]types.Type{underlying, lit.Base})
			}
		}
	}
	if underlying == nil {
		underlying = types.Integer
	}
	return &types.Enum{Name: n.Name.Name, Underlying: underlying, Members: members}
}
