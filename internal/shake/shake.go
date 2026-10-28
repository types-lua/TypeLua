package shake

// 保守的 TypeLua tree-shaking：删除零引用的纯局部函数
// （`local function f() ... end`、单个 `local x = function...`）。
// 其余一切永远保留。
//
// 安全契约（这里的 bug 会变成坏程序，因此偏置为
// 保留）：
//   - 只有纯闭包定义是候选。调用、table/方法
// …（略，详见实现；排查先看 Span/诊断）
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
//
// 设计者视角：ShakeFile 三幕——漫游建图（候选+根边+taint）、flood 求活、filter
// 删死。障碍一是安全压倒优化：未知节点即 taint 全保（见 default）；障碍二是
// 传递死：仅被死者引者随死，故 flood 自根沿边；障碍三是指针删：dead 以 Stmt 指针
// 判，filter 逐块过滤。候选：可达分析 vs 引用计数；选可达+不动点。不变量：
// taint 即零删；nil 返 0。排查现场：误删先查 taint 分支，漏删先查候选条件
// （纯闭包+单值+无属性）。
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
//
// 设计者视角：filterStmt 是删除的“下钻机”——只钻已知含块/闭包者，未知直留（此
// 处不 taint，因删集已定、指针判）。障碍是漏钻：某块不钻即其内死者删不掉，故须
// 与 shaker.stmt 覆盖对齐。候选：复用漫游 vs 独立；选独立轻量。不变量：只删 dead
// 指针。排查现场：深层死者未删先查此下钻是否漏该型。
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
//
// 设计者视角：filterExpr 补语句下钻的“表达式侧”——table/call/单目内闭包体亦有块
// 待滤。障碍是表达式无定义可删：只为钻到内层块。候选：合入 filterStmt vs 分立；
// 选分立，型别正交。不变量：未知表达式留。排查现场：表内闭包死者未删先查此下钻。
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

// 设计者视角：push 开新词法层（do/if/循环/函数/select 皆 push），pop 配对弹，
// 保遮蔽正确。障碍是 repeat 体 cond 同域（见 stmt）：须一 push 包两部。排查现场：
// 遮蔽错杀先查 push/pop 配对。
func (s *shaker) pop() { s.scopes = s.scopes[:len(s.scopes)-1] }

func (s *shaker) bind(name string, cand int) {
	// 设计者视角：bind 在顶层落户口——cand 为候选 id 或 liveCtx（非候选）。障碍是空名：
	// 直接弃防 map 脏。候选：覆写 vs 链；选覆写（遮蔽即新户，旧边已记）。排查现场：
	// 遮蔽后旧候选被误活先查 use 的最近层命中。
	if name == "" {
		return
	}
	s.scopes[len(s.scopes)-1][name] = &binding{cand: cand}
}

// use 记录名字的值位置引用：在外层候选外为根边，
// 否则为外层候选的边。
//
// 设计者视角：use 自内向外找最近绑定——非候选即返（无须保），候选则按 cur 分流：
// 顶层详见实现，排查看 Span。
// 标签/字段名调用方已避。候选：use 时 flood vs 事后 flood；选事后（图全再活）。
// 不变量：未解析名零操作。排查现场：死者被活先查是否误记根，活者被删先查 cur 上下文。
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
//
// 设计者视角：newCand 发号（len 即 id）登记 stmt 指针，调用方随即绑名+以 cur 切
// 上下文扫体。障碍是 id 须与 cands 下标对齐 flood；故 append 即号。排查现场：
// 删错行先查 stmt 指针登记。
func (s *shaker) newCand(stmt ast.Stmt) int {
	id := len(s.cands)
	s.cands = append(s.cands, &candInfo{id: id, stmt: stmt})
	return id
}

// isPureFuncLit 报告无副作用的闭包值。
//
// 设计者视角：纯度判据极严——仅 FuncLit 算纯（构造闭包无副作用），调用/表构造一
// 律不纯。障碍是 `local x = f()` 看似无害实则有调；故 non-Func 即非候选。候选：
// 数据流纯度 vs 语法纯；选语法，保守。不变量：非 FuncLit 恒 false。排查现场：
// 误删有副作用定义先查此判。
func isPureFuncLit(x ast.Expr) bool {
	_, ok := x.(*ast.FuncLit)
	return ok
}

func (s *shaker) block(b *ast.Block) {
	// 设计者视角：block 按“语句序+尾 return”漫游，taint 即停（保全）。障碍是 nil 块：
	// 空体直返。排查现场：taint 后仍删先查此早返。
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
	// 设计者视角：stmt 是候选的“安检门”——仅 `local function f`（非导出有体）与
	// `local x = function`（单名单值无属性）可进候选，余者或记活（全局/导出/
	// require/赋值目标/非 local 绑定）或下钻（块/类/枚举值）或 taint（未知）。障碍是
	// `local x = function` 中 x 体内不可见（Lua 语）：故先扫体后绑名；反之
	// `local function f` 递归可见：先绑后扫。候选：多值/属性 vs 严进；选严进。
	// 不变量：未知恒 taint。排查现场：候选漏/多先查此二分支条件。
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
	// 设计者视角：funcLit 开域绑参（含 self/具名 rest）再扫体，防参名被误作外层候选
	// 引用。障碍是方法 self：IsMethod 方绑。排查现场：参名致候选活先查此绑。
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
	// 设计者视角：expr 只认值位——Ident 即 use，NamedField Key/Field/Method 名非值
	// 位跳过，Cast 类型操作数非运行时跳过（见注释），未知即 taint 保全。障碍是字段名
	// 误计：`a.foo` 的 foo 若计 use 会保错候选；故跳。不变量：类型位永不 use。
	// 排查现场：候选误活先查字段名是否被计。
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
