package shake

// 保守的 TypeLua tree-shaking：删除零引用的纯局部函数
// （`local function f() ... end`、单个 `local x = function...`）。
// 其余一切永远保留。
//
// 安全契约（这里的 bug 会变成坏程序，因此偏置为
// 保留）：
//   - 只有纯闭包定义是候选。调用、table/方法
//     赋值、全局、`require`/`import`、`return`、多值或
//     带属性（`<const>`/`<close>`）的局部、类、枚举、宏与
//     类型声明从不触及。
//   - 名字解析是作用域感知的（遮蔽、参数、for 变量、方法
//     `self`、select `case local` 绑定）。标签、字段/方法名
//     与类型注解在其他命名空间，从不计数。
//   - 赋值目标与非 `local` 的 select 绑定计为使用：
//     只写局部变量被保留（错失优化，而非 bug）。
//   - 传递性不动点：仅被死候选使用的候选随其死亡；
//     可达于活代码的任何东西存活。
//   - 失败安全默认：任何未知 Stmt/Expr 节点污染运行，
//     什么都不删，因此未来语言构造退化为今天的
//     行为而非误删。
//
// ShakeFile 原地修改 f（从每个块删除死定义语句）
// 并返回删除数量。

import (
	"github.com/typelua/typelua/internal/ast"
)

const liveCtx = -1

type candInfo struct {
	id   int
	stmt ast.Stmt // 死亡时要删除的定义语句
}

type binding struct {
	cand int // 候选 id；非候选为 liveCtx
}

type shaker struct {
	scopes  []map[string]*binding
	cands   []*candInfo
	roots   map[int]bool
	edges   map[int]map[int]bool // 外层候选 -> 被引用候选
	cur     int                  // 最内层外层候选，或 liveCtx
	tainted bool
}

// ShakeFile 从 f 删除死的纯局部函数定义。
// 从不失败：任何无法识别的形状都不删除。
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
			// 返回表达式是使用，从不是定义：无物可删。
		}
	}
	filter(f.Block)
	return n
}

// filterStmt 递归进入 st 的子块，使删除作用于任意深度。
// 未知语句保持原样（删除由指针驱动，因此这里
// 无需污染：只能删除先前证明死亡的定义）。
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

// filterExpr 递归进入表达式中嵌套的闭包。
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

// use 记录名字的值位置引用：在外层候选外为根边，
// 否则为外层候选的边。
func (s *shaker) use(id *ast.Ident) {
	if id == nil {
		return
	}
	for i := len(s.scopes) - 1; i >= 0; i-- {
		if b, ok := s.scopes[i][id.Name]; ok {
			if b.cand == liveCtx {
				return // 非候选绑定：无物需保留
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
	// 未解析：全局/builtin/字段——从不是候选。
}

// newCand 为纯闭包定义注册删除候选。
func (s *shaker) newCand(stmt ast.Stmt) int {
	id := len(s.cands)
	s.cands = append(s.cands, &candInfo{id: id, stmt: stmt})
	return id
}

// isPureFuncLit 报告无副作用的闭包值。
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
		// 保守：目标计为使用（只写局部变量被保留）。
		for _, t := range n.Targets {
			s.expr(t)
		}
		for _, v := range n.Values {
			s.expr(v)
		}
	case *ast.LocalAssign:
		// Attrs 与 Names 平行，无属性为 ""（见 parser.parseLocal）。
		if len(n.Names) == 1 && len(n.Values) == 1 && n.Names[0] != nil &&
			isPureFuncLit(n.Values[0]) && !(len(n.Attrs) > 0 && n.Attrs[0] != "") {
			id := s.newCand(st)
			// `local x = function`：值只走一次，
			// 候选作为外层上下文（x 本身在
			// 内部不可见，Lua 语义：仅之后绑定）。
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
			// 导出：外部契约。无函数体：退化恢复
			// 形状；普通绑定后继续（从不是候选）。
			if n.Name != nil {
				s.bind(n.Name.Name, liveCtx)
			}
			return
		}
		id := s.newCand(st)
		// `local function f`：f 在其自身函数体内可见（递归）。
		if n.Name != nil {
			s.bind(n.Name.Name, id)
		}
		saved := s.cur
		s.cur = id
		s.funcLit(n.Func)
		s.cur = saved
	case *ast.FuncDefStmt:
		// 全局/方法定义：始终保留；前缀表计为使用。
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
		// `until` 看到函数体局部变量：一个共享作用域。
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
		// 标签命名空间：既非绑定也非值使用。
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
				// 预声明 case 目标：保守使用。
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
		// 有副作用的 require 或模块绑定：始终保留。
		if n.Name != nil {
			s.bind(n.Name.Name, liveCtx)
		}
	case *ast.TypeAliasDecl, *ast.InterfaceDecl:
		// 擦除：无运行时引用。
	case *ast.EnumDecl:
		// 枚举表在运行时构建：成员值是活代码。
		for _, m := range n.Members {
			if m != nil {
				s.expr(m.Value)
			}
		}
	case *ast.ClassDecl:
		// 类表始终发射：初始化器与函数体继承
		// 外层上下文（嵌套在死候选中时为边）。
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
		// 展开后这些消失；展开前把函数体当作
		// 外层上下文中的普通代码（保守）。
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
			// NamedField 的 Key 是字段名，不是变量使用。
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
		// Field 是成员名，不是变量使用。
	case *ast.CallExpr:
		s.expr(n.Func)
		for _, a := range n.Args {
			s.expr(a)
		}
	case *ast.MethodCallExpr:
		s.expr(n.Obj)
		// Method 是成员名，不是变量使用。
		for _, a := range n.Args {
			s.expr(a)
		}
	case *ast.BinOpExpr:
		s.expr(n.L)
		s.expr(n.R)
	case *ast.UnOpExpr:
		s.expr(n.X)
	case *ast.CastExpr:
		s.expr(n.E) // 类型操作数被擦除：不是运行时使用
	case *ast.NonNullExpr:
		s.expr(n.E)
	case *ast.AwaitExpr:
		s.expr(n.X)
	default:
		s.tainted = true
	}
}
