package checker

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// Block checking and per-block type collection.

// ---------- blocks ----------

// recordTypeDef records a type declaration name.
func (c *Checker) recordTypeDef(span token.Span, e *typeEntry) {
	if c.Index == nil || e == nil {
		return
	}
	c.Index.Uses = append(c.Index.Uses, SymUse{Span: span, IsDef: true, Entry: e})
}

func (c *Checker) collectTypes(b *ast.Block, sc *Scope) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		switch n := st.(type) {
		case *ast.TypeAliasDecl:
			if _, ok := sc.types[n.Name.Name]; !ok {
				e := &typeEntry{name: n.Name.Name, File: c.file, params: n.Generics, alias: n, span: n.Name.Span(), cacheInst: map[string]types.Type{}}
				sc.types[n.Name.Name] = e
				c.recordTypeDef(n.Name.Span(), e)
			} else {
				c.errorf(diag.DuplicateType, n.Span(), "duplicate type '%s'", n.Name.Name)
			}
		case *ast.InterfaceDecl:
			if _, ok := sc.types[n.Name.Name]; !ok {
				e := &typeEntry{name: n.Name.Name, File: c.file, params: n.Generics, iface: n, span: n.Name.Span(), cacheInst: map[string]types.Type{}}
				sc.types[n.Name.Name] = e
				c.recordTypeDef(n.Name.Span(), e)
			} else {
				c.errorf(diag.DuplicateType, n.Span(), "duplicate type '%s'", n.Name.Name)
			}
		case *ast.EnumDecl:
			if _, ok := sc.types[n.Name.Name]; !ok {
				e := &typeEntry{name: n.Name.Name, enumDecl: n, span: n.Name.Span(), cacheInst: map[string]types.Type{}}
				sc.types[n.Name.Name] = e
				c.recordTypeDef(n.Name.Span(), e)
			} else {
				c.errorf(diag.DuplicateType, n.Span(), "duplicate type '%s'", n.Name.Name)
			}
		case *ast.ClassDecl:
			if _, ok := sc.types[n.Name.Name]; !ok {
				e := &typeEntry{name: n.Name.Name, File: c.file, params: n.Generics, classDecl: n, span: n.Name.Span(), cacheInst: map[string]types.Type{}}
				sc.types[n.Name.Name] = e
				c.recordTypeDef(n.Name.Span(), e)
			} else {
				c.errorf(diag.DuplicateType, n.Span(), "duplicate type '%s'", n.Name.Name)
			}
		}
	}
}

func (c *Checker) checkBlock(b *ast.Block) {
	if b == nil {
		return
	}
	c.collectTypes(b, c.cur())
	c.recordScope(c.cur(), b.Span())
	for _, st := range b.Stmts {
		c.checkStmt(st)
	}
	if b.Ret != nil {
		c.checkReturn(b.Ret)
	}
}
