package checker

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/expand"
)

// 设计者视角：Ambient 作用域是"只读父节点"——所有文件共享同一份
// builtins + .d.tl 声明；文件检查只写入自己的子作用域，避免重复定义与
// 跨文件污染。共享 typeEntry 的缓存按顺序追加，不并发。

// Ambient .d.tl support (like tsconfig types/typeRoots): declaration files
// are checked once into a shared parent scope holding builtins + globals.
// Program files then check in child scopes so shadowing stays detached and
// duplicate detection only applies within the file itself.
//
// 共享作用域在构造后冻结：文件检查只写入其子顶层作用域（bindVar/collectTypes
// 仅触碰 cur；查找只读地向上遍历），因此可安全顺序复用。类型条目共享
// by pointer with warm caches; generic instantiation appends to shared
// cacheInst maps sequentially (no concurrency in Program.Check).

// BuildAmbient checks declFiles in order into one parent scope and returns
// it (builtins included) plus diagnostics. Files are expected pre-sorted;
// expansion runs once per file here (callers must not re-expand).
//
// 设计者视角：BuildAmbient 是声明文件的“预制地基”——把 builtins+.d.tl 按序
// 检查进同一父层，各业务文件再以其为 Ambient 开子层。障碍有二：一是重复展开
// （宏展开幂等但诊断会翻倍），故此处展开一次、调用方禁再展；二是跨文件污染
// （子文件写入父层），故文件检查只写子顶层（见 checkFileModule 的 newScope
// (opt.Ambient)）。候选：每文件独立 Ambient vs 共享；选共享，泛型实例缓存
// （cacheInst）跨文件复用。图：
//
//	[builtins + d1 + d2] → base(Ambient) → file子层（各写各的）
//
// 不变量：base 构造后只读（查找向上，写入向下）；顺序复用，无并发。排查现场：
// 若出现跨文件 DuplicateType，先查是否误把业务文件喂进此处；若宏诊断翻倍，
// 先查调用方是否二次 ExpandFile。
func BuildAmbient(declFiles []*ast.File, opt Options) (*Scope, []diag.Diagnostic) {
	c := New(opt)
	// Detach ambient from any incoming parent: fresh roots only.
	c.opt.Ambient = nil
	c.opt.RequireTypes = nil
	base := newScope(nil)
	c.scopes = []*Scope{base}
	c.defineBuiltins(base)
	c.Index = &SymIndex{}
	var diags []diag.Diagnostic
	for _, f := range declFiles {
		c.file = f.Name
		diags = append(diags, expand.ExpandFile(f)...)
		if f.Block != nil {
			c.recordScope(base, f.Block.Span())
		}
		c.checkBlock(f.Block)
	}
	diags = append(diags, c.diags...)
	return base, diags
}
