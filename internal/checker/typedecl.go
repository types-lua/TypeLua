package checker

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/types"
)

// Type declaration checking (aliases, interfaces, enums).

// ---------- type declarations ----------

func (c *Checker) checkTypeAlias(n *ast.TypeAliasDecl) {
	e := c.cur().lookupType(n.Name.Name)
	if e == nil {
		return
	}
	// Resolve to surface errors + cycles.
	_ = c.resolveTypeEntry(e, nil)
}

func (c *Checker) checkInterface(n *ast.InterfaceDecl) {
	e := c.cur().lookupType(n.Name.Name)
	if e == nil {
		return
	}
	for _, m := range n.Fields {
		if m.Name == nil {
			continue
		}
		kind := "field"
		if _, ok := m.Type.(*ast.FuncType); ok {
			kind = "method"
		}
		c.recordMemberDef(m.Name.Span(), e, m.Name.Name, kind)
	}
	_ = c.resolveTypeEntry(e, nil)
}

func (c *Checker) checkEnum(n *ast.EnumDecl) {
	e := c.cur().lookupType(n.Name.Name)
	if e == nil {
		return
	}
	et := c.resolveTypeEntry(e, nil)
	// Define runtime value: enum table.
	if _, ok := et.(*types.Enum); ok {
		fields := map[string]types.Field{}
		for name, m := range et.(*types.Enum).Members {
			fields[name] = types.Field{Type: m}
		}
		obj := &types.Object{Name: n.Name.Name, Fields: fields}
		c.recordDefEntry(n.Name.Span(), c.bindVar(n.Name.Name, &Symbol{Name: n.Name.Name, Kind: symVar, Type: obj, Span: n.Span()}), e)
		for _, m := range n.Members {
			if m.Name == nil {
				continue
			}
			c.recordMemberDef(m.Name.Span(), e, m.Name.Name, "member")
		}
	}
}
