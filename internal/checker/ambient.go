package checker

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/expand"
)

// Ambient .d.tl support (like tsconfig types/typeRoots): declaration files
// are checked once into a shared parent scope holding builtins + globals.
// Program files then check in child scopes so shadowing stays detached and
// duplicate detection only applies within the file itself.
//
// The shared scope is frozen after construction: file checking only writes
// to its child top scope (bindVar/collectTypes touch cur only; lookups walk
// parents read-only), so sequential reuse is safe. Type entries are shared
// by pointer with warm caches; generic instantiation appends to shared
// cacheInst maps sequentially (no concurrency in Program.Check).

// BuildAmbient checks declFiles in order into one parent scope and returns
// it (builtins included) plus diagnostics. Files are expected pre-sorted;
// expansion runs once per file here (callers must not re-expand).
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
