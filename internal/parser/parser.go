package parser

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/lexer"
	"github.com/typelua/typelua/internal/token"
)

// Parser is a recursive-descent parser with Pratt expressions and recovery.
type Parser struct {
	toks         []token.Token
	pos          int
	file         string
	diags        []diag.Diagnostic
	pendingGt    int // split >> into two >
	extendsDepth int // >0 while parsing an extends clause (for `infer`)
	inMacroBody  int // >0 while parsing a macro definition body
	depth        int // current nesting depth (blocks, primaries, primary types)
}

// maxParseDepth caps nesting so adversarial inputs (e.g. 100k open
// parens) report an error instead of exhausting the goroutine stack.
const maxParseDepth = 256

// enterDepth accounts one nesting level; a trip reports once and bails
// out to ParseFile (see depthBailout). Callers must not run after false.
func (p *Parser) enterDepth(sp token.Span) bool {
	p.depth++
	if p.depth > maxParseDepth {
		if p.depth == maxParseDepth+1 {
			p.errorf(diag.ParseNestingTooDeep, sp, "nesting too deep (maximum %d)", maxParseDepth)
		}
		panic(depthBailout{})
	}
	return true
}

func (p *Parser) leaveDepth() { p.depth-- }

func ParseFile(filename, src string) (*ast.File, []diag.Diagnostic) {
	toks, ldiags := lexer.Lex(filename, src)
	p := &Parser{toks: toks, file: filename, diags: append([]diag.Diagnostic{}, ldiags...)}
	block := p.parseFileBlock()
	eofSpan := token.Span{File: filename}
	if len(toks) > 0 {
		eofSpan = toks[len(toks)-1].Span
	}
	// Drain to EOF.
	return &ast.File{Name: filename, Block: block, EOF: eofSpan}, p.diags
}

// depthBailout unwinds a depth-limit trip straight to ParseFile: the file
// is pathologically nested, so one TL2015 plus an empty AST beats a flood
// of recovery artifacts.
type depthBailout struct{}

// parseFileBlock parses the top-level block, converting a depth bailout
// into an empty remainder.
func (p *Parser) parseFileBlock() (block *ast.Block) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(depthBailout); !ok {
				panic(r)
			}
			block = &ast.Block{S: token.Span{File: p.file}}
		}
	}()
	return p.parseBlock()
}
