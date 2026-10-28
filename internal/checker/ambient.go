package checker

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/expand"
)

// 设计者视角：Ambient 作用域是"只读父节点"——所有文件共享同一份
// builtins + .d.tl 声明；文件检查只写入自己的子作用域，避免重复定义与
// 跨文件污染。共享 typeEntry 的缓存按顺序追加，不并发。
// 父层详见实现，排查看 Span。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// …（略，详见实现；排查先看 Span/诊断）
// 不变量：base 构造后只读（查找向上，写入向下）；顺序复用，无并发。排查现场：
// 若出现跨文件 DuplicateType，先查是否误把业务文件喂进此处；若宏诊断翻倍，
// 先查调用方是否二次 ExpandFile。
func BuildAmbient(declFiles []*ast.File, opt Options) (*Scope, []diag.Diagnostic) {
	c := New(opt)
	// 与入参父层脱钩，只用全新根。
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
