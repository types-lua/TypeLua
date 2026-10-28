package checker

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/types"
)

// 设计者视角：require 解析分两层——RequiredModules 静态收集路径供驱动，
// requirePathType 在检查时查 RequireTypes 映射；ffi 特例在检查侧短路。

// Cross-file require support: the driver (compiler.Program) resolves
// dotted require paths to files, checks them in dependency order, and
// feeds each file the module types of its requirements through
// Options.RequireTypes. Unresolved requirements stay `any`.
//
// 设计者视角：跨文件是“驱动排序、检查取货”——RequiredModules 静态收路径（供驱动
// 拓扑），requirePathType 检查时按 RequireTypes 取模块型（无货即 any），ffi
// 短路精确模块。障碍是 require 被局部遮蔽：形似而非 builtin，此时两端皆按 any
// （不取货不收录？收录仍收，取货放行，见 isShadowedRequire）。图：
//
//	收录(RequiredModules) → 驱动排序检查 → 取货(requirePathType)
//
// 取舍：未解一律 any（经典模式）。排查现场：跨文件取 any 先查驱动是否喂 RequireTypes。

// isShadowedRequire reports whether a local shadows the require builtin.
//
// 设计者视角：isShadowedRequire 是取货的“防假闸”——require 名被局部占即影子，
// 此时 require 调用走普通函数义（不取模块型）。查值册 IsBuiltin 判。排查现场：
// 跨文件类型丢失先查是否局部误名 require。
func isShadowedRequire(c *Checker) bool {
	if s := c.cur().lookupVar("require"); s != nil && !s.IsBuiltin {
		return true
	}
	return false
}

// unquoteModulePath strips one layer of Lua quotes from a require path
// literal (best-effort; escapes are rare in module paths).
//
// 设计者视角：unquoteModulePath 是模块径的“脱壳”——双/单引去壳，长串不认（false）。
// 经 parseStringLiteral（转义罕见，best-effort）。失败即弃（收录/取货皆跳）。
// 排查现场：require 路径收不上先查是否为长串或拼接（仅字面可收）。

func unquoteModulePath(sl *ast.StringLit) (string, bool) {
	if sl == nil {
		return "", false
	}
	lit := sl.Literal
	if len(lit) < 2 {
		return "", false
	}
	q := lit[0]
	if (q != '"' && q != '\'') || lit[len(lit)-1] != q {
		return "", false
	}
	name, _ := parseStringLiteral(lit)
	return name, true
}

// requirePathType resolves a require path literal through RequireTypes
// (Any when unresolved or shadowed). `require("ffi")` is special-cased to
// the precise LuaJIT FFI module so cross-language C-ABI bindings typecheck
// without a cast on the require itself (callers still `as` their CLib).
//
// 设计者视角：requirePathType 是取货的“货架”——先短路 ffi（未遮蔽即精确模块，
// 免 require 处强转），再影子/空表即 Any，未上架亦 Any。障碍是 ffi 与影子优先级：
// 影子优先（局部 require 即普通函数，不给 ffi）。取舍：nil 表项亦 Any。排查现场：
// ffi 取到 any 先查是否被遮蔽，普通模块取 any 先查驱动表键（点径拼写）。

func (c *Checker) requirePathType(lit *ast.StringLit) types.Type {
	name, ok := unquoteModulePath(lit)
	if ok && name == "ffi" && !isShadowedRequire(c) {
		return c.ffiModule()
	}
	if isShadowedRequire(c) || len(c.opt.RequireTypes) == 0 {
		return types.Any
	}
	if !ok {
		return types.Any
	}
	if t, ok := c.opt.RequireTypes[name]; ok && t != nil {
		return t
	}
	return types.Any
}

// RequiredModules lists dotted require paths in a file, first-seen order:
// `require("lit")` calls (any function position, shadowed or not) and
// import statement paths. Best-effort: unresolvable spellings are skipped,
// never reported (checking stays total).
//
// 设计者视角：RequiredModules 是驱动的“采买单”——全文件 walk 收 `require("字面")`
// （任意函数位，遮蔽亦收）与 import 径，首见序、去重。障碍是动态径（拼接/变量）：
// 解不出即跳（best-effort，不报）。候选：检查时收 vs 预收；选预收（驱动先排序）。
// 不变量：纯读 AST，无诊断。排查现场：依赖漏排先查径是否为字面。

func RequiredModules(f *ast.File) []string {
	w := &requireWalker{seen: map[string]bool{}}
	if f.Block != nil {
		w.block(f.Block)
	}
	return w.out
}

// requireWalker is the static collector state (seen-set plus ordered output).
//
// 设计者视角：requireWalker 是采买单的“账本”——seen 防重，out 保首见序。无诊断，
// 纯 walk。排查现场：顺序错乱先查是否复用同一 walker 跑多文件。
type requireWalker struct {
	seen map[string]bool
	out  []string
}

// add records one module path once (first-seen wins, unresolvable skipped).
//
// 设计者视角：add 是账本的“记一笔”——解不出或已见即弃，否则入 out。幂等。排查现场：
// 重复径多记先查 seen 是否每文件新账本。
func (w *requireWalker) add(sl *ast.StringLit) {
	name, ok := unquoteModulePath(sl)
	if !ok || w.seen[name] {
		return
	}
	w.seen[name] = true
	w.out = append(w.out, name)
}

// block walks a block for require/import paths (statements plus return).
//
// 设计者视角：block/expr/stmt 三件是 walk 的“地毯式”——语句 switch 全覆盖（含
// 类体/宏体/select/spawn/defer），表达式递归（含调用内 require 形）。障碍是新
// 语法加语句易漏：漏即采买缺，驱动少排。取舍：手写 switch（显式）而非反射。
// 不变量：nil 块即返。排查现场：某处 require 收不上，先查对应 stmt/expr 分支是否缺失。
func (w *requireWalker) block(b *ast.Block) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		w.stmt(st)
	}
	if b.Ret != nil {
		for _, e := range b.Ret.Exprs {
			w.expr(e)
		}
	}
}

// stmt walks one statement for embedded require paths.
//
// 设计者视角：见 block 注释——语句级分流，缺分支即漏收。排查现场同上。
func (w *requireWalker) stmt(st ast.Stmt) {
	switch n := st.(type) {
	case *ast.ExprStmt:
		w.expr(n.E)
	case *ast.AssignStmt:
		for _, t := range n.Targets {
			w.expr(t)
		}
		for _, v := range n.Values {
			w.expr(v)
		}
	case *ast.LocalAssign:
		for _, v := range n.Values {
			w.expr(v)
		}
	case *ast.LocalFuncDef:
		w.expr(n.Func)
	case *ast.FuncDefStmt:
		w.expr(n.Func)
	case *ast.DoBlock:
		w.block(n.Block)
	case *ast.IfStmt:
		w.expr(n.Cond)
		w.block(n.Then)
		for _, ei := range n.Elseifs {
			w.expr(ei.Cond)
			w.block(ei.Block)
		}
		w.block(n.Else)
	case *ast.WhileStmt:
		w.expr(n.Cond)
		w.block(n.Block)
	case *ast.RepeatStmt:
		w.block(n.Block)
		w.expr(n.Cond)
	case *ast.NumericFor:
		w.expr(n.Start)
		w.expr(n.End)
		if n.Step != nil {
			w.expr(n.Step)
		}
		w.block(n.Block)
	case *ast.GenericFor:
		for _, x := range n.Exprs {
			w.expr(x)
		}
		w.block(n.Block)
	case *ast.EnumDecl:
		for _, m := range n.Members {
			if m.Value != nil {
				w.expr(m.Value)
			}
		}
	case *ast.MacroDef:
		if n.Body != nil {
			w.expr(n.Body)
		}
		w.block(n.Block)
	case *ast.ClassDecl:
		for _, m := range n.Members {
			if m.Init != nil {
				w.expr(m.Init)
			}
			w.block(m.Body)
		}
	case *ast.ImportStmt:
		w.add(n.Path)
	case *ast.DeferStmt:
		w.expr(n.Call)
	case *ast.SpawnStmt:
		w.expr(n.Call)
	case *ast.SelectStmt:
		for _, sc := range n.Cases {
			if sc.Recv != nil {
				w.expr(sc.Recv.Obj)
				for _, a := range sc.Recv.Args {
					w.expr(a)
				}
			}
			if sc.Send != nil {
				w.expr(sc.Send.Obj)
				for _, a := range sc.Send.Args {
					w.expr(a)
				}
			}
			if sc.Timeout != nil {
				w.expr(sc.Timeout)
			}
			w.block(sc.Block)
		}
		w.block(n.Default)
	}
}

// expr walks one expression, collecting `require("lit")` calls.
//
// 设计者视角：见 block 注释——表达式级递归，调用位特判 require 字面（任意函数位）。
// 余者透视。排查现场：嵌套 require（如 f(require("x"))）漏收先查递归是否到 Args。
func (w *requireWalker) expr(x ast.Expr) {
	switch n := x.(type) {
	case *ast.ParenExpr:
		w.expr(n.E)
	case *ast.TableLit:
		for _, f := range n.Fields {
			if f.KeyX != nil {
				w.expr(f.KeyX)
			}
			if f.Val != nil {
				w.expr(f.Val)
			}
		}
	case *ast.FuncLit:
		w.block(n.Body)
	case *ast.IndexExpr:
		w.expr(n.Obj)
		w.expr(n.Key)
	case *ast.FieldExpr:
		w.expr(n.Obj)
	case *ast.CallExpr:
		if id, ok := n.Func.(*ast.Ident); ok && id.Name == "require" && len(n.Args) == 1 {
			if sl, ok := n.Args[0].(*ast.StringLit); ok {
				w.add(sl)
			}
		}
		w.expr(n.Func)
		for _, a := range n.Args {
			w.expr(a)
		}
	case *ast.MethodCallExpr:
		w.expr(n.Obj)
		for _, a := range n.Args {
			w.expr(a)
		}
	case *ast.BinOpExpr:
		w.expr(n.L)
		w.expr(n.R)
	case *ast.UnOpExpr:
		w.expr(n.X)
	case *ast.CastExpr:
		w.expr(n.E)
	case *ast.NonNullExpr:
		w.expr(n.E)
	case *ast.AwaitExpr:
		w.expr(n.X)
	}
}
