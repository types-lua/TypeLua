package shake

// Conservative tree-shaking for TypeLua: drop zero-reference pure local
// functions (`local function f() ... end`, single `local x = function...
// end`). Everything else is always kept.
//
// Safety contract (bugs here become broken programs, so the bias is to
// KEEP):
//   - Only pure closure definitions are candidates. Calls, table/method
//     assignments, globals, `require`/`import`, `return`s, multi-value or
//     attributed (`<const>`/`<close>`) locals, classes, enums, macros and
//     type declarations are never touched.
//   - Name resolution is scope-aware (shadowing, params, for-vars, method
//     `self`, select `case local` bindings). Labels, field/method names
//     and type annotations live in other namespaces and never count.
//   - Assignment targets and non-`local` select bindings count as uses:
//     a write-only local is kept (a missed optimization, never a bug).
//   - Transitive fixpoint: a candidate used only by dead candidates dies
//     with them; anything reachable from live code survives.
//   - Fail-safe default: any unknown Stmt/Expr node taints the run and
//     NOTHING is removed, so future language additions degrade to today's
//     behavior instead of mis-shaking.
//
// ShakeFile mutates f in place (drops dead definition statements from
// every block) and returns the removal count.

import (
	"github.com/typelua/typelua/internal/ast"
)

const liveCtx = -1

type candInfo struct {
	id   int
	stmt ast.Stmt // definition statement to drop when dead
}

type binding struct {
	cand int // candidate id, or liveCtx when not a candidate
}

type shaker struct {
	scopes  []map[string]*binding
	cands   []*candInfo
	roots   map[int]bool
	edges   map[int]map[int]bool // enclosing candidate -> referenced candidate
	cur     int                  // innermost enclosing candidate, or liveCtx
	tainted bool
}

// ShakeFile drops dead pure-local-function definitions from f.
// It never fails: on any unrecognized shape it removes nothing.
func ShakeFile(f *ast.File) int {
	if f == nil || f.Block == nil {
		return 0
	}
	s := &shaker{cur: liveCtx, roots: map[int]bool{}, edges: map[int]map[int]bool{}}
	s.push()
	s.block(f.Block)
	s.pop()
	if s.tainted {
		return 0
	}
	live := map[int]bool{}
	var flood func(id int)
	flood = func(id int) {
		if live[id] {
			return
		}
		live[id] = true
		for to := range s.edges[id] {
			flood(to)
		}
	}
	for id := range s.roots {
		flood(id)
	}
	dead := map[ast.Stmt]bool{}
	for _, c := range s.cands {
		if !live[c.id] {
			dead[c.stmt] = true
		}
	}
	if len(dead) == 0 {
		return 0
	}
	n := 0
	var filter func(b *ast.Block)
	filter = func(b *ast.Block) {
		if b == nil {
			return
		}
		kept := b.Stmts[:0]
		for _, st := range b.Stmts {
			if st != nil && dead[st] {
				n++
				continue
			}
			kept = append(kept, st)
			filterStmt(st, filter)
		}
		b.Stmts = kept
		if b.Ret != nil {
			// Return expressions are uses, never definitions: nothing to drop.
		}
	}
	filter(f.Block)
	return n
}

// filterStmt recurses into child blocks of st so drops apply at any depth.
// Unknown statements are left intact (removal is pointer-driven, so this
// needs no taint: it can only drop previously proven-dead definitions).
func filterStmt(st ast.Stmt, filter func(b *ast.Block)) {
	switch n := st.(type) {
	case *ast.DoBlock:
		filter(n.Block)
	case *ast.IfStmt:
		filter(n.Then)
		for _, e := range n.Elseifs {
			if e != nil {
				filter(e.Block)
			}
		}
		filter(n.Else)
	case *ast.WhileStmt:
		filter(n.Block)
	case *ast.RepeatStmt:
		filter(n.Block)
	case *ast.NumericFor:
		filter(n.Block)
	case *ast.GenericFor:
		filter(n.Block)
	case *ast.SelectStmt:
		for _, c := range n.Cases {
			if c != nil {
				filter(c.Block)
			}
		}
		filter(n.Default)
	case *ast.FuncDefStmt:
		if n.Func != nil {
			filter(n.Func.Body)
		}
	case *ast.LocalAssign:
		for _, v := range n.Values {
			filterExpr(v, filter)
		}
	case *ast.LocalFuncDef:
		if n.Func != nil {
			filter(n.Func.Body)
		}
	case *ast.ClassDecl:
		for _, m := range n.Members {
			if m != nil {
				filter(m.Body)
			}
		}
	case *ast.MacroDef:
		if n.IsExpr {
			filterExpr(n.Body, filter)
		} else {
			filter(n.Block)
		}
	case *ast.EnumDecl:
		for _, m := range n.Members {
			if m != nil {
				filterExpr(m.Value, filter)
			}
		}
	}
}

// filterExpr recurses into closures nested in expressions.
func filterExpr(x ast.Expr, filter func(b *ast.Block)) {
	switch n := x.(type) {
	case *ast.FuncLit:
		filter(n.Body)
	case *ast.TableLit:
		for _, f := range n.Fields {
			if f == nil {
				continue
			}
			filterExpr(f.KeyX, filter)
			filterExpr(f.Val, filter)
		}
	case *ast.ParenExpr:
		filterExpr(n.E, filter)
	case *ast.IndexExpr:
		filterExpr(n.Obj, filter)
		filterExpr(n.Key, filter)
	case *ast.FieldExpr:
		filterExpr(n.Obj, filter)
	case *ast.CallExpr:
		filterExpr(n.Func, filter)
		for _, a := range n.Args {
			filterExpr(a, filter)
		}
	case *ast.MethodCallExpr:
		filterExpr(n.Obj, filter)
		for _, a := range n.Args {
			filterExpr(a, filter)
		}
	case *ast.BinOpExpr:
		filterExpr(n.L, filter)
		filterExpr(n.R, filter)
	case *ast.UnOpExpr:
		filterExpr(n.X, filter)
	case *ast.CastExpr:
		filterExpr(n.E, filter)
	case *ast.NonNullExpr:
		filterExpr(n.E, filter)
	case *ast.AwaitExpr:
		filterExpr(n.X, filter)
	}
}

func (s *shaker) push() { s.scopes = append(s.scopes, map[string]*binding{}) }
func (s *shaker) pop()  { s.scopes = s.scopes[:len(s.scopes)-1] }

func (s *shaker) bind(name string, cand int) {
	if name == "" {
		return
	}
	s.scopes[len(s.scopes)-1][name] = &binding{cand: cand}
}

// use records a value-position reference to name: a root edge when outside
// every candidate, otherwise an edge from the enclosing candidate.
func (s *shaker) use(id *ast.Ident) {
	if id == nil {
		return
	}
	for i := len(s.scopes) - 1; i >= 0; i-- {
		if b, ok := s.scopes[i][id.Name]; ok {
			if b.cand == liveCtx {
				return // non-candidate binding: nothing to keep
			}
			if s.cur == liveCtx {
				s.roots[b.cand] = true
			} else {
				m := s.edges[s.cur]
				if m == nil {
					m = map[int]bool{}
					s.edges[s.cur] = m
				}
				m[b.cand] = true
			}
			return
		}
	}
	// Unresolved: global/builtin/field — never a candidate.
}

// newCand registers a removal candidate for a pure closure definition.
func (s *shaker) newCand(stmt ast.Stmt) int {
	id := len(s.cands)
	s.cands = append(s.cands, &candInfo{id: id, stmt: stmt})
	return id
}

// isPureFuncLit reports a side-effect-free closure value.
func isPureFuncLit(x ast.Expr) bool {
	_, ok := x.(*ast.FuncLit)
	return ok
}

func (s *shaker) block(b *ast.Block) {
	if b == nil || s.tainted {
		return
	}
	for _, st := range b.Stmts {
		s.stmt(st)
		if s.tainted {
			return
		}
	}
	if b.Ret != nil {
		for _, e := range b.Ret.Exprs {
			s.expr(e)
		}
	}
}

func (s *shaker) stmt(st ast.Stmt) {
	if st == nil || s.tainted {
		return
	}
	switch n := st.(type) {
	case *ast.EmptyStmt, *ast.BreakStmt:
	case *ast.ExprStmt:
		s.expr(n.E)
	case *ast.AssignStmt:
		// Conservative: targets count as uses (write-only locals are kept).
		for _, t := range n.Targets {
			s.expr(t)
		}
		for _, v := range n.Values {
			s.expr(v)
		}
	case *ast.LocalAssign:
		// Attrs parallels Names with "" for none (see parser.parseLocal).
		if len(n.Names) == 1 && len(n.Values) == 1 && n.Names[0] != nil &&
			isPureFuncLit(n.Values[0]) && !(len(n.Attrs) > 0 && n.Attrs[0] != "") {
			id := s.newCand(st)
			// `local x = function`: walk the value exactly once, with the
			// candidate as enclosing context (x itself is NOT visible
			// inside, per Lua semantics: bind only afterwards).
			saved := s.cur
			s.cur = id
			s.expr(n.Values[0])
			s.cur = saved
			s.bind(n.Names[0].Name, id)
		} else {
			for _, v := range n.Values {
				s.expr(v)
			}
			for _, name := range n.Names {
				if name != nil {
					s.bind(name.Name, liveCtx)
				}
			}
		}
	case *ast.LocalFuncDef:
		if n.Exported || n.Func == nil {
			// Exported: external contract. Body-less: degenerate recovery
			// shape; bind plainly and move on (never a candidate).
			if n.Name != nil {
				s.bind(n.Name.Name, liveCtx)
			}
			return
		}
		id := s.newCand(st)
		// `local function f`: f IS visible inside its own body (recursive).
		if n.Name != nil {
			s.bind(n.Name.Name, id)
		}
		saved := s.cur
		s.cur = id
		s.funcLit(n.Func)
		s.cur = saved
	case *ast.FuncDefStmt:
		// Global/method definition: always kept; prefix tables are uses.
		if n.Name != nil {
			for _, p := range n.Name.Prefix {
				s.use(p)
			}
		}
		if n.Func != nil {
			s.funcLit(n.Func)
		}
	case *ast.DoBlock:
		s.push()
		s.block(n.Block)
		s.pop()
	case *ast.IfStmt:
		s.expr(n.Cond)
		s.push()
		s.block(n.Then)
		s.pop()
		for _, e := range n.Elseifs {
			if e == nil {
				continue
			}
			s.expr(e.Cond)
			s.push()
			s.block(e.Block)
			s.pop()
		}
		s.push()
		s.block(n.Else)
		s.pop()
	case *ast.WhileStmt:
		s.expr(n.Cond)
		s.push()
		s.block(n.Block)
		s.pop()
	case *ast.RepeatStmt:
		// `until` sees body locals: one shared scope.
		s.push()
		s.block(n.Block)
		s.expr(n.Cond)
		s.pop()
	case *ast.NumericFor:
		s.expr(n.Start)
		s.expr(n.End)
		if n.Step != nil {
			s.expr(n.Step)
		}
		s.push()
		if n.Var != nil {
			s.bind(n.Var.Name, liveCtx)
		}
		s.block(n.Block)
		s.pop()
	case *ast.GenericFor:
		for _, e := range n.Exprs {
			s.expr(e)
		}
		s.push()
		for _, name := range n.Names {
			if name != nil {
				s.bind(name.Name, liveCtx)
			}
		}
		s.block(n.Block)
		s.pop()
	case *ast.GotoStmt, *ast.LabelStmt:
		// Label namespace: neither bindings nor value uses.
	case *ast.DeferStmt:
		s.expr(n.Call)
	case *ast.SpawnStmt:
		s.expr(n.Call)
	case *ast.SelectStmt:
		for _, c := range n.Cases {
			if c == nil {
				continue
			}
			if c.Recv != nil {
				s.expr(c.Recv)
			}
			if c.Send != nil {
				s.expr(c.Send)
			}
			if c.Timeout != nil {
				s.expr(c.Timeout)
			}
			if c.RecvLocal {
				s.push()
				for _, v := range c.RecvVars {
					if v != nil {
						s.bind(v.Name, liveCtx)
					}
				}
				s.block(c.Block)
				s.pop()
			} else {
				// Predeclared case targets: conservative uses.
				for _, v := range c.RecvVars {
					s.use(v)
				}
				s.block(c.Block)
			}
		}
		if n.Default != nil {
			s.push()
			s.block(n.Default)
			s.pop()
		}
	case *ast.ImportStmt:
		// Side-effect require or module binding: always kept.
		if n.Name != nil {
			s.bind(n.Name.Name, liveCtx)
		}
	case *ast.TypeAliasDecl, *ast.InterfaceDecl:
		// Erased: no runtime references.
	case *ast.EnumDecl:
		// Enum tables are built at runtime: member values are live code.
		for _, m := range n.Members {
			if m != nil {
				s.expr(m.Value)
			}
		}
	case *ast.ClassDecl:
		// Class tables are always emitted: initializers and bodies inherit
		// the enclosing context (an edge when nested in a dead candidate).
		for _, m := range n.Members {
			if m == nil {
				continue
			}
			s.expr(m.Init)
			if m.Body != nil {
				s.push()
				for _, p := range m.Params {
					if p != nil && p.Name != nil {
						s.bind(p.Name.Name, liveCtx)
					}
				}
				if m.VarArgName != nil {
					s.bind(m.VarArgName.Name, liveCtx)
				}
				s.block(m.Body)
				s.pop()
			}
		}
	case *ast.MacroDef:
		// Post-expansion these are gone; pre-expansion treat bodies as
		// ordinary code in the enclosing context (conservative).
		s.push()
		for _, p := range n.Params {
			if p != nil {
				s.bind(p.Name, liveCtx)
			}
		}
		if n.IsExpr {
			s.expr(n.Body)
		} else {
			s.block(n.Block)
		}
		s.pop()
	default:
		s.tainted = true
	}
}

func (s *shaker) funcLit(n *ast.FuncLit) {
	if n == nil || s.tainted {
		return
	}
	s.push()
	if n.IsMethod {
		s.bind("self", liveCtx)
	}
	for _, p := range n.Params {
		if p != nil && p.Name != nil {
			s.bind(p.Name.Name, liveCtx)
		}
	}
	if n.VarArgName != nil {
		s.bind(n.VarArgName.Name, liveCtx)
	}
	s.block(n.Body)
	s.pop()
}

func (s *shaker) expr(x ast.Expr) {
	if x == nil || s.tainted {
		return
	}
	switch n := x.(type) {
	case *ast.Ident:
		s.use(n)
	case *ast.NumberLit, *ast.StringLit, *ast.BoolLit, *ast.NilLit, *ast.VarArg:
	case *ast.ParenExpr:
		s.expr(n.E)
	case *ast.TableLit:
		for _, f := range n.Fields {
			if f == nil {
				continue
			}
			// NamedField Key is a field name, not a variable use.
			s.expr(f.KeyX)
			s.expr(f.Val)
		}
	case *ast.FuncLit:
		s.funcLit(n)
	case *ast.IndexExpr:
		s.expr(n.Obj)
		s.expr(n.Key)
	case *ast.FieldExpr:
		s.expr(n.Obj)
		// Field is a member name, not a variable use.
	case *ast.CallExpr:
		s.expr(n.Func)
		for _, a := range n.Args {
			s.expr(a)
		}
	case *ast.MethodCallExpr:
		s.expr(n.Obj)
		// Method is a member name, not a variable use.
		for _, a := range n.Args {
			s.expr(a)
		}
	case *ast.BinOpExpr:
		s.expr(n.L)
		s.expr(n.R)
	case *ast.UnOpExpr:
		s.expr(n.X)
	case *ast.CastExpr:
		s.expr(n.E) // type operand is erased: not a runtime use
	case *ast.NonNullExpr:
		s.expr(n.E)
	case *ast.AwaitExpr:
		s.expr(n.X)
	default:
		s.tainted = true
	}
}
