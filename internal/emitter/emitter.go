package emitter

import (
	"strings"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/sourcemap"
)

// Core emitter state, file drivers, and block output with source maps.

// Options for code generation.
type Options struct {
	// Target is "5.4" (default) or "5.1". Under 5.1, `<const>`/`<close>` are stripped.
	Target string
}

func DefaultOptions() Options { return Options{Target: "5.4"} }

// EmitFile renders a parsed file to Lua source (type erasure).
func EmitFile(f *ast.File, opt Options) string {
	out, _ := EmitFileWithMap(f, opt)
	return out
}

// EmitFileWithMap renders Lua source plus source-map entries (statement
// granularity, rune columns). Entries are always collected; encoding
// happens only when the caller wants a map (near-zero overhead).
func EmitFileWithMap(f *ast.File, opt Options) (string, []sourcemap.Mapping) {
	e := &emitter{opt: opt, line: 1}
	e.emitBlock(f.Block, 0)
	out := e.buf.String()
	if out == "" || strings.HasSuffix(out, "\n") {
		return out, e.mappings
	}
	return out + "\n", e.mappings
}

type pendingMark struct {
	span ast.Node
	name string
}

type emitter struct {
	opt        Options
	buf        strings.Builder
	indent     int
	classStack []classEmitCtx
	line       int // 1-based generated line
	col        int // 0-based generated column (runes)
	pending    *pendingMark
	mappings   []sourcemap.Mapping
}

type classEmitCtx struct {
	name string
	base string // "" if none
}

func (e *emitter) curClass() *classEmitCtx {
	if len(e.classStack) == 0 {
		return nil
	}
	return &e.classStack[len(e.classStack)-1]
}

func (e *emitter) write(s string) {
	for _, r := range s {
		// Flush a pending statement mark on the first visible rune.
		if e.pending != nil && r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			sp := e.pending.span.Span()
			e.mappings = append(e.mappings, sourcemap.Mapping{
				GenLine: e.line,
				GenCol:  e.col,
				SrcFile: sp.File,
				SrcLine: sp.Start.Line - 1,
				SrcCol:  sp.Start.Column - 1,
				Name:    e.pending.name,
			})
			e.pending = nil
		}
		e.buf.WriteRune(r)
		if r == '\n' {
			e.line++
			e.col = 0
		} else {
			e.col++
		}
	}
}
func (e *emitter) nl() { e.write("\n") }
func (e *emitter) pad() {
	for i := 0; i < e.indent; i++ {
		e.write("    ")
	}
}

// mark records the next visible output position for a source node.
func (e *emitter) mark(n ast.Node, name string) {
	e.pending = &pendingMark{span: n, name: name}
}

// stmtName extracts a debug name for function definitions.
func stmtName(st ast.Stmt) string {
	switch n := st.(type) {
	case *ast.LocalFuncDef:
		return n.Name.Name
	case *ast.FuncDefStmt:
		if n.Name != nil {
			s := ""
			for i, p := range n.Name.Prefix {
				if i > 0 {
					s += "."
				}
				s += p.Name
			}
			if n.Name.Method != nil {
				s += ":" + n.Name.Method.Name
			}
			return s
		}
	case *ast.ClassDecl:
		return n.Name.Name
	}
	return ""
}

func (e *emitter) emitBlock(b *ast.Block, ind int) {
	if b == nil {
		return
	}
	saved := e.indent
	e.indent = ind
	for _, st := range b.Stmts {
		e.emitStmt(st)
	}
	if b.Ret != nil {
		e.mark(b.Ret, "")
		e.pad()
		e.write("return")
		if len(b.Ret.Exprs) > 0 {
			e.write(" ")
			for i, x := range b.Ret.Exprs {
				if i > 0 {
					e.write(", ")
				}
				e.emitExpr(x)
			}
		}
		e.nl()
	}
	e.indent = saved
}
