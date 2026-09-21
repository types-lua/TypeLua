package checker

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// Calls, method lookup, explicit type arguments, and inference.

// ---------- calls & generics ----------

func (c *Checker) lookupMethod(ot types.Type, name string, sp token.Span) types.Type {
	if f, ok := lookupField(ot, name); ok {
		return f.Type
	}
	if isAnyLike(ot) {
		return types.Any
	}
	c.errorf("TL3230", sp, "type '%s' has no method '%s'", ot.String(), name)
	return types.Any
}

// lookupMethodField is lookupMethod with visibility metadata for access checks.
func (c *Checker) lookupMethodField(ot types.Type, name string, sp token.Span) types.Field {
	if f, ok := lookupField(ot, name); ok {
		return f
	}
	if isAnyLike(ot) {
		return types.Field{Type: types.Any}
	}
	c.errorf("TL3230", sp, "type '%s' has no method '%s'", ot.String(), name)
	return types.Field{Type: types.Any}
}

// resolveTypeArgs resolves explicit generic argument nodes (`f<T>(x)`).
func (c *Checker) resolveTypeArgs(nodes []ast.TypeNode) []types.Type {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]types.Type, len(nodes))
	for i, n := range nodes {
		out[i] = c.resolveType(n)
	}
	return out
}

// bindExplicitArgs binds explicit type arguments positionally into sub and
// returns the function with those positions pre-substituted, so later
// inference only fills the remainder. Unfilled params use defaults.
func (c *Checker) bindExplicitArgs(fn *types.Func, explicit []types.Type, callSpan token.Span, sub map[string]types.Type) *types.Func {
	if len(explicit) == 0 {
		return fn
	}
	if len(fn.TypeParams) == 0 {
		c.errorf("TL3234", callSpan, "explicit type arguments given to non-generic function")
		return fn
	}
	if len(explicit) > len(fn.TypeParams) {
		c.errorf("TL3234", callSpan, "expected %d type argument(s), got %d", len(fn.TypeParams), len(explicit))
		explicit = explicit[:len(fn.TypeParams)]
	}
	exSub := map[string]types.Type{}
	for i, gp := range fn.TypeParams {
		if i >= len(explicit) {
			break
		}
		a := explicit[i]
		if gp.Constraint != nil && !types.IsAssignable(a, gp.Constraint, c.opt.toTypes()) {
			c.errorf("TL3234", callSpan, "type argument '%s' does not satisfy constraint '%s'", a.String(), gp.Constraint.String())
		}
		exSub[gp.Name] = a
		sub[gp.Name] = a
	}
	// Defaults for params with neither explicit nor (yet) inferred values.
	for _, gp := range fn.TypeParams {
		if _, ok := sub[gp.Name]; !ok && gp.Default != nil {
			sub[gp.Name] = types.Substitute(gp.Default, sub)
		}
	}
	if out, ok := types.Substitute(fn, exSub).(*types.Func); ok {
		return out
	}
	return fn
}

// checkCall checks a call and returns multi return types.
// explicit holds resolved explicit generic arguments (`f<T>(x)`);
// remaining parameters still infer from arguments.
func (c *Checker) checkCall(opSpan token.Span, fnT types.Type, args []ast.Expr, callSpan token.Span, explicit []types.Type) []types.Type {
	if isAnyLike(fnT) {
		for _, a := range args {
			c.checkExpr(a)
		}
		return []types.Type{types.Any}
	}
	// Calling the class itself instantiates it (`Point(...)` via __call).
	if cv, ok := fnT.(*types.ClassValue); ok {
		return c.checkClassInstantiate(cv, args, callSpan, explicit)
	}
	fn, ok := fnT.(*types.Func)
	if !ok {
		// Callable objects with __call? For v1, only functions callable.
		for _, a := range args {
			c.checkExpr(a)
		}
		c.errorf("TL3231", callSpan, "cannot call type '%s'", fnT.String())
		return []types.Type{types.Any}
	}
	// Generic inference: collect type params from fn? Our Func doesn't store generic names after substitution? For generic functions, funcTypeFromLit resolved params with tparams as GenericParam placeholders.
	// Infer: map param name -> inferred type from args.
	sub := map[string]types.Type{}
	// Explicit type arguments bind first (positional); the rest infer.
	// Missing ones fall back to defaults, like TS partial application.
	fn = c.bindExplicitArgs(fn, explicit, callSpan, sub)
	// Check args (expand? call args don't expand multi except last? In Lua, `f(g())` where g returns multi expands! Support: expand last arg if multi.
	argTypes := c.checkCallArgs(args)
	// Arity checks (Lua allows any count at runtime; statically enforce required).
	req := 0
	for _, p := range fn.Params {
		if !p.Optional {
			req++
		}
	}
	if len(argTypes) < req && !fn.HasVarArg {
		// If source requires more than supplied and missing can't be nil-optional, error.
		// Check if missing params are optional or nilable.
		missingOK := true
		for i := len(argTypes); i < len(fn.Params); i++ {
			if !fn.Params[i].Optional && !types.ContainsNil(fn.Params[i].Type) {
				missingOK = false
				break
			}
		}
		if !missingOK {
			c.errorf("TL3232", callSpan, "expected %d argument(s), got %d", req, len(argTypes))
		}
	}
	// Check each arg.
	for i, at := range argTypes {
		var pt types.Type
		if i < len(fn.Params) {
			pt = fn.Params[i].Type
		} else if fn.HasVarArg {
			if fn.VarArg != nil {
				pt = fn.VarArg
			} else {
				pt = types.Any
			}
		} else {
			// Extra args: allowed in Lua (dropped). No error to stay compatible? TS would error. For Lua compat, allow silently.
			continue
		}
		// Infer generics occurring in pt.
		inferInto(pt, at, sub)
		if !types.IsAssignable(at, applySub(pt, sub), c.opt.toTypes()) {
			// Retry with full sub after inference? For better messages, check after collecting all? We check incrementally; may false-positive when later args refine.
			// To avoid, defer errors to second pass below.
		}
	}
	// Second pass for errors with final sub.
	for i, at := range argTypes {
		var pt types.Type
		if i < len(fn.Params) {
			pt = applySub(fn.Params[i].Type, sub)
		} else if fn.HasVarArg {
			if fn.VarArg != nil {
				pt = applySub(fn.VarArg, sub)
			} else {
				continue
			}
		} else {
			continue
		}
		if !types.IsAssignable(at, pt, c.opt.toTypes()) {
			c.errorf("TL3233", args[i].Span(), "argument %d: type '%s' is not assignable to '%s'", i+1, at.String(), pt.String())
			break // one error per call to avoid cascades.
		}
	}
	// Substitute returns.
	var rets []types.Type
	for _, r := range fn.Returns {
		rets = append(rets, applySub(r, sub))
	}
	// Validate generic constraints with inferred args? Constraints checked at definition; inferred any satisfies? If inferred Unknown, keep.
	if len(rets) == 0 {
		return nil
	}
	return rets
}

func (c *Checker) checkCallArgs(args []ast.Expr) []types.Type {
	if len(args) == 0 {
		return nil
	}
	var out []types.Type
	for i, a := range args {
		if i == len(args)-1 {
			out = append(out, c.checkExprMulti(a)...)
		} else {
			out = append(out, c.checkExprSingle(a))
		}
	}
	return out
}

func inferInto(param, arg types.Type, sub map[string]types.Type) {
	switch p := param.(type) {
	case *types.GenericParam:
		if existing, ok := sub[p.Name]; ok {
			// Join: union? Keep first (simple). If differing, widen to union.
			if existing.String() != arg.String() {
				sub[p.Name] = types.NewUnion([]types.Type{existing, arg})
			}
			return
		}
		sub[p.Name] = arg
	case *types.InferParam:
		// Inference positions (e.g. conditional checks) bind like params.
		if existing, ok := sub[p.Name]; ok {
			if existing.String() != arg.String() {
				sub[p.Name] = types.NewUnion([]types.Type{existing, arg})
			}
			return
		}
		sub[p.Name] = arg
	case *types.Conditional:
		// Infer through the check pattern (`Elem<T>` from `[1, 2]`).
		inferInto(p.Check, arg, sub)
	case *types.Array:
		switch a := arg.(type) {
		case *types.Array:
			inferInto(p.Elem, a.Elem, sub)
		case *types.Tuple:
			// `{1,2,3}` infers as Tuple positionally; `T[]` accepts each element.
			for _, e := range a.Elems {
				inferInto(p.Elem, e, sub)
			}
		}
	case *types.Map:
		if a, ok := arg.(*types.Map); ok {
			inferInto(p.Key, a.Key, sub)
			inferInto(p.Value, a.Value, sub)
		}
	case *types.Tuple:
		if a, ok := arg.(*types.Tuple); ok && len(a.Elems) == len(p.Elems) {
			for i := range p.Elems {
				inferInto(p.Elems[i], a.Elems[i], sub)
			}
		}
	case *types.Func:
		// Skip deep inference for funcs (could unify returns/params; keep simple).
	case *types.Object:
		if a, ok := arg.(*types.Object); ok {
			for name, f := range p.Fields {
				if af, ok := a.Fields[name]; ok {
					inferInto(f.Type, af.Type, sub)
				}
			}
		}
	case *types.Union:
		// If arg matches one member, infer into that member? Try each.
		for _, m := range p.Types {
			if types.IsAssignable(arg, m, types.Options{StrictNilChecks: true}) {
				inferInto(m, arg, sub)
				break
			}
		}
	}
}

func applySub(t types.Type, sub map[string]types.Type) types.Type {
	if len(sub) == 0 {
		return t
	}
	return types.Substitute(t, sub)
}
