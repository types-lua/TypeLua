package parser

import (
	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/token"
)

// Concurrency syntax (all soft keywords, Lua-superset preserving):
//
//	async function f() ... end        (global async)
//	local async function f() ... end  (local async)
//	local f = async function() ... end (async literal)
//	async methods: `async name(...) ... end` in classes
//	await <expr>                      (suspension marker, erases to expr)
//	spawn <call>                      (`spawn f(x)`, `spawn o:m()`, `spawn function() end()`)
//	select { case ... default ... }   (Go-style multiplex)
//
// `async`/`await`/`spawn`/`select`/`case`/`default` stay plain identifiers
// everywhere else (`local await = 1`, `spawn(x)`, `select(...)`, ...).

func isIdentNamed(t token.Token, name string) bool {
	return t.Kind == token.Ident && t.Literal == name
}

func sameLine(a, b token.Token) bool {
	return a.Span.Start.Line == b.Span.Start.Line
}

// ---------- async ----------

// isAsyncFuncStart reports `async function` at current pos (same line).
func (p *Parser) isAsyncFuncStart() bool {
	if !isIdentNamed(p.cur(), "async") {
		return false
	}
	n1 := p.peek(1)
	return n1.Kind == token.Function && sameLine(p.cur(), n1)
}

// parseAsyncFuncLit parses `async function...` assuming `async` not yet
// consumed. isMethod marks colon-methods (implicit self). Handles both
// anonymous (`async function() ... end`) and, when allowName is set,
// named forms via parseAsyncGlobal/parseAsyncLocal (not here).
func (p *Parser) parseAsyncFuncLit(isMethod bool) *ast.FuncLit {
	start := p.next() // async
	// Optional generics on anonymous async functions.
	var fgen []*ast.GenericParam
	if p.at(token.Lt) {
		save := p.pos
		nd := len(p.diags)
		g := p.parseGenericParams()
		if g != nil && p.at(token.LParen) {
			fgen = g
		} else {
			p.pos = save
			p.diags = p.diags[:nd]
		}
	}
	// p.cur() should be `function`; parseFuncBody expects it? No:
	// parseFuncBody expects `(` at cur (it is called after `function` is
	// consumed in parsePrimary). Mirror that: consume `function` then body.
	if !p.at(token.Function) {
		p.errorf(diag.ParseUnexpectedSyntax, p.cur().Span, "expected 'function' after 'async'")
		return &ast.FuncLit{Body: &ast.Block{}, IsAsync: true, S: start.Span}
	}
	p.next() // function
	var fgen2 []*ast.GenericParam
	if p.at(token.Lt) {
		save := p.pos
		nd := len(p.diags)
		g := p.parseGenericParams()
		if g != nil && p.at(token.LParen) {
			fgen2 = g
		} else {
			p.pos = save
			p.diags = p.diags[:nd]
		}
	}
	if fgen == nil {
		fgen = fgen2
	}
	fn := p.parseFuncBody(isMethod)
	fn.Generics = append(fgen, fn.Generics...)
	fn.IsAsync = true
	fn.S = token.Span{Start: start.Span.Start, End: fn.Span().End, File: p.file}
	return fn
}

// parseAsyncGlobal parses `async function name<T>(...) ... end` at
// statement level (global function, like parseGlobalFunc but async).
func (p *Parser) parseAsyncGlobal() ast.Stmt {
	start := p.next() // async
	p.next()          // function
	name := p.parseFuncName()
	if name == nil {
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	var generics []*ast.GenericParam
	if p.at(token.Lt) {
		save := p.pos
		nd := len(p.diags)
		g := p.parseGenericParams()
		if g != nil && p.at(token.LParen) {
			generics = g
		} else {
			p.pos = save
			p.diags = p.diags[:nd]
		}
	}
	fn := p.parseFuncBody(name.Method != nil)
	fn.IsAsync = true
	fn.S = token.Span{Start: start.Span.Start, End: fn.Span().End, File: p.file}
	return &ast.FuncDefStmt{Name: name, Generics: generics, Func: fn, S: token.Span{Start: start.Span.Start, End: fn.Span().End, File: p.file}}
}

// parseAsyncLocalFunc parses `async function name(...) ... end` after a
// `local` token (caller consumed `local` + peeked `async function`).
func (p *Parser) parseAsyncLocalFunc(start token.Token) ast.Stmt {
	astart := p.next() // async
	_ = astart
	p.next() // function
	name := p.parseIdentAllowSoft()
	if name == nil {
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
	var generics []*ast.GenericParam
	if p.at(token.Lt) {
		save := p.pos
		nd := len(p.diags)
		g := p.parseGenericParams()
		if g != nil && p.at(token.LParen) {
			generics = g
		} else {
			p.pos = save
			p.diags = p.diags[:nd]
		}
	}
	fn := p.parseFuncBody(false)
	fn.IsAsync = true
	sp := token.Span{Start: start.Span.Start, End: fn.Span().End, File: p.file}
	return &ast.LocalFuncDef{Name: name, Generics: generics, Func: fn, S: sp}
}

// ---------- await ----------

// isAwaitPrefix reports whether `await` at current pos starts an
// `await <expr>` (vs. a plain variable use). Rules:
//   - must be Ident "await" with an operand starting on the same line;
//   - `await` alone (delimiter/operator/EOL follows) is a variable;
//   - `await(`/`await{`/`await"..."`/`await.`/`await:`/`await[` stay calls
//     (Lua call/index syntax wins, preserving `await(x)` as a call);
//   - otherwise (Ident/Number/Function/not/-/~/`/await/function/...)
//     it is an await marker.
func (p *Parser) isAwaitPrefix() bool {
	if !isIdentNamed(p.cur(), "await") {
		return false
	}
	n1 := p.peek(1)
	if n1.Kind == token.EOF {
		return false
	}
	if !sameLine(p.cur(), n1) {
		return false
	}
	switch n1.Kind {
	case token.LParen, token.LBrace, token.String, token.Dot, token.Colon, token.LBrack:
		return false
	case token.Comma, token.Semi, token.RParen, token.RBrace, token.RBrack,
		token.Assign, token.Then, token.Do, token.Else, token.Elseif, token.End,
		token.Until, token.In, token.EOF:
		return false
	case token.Plus, token.Minus, token.Star, token.Slash, token.DoubleSlash,
		token.Percent, token.Caret, token.Hash, token.Amp, token.Tilde,
		token.Pipe, token.ShiftL, token.ShiftR, token.DotDot,
		token.Lt, token.Le, token.Gt, token.Ge, token.Eq, token.Ne,
		token.And, token.Or, token.ColonColon, token.Question, token.Arrow,
		token.FatArrow, token.Bang:
		// `await + 1` is `await` variable plus 1, not await of `+1`? Unary
		// minus is the exception: `await -x` awaits `-x` (common).
		// Keep it simple: binary position => variable.
		return false
	}
	// not/Hash/Tilde/Minus/Ident/Number/True/False/Nil/Function/... are operands.
	return true
}

func (p *Parser) parseAwait() ast.Expr {
	start := p.next() // await
	x := p.parseExprPrec(precUnary)
	if x == nil {
		p.errorf(diag.ParseExpectedExpression, p.cur().Span, "expected expression after 'await'")
		return &ast.Ident{Name: "await", S: start.Span}
	}
	return &ast.AwaitExpr{X: x, S: token.Span{Start: start.Span.Start, End: x.Span().End, File: p.file}}
}

// ---------- spawn ----------

// isSpawnStart reports `spawn <call>` at statement start: `spawn` + call
// operand on the same line. Genuine value uses stay plain: `spawn(x)`,
// `spawn = 1`, `spawn.foo`, `spawn:foo()`, `spawn + 1`, lone `spawn`.
// Anything else on the same line (including mistakes like `spawn 42`)
// enters parseSpawn for validation.
func (p *Parser) isSpawnStart() bool {
	if !isIdentNamed(p.cur(), "spawn") {
		return false
	}
	n1 := p.peek(1)
	if n1.Kind == token.EOF {
		return false
	}
	if !sameLine(p.cur(), n1) {
		return false
	}
	switch n1.Kind {
	case token.Dot, token.Colon, token.LParen, token.LBrace, token.String,
		token.LBrack, token.Comma, token.Semi, token.RParen, token.RBrace,
		token.RBrack, token.Assign, token.Then, token.Do, token.Else,
		token.Elseif, token.End, token.Until, token.In, token.ColonColon,
		token.Question, token.Arrow, token.FatArrow, token.Bang,
		token.Plus, token.Star, token.Slash, token.DoubleSlash,
		token.Percent, token.Caret, token.Amp, token.Tilde,
		token.Pipe, token.ShiftL, token.ShiftR, token.DotDot,
		token.Lt, token.Le, token.Gt, token.Ge, token.Eq, token.Ne,
		token.And, token.Or, token.Not, token.Hash:
		return false
	}
	return true
}

func (p *Parser) parseSpawn() ast.Stmt {
	start := p.next() // spawn
	// Anonymous `spawn function() ... end(args)`: the body runs as a task,
	// so it is implicitly async (await/select allowed inside).
	if p.at(token.Function) || (isIdentNamed(p.cur(), "async") && p.peek(1).Kind == token.Function && sameLine(p.cur(), p.peek(1))) {
		var fn ast.Expr
		if isIdentNamed(p.cur(), "async") {
			fn = p.parseAsyncFuncLit(false)
		} else {
			p.next() // function
			var fgen []*ast.GenericParam
			if p.at(token.Lt) {
				save := p.pos
				nd := len(p.diags)
				g := p.parseGenericParams()
				if g != nil && p.at(token.LParen) {
					fgen = g
				} else {
					p.pos = save
					p.diags = p.diags[:nd]
				}
			}
			fb := p.parseFuncBody(false)
			fb.Generics = fgen
			fb.IsAsync = true
			fn = fb
		}
		// `spawn function() ... end(args)`; the trailing call is optional
		// (`spawn function() ... end` spawns with no arguments).
		args := p.parseCallArgs()
		if args == nil {
			args = []ast.Expr{}
		}
		call := &ast.CallExpr{Func: fn, Args: args, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
		return &ast.SpawnStmt{Call: call, S: call.Span()}
	}
	call := p.parseSuffix()
	switch call.(type) {
	case *ast.CallExpr, *ast.MethodCallExpr:
		return &ast.SpawnStmt{Call: call, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
	default:
		p.errorf(diag.ParseUnexpectedSyntax, p.cur().Span, "expected a function call after 'spawn'")
		p.synchronize()
		return &ast.EmptyStmt{S: start.Span}
	}
}

// ---------- select ----------

// isSelectStart reports `select {` (same line): `select(...)` stays a call.
func (p *Parser) isSelectStart() bool {
	if !isIdentNamed(p.cur(), "select") {
		return false
	}
	n1 := p.peek(1)
	return n1.Kind == token.LBrace && sameLine(p.cur(), n1)
}

func (p *Parser) parseSelect() ast.Stmt {
	start := p.next() // select
	p.next()          // {
	var cases []*ast.SelectCase
	var defBlock *ast.Block
	var defSpan token.Span
	seenDefault := false
	for !p.at(token.RBrace) && !p.at(token.EOF) {
		if p.at(token.Semi) || p.at(token.Comma) {
			p.next()
			continue
		}
		if p.cur().Kind == token.Ident && p.cur().Literal == "default" {
			dstart := p.next()
			if seenDefault {
				p.errorf(diag.ParseUnexpectedSyntax, dstart.Span, "duplicate 'default' in select")
			}
			seenDefault = true
			// `default then` or `default:`? Accept `then`, `:`, or newline block.
			if p.cur().Kind == token.Then {
				p.next()
			} else if p.at(token.Colon) {
				p.next()
			}
			defBlock = p.parseSelectCaseBlock()
			defSpan = token.Span{Start: dstart.Span.Start, End: p.prevEnd(), File: p.file}
			continue
		}
		if p.cur().Kind == token.Ident && p.cur().Literal == "case" {
			if seenDefault {
				p.errorf(diag.ParseUnexpectedSyntax, p.cur().Span, "'case' after 'default' in select (default must be last)")
			}
			c := p.parseSelectCase()
			if c != nil {
				cases = append(cases, c)
			}
			continue
		}
		p.errorf(diag.ParseUnexpectedSyntax, p.cur().Span, "expected 'case' or 'default' in select, got %s", p.cur().Kind)
		p.synchronizeSelect()
	}
	p.expect(token.RBrace, "'}'")
	if len(cases) == 0 && defBlock == nil {
		p.errorf(diag.ParseUnexpectedSyntax, start.Span, "select must have at least one 'case' or 'default'")
	}
	return &ast.SelectStmt{Cases: cases, Default: defBlock, DS: defSpan, S: token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}}
}

func (p *Parser) synchronizeSelect() {
	for {
		k := p.cur().Kind
		if k == token.EOF || k == token.RBrace {
			return
		}
		if k == token.Ident && (p.cur().Literal == "case" || p.cur().Literal == "default") {
			return
		}
		if k == token.Semi || k == token.Comma {
			p.next()
			return
		}
		p.next()
	}
}

// parseSelectCaseBlock parses a case/default body up to the next
// `case`/`default`/`}` (with optional `return` tail, like parseBlock).
func (p *Parser) parseSelectCaseBlock() *ast.Block {
	var stmts []ast.Stmt
	var ret *ast.ReturnStmt
	for {
		if p.at(token.EOF) || p.at(token.RBrace) {
			break
		}
		if p.cur().Kind == token.Ident && (p.cur().Literal == "case" || p.cur().Literal == "default") {
			break
		}
		if p.at(token.Semi) {
			s := p.next()
			stmts = append(stmts, &ast.EmptyStmt{S: s.Span})
			continue
		}
		if p.at(token.Return) {
			// Bare `return` when a case/default/`}` follows (a select
			// case is not a Lua block end, so parseReturn would eat the
			// next `case` as a return value).
			n1 := p.peek(1)
			if n1.Kind == token.RBrace || n1.Kind == token.EOF ||
				(n1.Kind == token.Ident && (n1.Literal == "case" || n1.Literal == "default")) {
				start := p.next()
				ret = &ast.ReturnStmt{S: start.Span}
			} else {
				ret = p.parseReturn()
			}
			for p.at(token.Semi) {
				p.next()
			}
			break
		}
		st := p.parseStmt()
		if st == nil {
			break
		}
		stmts = append(stmts, st)
	}
	sp := token.Span{File: p.file}
	if len(stmts) > 0 {
		sp = token.Span{Start: stmts[0].Span().Start, End: stmts[0].Span().End, File: p.file}
		for _, s := range stmts {
			sp.End = s.Span().End
		}
		if ret != nil {
			sp.End = ret.Span().End
		}
	} else if ret != nil {
		sp = ret.Span()
	} else {
		sp = token.Span{Start: p.cur().Span.Start, End: p.cur().Span.Start, File: p.file}
	}
	return &ast.Block{Stmts: stmts, Ret: ret, S: sp}
}

// parseSelectCase parses `case <guard> then <block>`.
func (p *Parser) parseSelectCase() *ast.SelectCase {
	start := p.next() // case
	sc := &ast.SelectCase{S: token.Span{Start: start.Span.Start, End: start.Span.Start, File: p.file}}
	// `case local v = ch:recv()` form.
	if p.at(token.Local) {
		lstart := p.next()
		_ = lstart
		vars := []*ast.Ident{}
		n := p.parseIdentAllowSoft()
		if n == nil {
			p.synchronizeSelect()
			return nil
		}
		vars = append(vars, n)
		for p.at(token.Comma) {
			p.next()
			n2 := p.parseIdentAllowSoft()
			if n2 == nil {
				break
			}
			vars = append(vars, n2)
			if len(vars) > 2 {
				break
			}
		}
		if len(vars) > 2 {
			p.errorf(diag.ParseUnexpectedSyntax, p.cur().Span, "select receive binds at most 2 variables (v, ok)")
			p.synchronizeSelect()
			return nil
		}
		if !p.at(token.Assign) {
			p.errorf(diag.ParseUnexpectedSyntax, p.cur().Span, "expected '=' after select receive variables")
			p.synchronizeSelect()
			return nil
		}
		p.next()
		recv := p.parseSuffix()
		mc, ok := recv.(*ast.MethodCallExpr)
		if !ok || mc.Method.Name != "recv" {
			p.errorf(diag.ParseUnexpectedSyntax, p.cur().Span, "select 'case local v = ...' expects 'ch:recv()'")
			p.synchronizeSelect()
			return nil
		}
		sc.Recv = mc
		sc.RecvVars = vars
		sc.RecvLocal = true
	} else {
		// Try `v = ch:recv()` / `v, ok = ch:recv()` assign form via lookahead.
		if save := p.tryParseSelectRecvAssign(sc); save {
			// sc filled.
		} else {
			// Expression guard: `ch:send(v)`, `ch:recv()`, `sleep(s)`/`timeout(s)`.
			g := p.parseExpr()
			if g == nil {
				p.synchronizeSelect()
				return nil
			}
			switch n := g.(type) {
			case *ast.MethodCallExpr:
				if n.Method.Name == "send" {
					sc.Send = n
				} else if n.Method.Name == "recv" {
					sc.Recv = n
				} else {
					p.errorf(diag.ParseUnexpectedSyntax, g.Span(), "select case expects 'ch:recv()', 'ch:send(v)' or 'timeout(s)', got method ':%s'", n.Method.Name)
					p.synchronizeSelect()
					return nil
				}
			case *ast.CallExpr:
				if id, ok := n.Func.(*ast.Ident); ok && (id.Name == "sleep" || id.Name == "timeout") {
					if len(n.Args) != 1 {
						p.errorf(diag.ParseUnexpectedSyntax, g.Span(), "select timeout expects exactly 1 argument (seconds)")
						p.synchronizeSelect()
						return nil
					}
					sc.Timeout = n.Args[0]
				} else {
					p.errorf(diag.ParseUnexpectedSyntax, g.Span(), "select case expects 'ch:recv()', 'ch:send(v)' or 'timeout(s)'")
					p.synchronizeSelect()
					return nil
				}
			default:
				p.errorf(diag.ParseUnexpectedSyntax, g.Span(), "select case expects 'ch:recv()', 'ch:send(v)' or 'timeout(s)'")
				p.synchronizeSelect()
				return nil
			}
		}
	}
	// Guard/body separator: `then` preferred, `:` or `do` accepted.
	if p.cur().Kind == token.Then || p.at(token.Colon) || p.cur().Kind == token.Do {
		p.next()
	} else {
		p.errorf(diag.ParseUnexpectedSyntax, p.cur().Span, "expected 'then' after select case guard")
	}
	sc.Block = p.parseSelectCaseBlock()
	sc.S = token.Span{Start: start.Span.Start, End: p.prevEnd(), File: p.file}
	// Validate send arity now (cheap): `ch:send(v)` exactly 1 arg.
	if sc.Send != nil && len(sc.Send.Args) != 1 {
		p.errorf(diag.ParseUnexpectedSyntax, sc.Send.Span(), "'ch:send' expects exactly 1 argument, got %d", len(sc.Send.Args))
	}
	if sc.Recv != nil && len(sc.Recv.Args) != 0 {
		p.errorf(diag.ParseUnexpectedSyntax, sc.Recv.Span(), "'ch:recv' expects no arguments, got %d", len(sc.Recv.Args))
	}
	return sc
}

// tryParseSelectRecvAssign tries `v = ch:recv()` / `v, ok = ch:recv()`
// (up to 2 idents). Returns true and fills sc on success; rewinds otherwise.
func (p *Parser) tryParseSelectRecvAssign(sc *ast.SelectCase) bool {
	save := p.pos
	nd := len(p.diags)
	var vars []*ast.Ident
	if p.cur().Kind != token.Ident && !isSoftKw(p.cur().Kind) {
		return false
	}
	first := p.next()
	lit := first.Literal
	if lit == "" {
		lit = first.Kind.String()
	}
	// `case`/`default`/`then` can never start a recv-assign guard.
	if lit == "case" || lit == "default" || lit == "then" || lit == "do" || lit == "local" {
		p.pos = save
		p.diags = p.diags[:nd]
		return false
	}
	vars = append(vars, &ast.Ident{Name: lit, S: first.Span})
	if p.at(token.Comma) {
		// Need second ident + `=`. If not, rewind (it was e.g. `f(a, b)`? No:
		// guard exprs starting `ident ,` are rare; rewind to expr parse).
		p.next()
		if p.cur().Kind != token.Ident && !isSoftKw(p.cur().Kind) {
			p.pos = save
			p.diags = p.diags[:nd]
			return false
		}
		second := p.next()
		lit2 := second.Literal
		if lit2 == "" {
			lit2 = second.Kind.String()
		}
		vars = append(vars, &ast.Ident{Name: lit2, S: second.Span})
	}
	if !p.at(token.Assign) {
		p.pos = save
		p.diags = p.diags[:nd]
		return false
	}
	if len(vars) > 2 {
		p.pos = save
		p.diags = p.diags[:nd]
		return false
	}
	p.next() // =
	recv := p.parseSuffix()
	mc, ok := recv.(*ast.MethodCallExpr)
	if !ok || mc.Method.Name != "recv" {
		p.pos = save
		p.diags = p.diags[:nd]
		return false
	}
	// Success: keep diags as-is (suffix parse reported nothing fatal), fill sc.
	_ = nd
	sc.Recv = mc
	sc.RecvVars = vars
	return true
}
