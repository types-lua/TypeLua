package parser

import (
	"testing"

	"github.com/typelua/typelua/internal/ast"
)

// Concurrency syntax: async/await/spawn/select (all soft keywords).

func TestParseAsyncFunc(t *testing.T) {
	f := mustParse(t, `async function foo(): number return 1 end`)
	fd, ok := f.Block.Stmts[0].(*ast.FuncDefStmt)
	if !ok {
		t.Fatalf("not funcdef %T", f.Block.Stmts[0])
	}
	if !fd.Func.IsAsync {
		t.Fatalf("async marker lost")
	}
	f = mustParse(t, `local async function foo(): number return 1 end`)
	lf, ok := f.Block.Stmts[0].(*ast.LocalFuncDef)
	if !ok {
		t.Fatalf("not localfunc %T", f.Block.Stmts[0])
	}
	if !lf.Func.IsAsync {
		t.Fatalf("async marker lost (local)")
	}
	f = mustParse(t, `local f = async function(): number return 1 end`)
	la := f.Block.Stmts[0].(*ast.LocalAssign)
	fn, ok := la.Values[0].(*ast.FuncLit)
	if !ok || !fn.IsAsync {
		t.Fatalf("async literal lost: %T", la.Values[0])
	}
}

func TestParseAwait(t *testing.T) {
	f := mustParse(t, `local async function f(): number local v = await g() return v end`)
	lf := f.Block.Stmts[0].(*ast.LocalFuncDef)
	la := lf.Func.Body.Stmts[0].(*ast.LocalAssign)
	if _, ok := la.Values[0].(*ast.AwaitExpr); !ok {
		t.Fatalf("not await %T", la.Values[0])
	}
	// `await(x)` stays a call to a function named await.
	f = mustParse(t, `local x = await(1)`)
	la = f.Block.Stmts[0].(*ast.LocalAssign)
	if _, ok := la.Values[0].(*ast.CallExpr); !ok {
		t.Fatalf("await(x) must stay a call, got %T", la.Values[0])
	}
}

func TestParseSpawn(t *testing.T) {
	f := mustParse(t, `spawn worker(1, 2)`)
	if _, ok := f.Block.Stmts[0].(*ast.SpawnStmt); !ok {
		t.Fatalf("not spawn %T", f.Block.Stmts[0])
	}
	f = mustParse(t, `spawn obj:method()`)
	if _, ok := f.Block.Stmts[0].(*ast.SpawnStmt); !ok {
		t.Fatalf("not spawn method %T", f.Block.Stmts[0])
	}
	f = mustParse(t, `spawn function() print(1) end()`)
	if _, ok := f.Block.Stmts[0].(*ast.SpawnStmt); !ok {
		t.Fatalf("not spawn anon %T", f.Block.Stmts[0])
	}
	// `spawn(x)` stays a call; `local spawn = 1` stays a variable.
	f = mustParse(t, `local r = spawn(1)`)
	la := f.Block.Stmts[0].(*ast.LocalAssign)
	if _, ok := la.Values[0].(*ast.CallExpr); !ok {
		t.Fatalf("spawn(x) must stay a call, got %T", la.Values[0])
	}
	mustParse(t, "local spawn = 1\nprint(spawn + 1)")
	// `go` is an ordinary identifier (no special meaning).
	mustParse(t, "local go = 1\nprint(go + 1)")
	f = mustParse(t, `local r = go(1)`)
	la = f.Block.Stmts[0].(*ast.LocalAssign)
	if _, ok := la.Values[0].(*ast.CallExpr); !ok {
		t.Fatalf("go(x) must be a plain call, got %T", la.Values[0])
	}
}

func TestParseSelect(t *testing.T) {
	f := mustParse(t, `select {
  case local v = ch:recv() then
    print(v)
  case ch:send(x) then
    print("s")
  case timeout(1.0) then
    print("t")
  default then
    print("d")
}`)
	sel, ok := f.Block.Stmts[0].(*ast.SelectStmt)
	if !ok {
		t.Fatalf("not select %T", f.Block.Stmts[0])
	}
	if len(sel.Cases) != 3 || sel.Default == nil {
		t.Fatalf("cases=%d default=%v", len(sel.Cases), sel.Default != nil)
	}
	if sel.Cases[0].Recv == nil || len(sel.Cases[0].RecvVars) != 1 || !sel.Cases[0].RecvLocal {
		t.Fatalf("recv case wrong: %+v", sel.Cases[0])
	}
	if sel.Cases[1].Send == nil {
		t.Fatalf("send case wrong")
	}
	if sel.Cases[2].Timeout == nil {
		t.Fatalf("timeout case wrong")
	}
	// `select(...)` stays the varargs builtin call.
	f = mustParse(t, `local n = select("#", ...)`)
	_ = f
}

func TestParseAsyncMethod(t *testing.T) {
	f := mustParse(t, `class C {
  async fetch(): string return "x" end,
}`)
	cd := f.Block.Stmts[0].(*ast.ClassDecl)
	if !cd.Members[0].IsAsync {
		t.Fatalf("async method marker lost")
	}
	// A field named async still parses as a field.
	f = mustParse(t, `class D {
  async: string = "x",
}`)
	cd = f.Block.Stmts[0].(*ast.ClassDecl)
	if cd.Members[0].IsAsync || cd.Members[0].Kind != ast.ClassField {
		t.Fatalf("field named async misparsed")
	}
}

func TestParseConcurrencyErrors(t *testing.T) {
	for _, src := range []string{
		"spawn 42",
		"spawn x + 1",
		"select {\n case nosuch then\n end\n}",
		"select {\n default then\n print(1)\n case local v = ch:recv() then\nend}",
	} {
		_, diags := ParseFile("test.tl", src)
		if len(diags) == 0 {
			t.Fatalf("expected error for %q", src)
		}
	}
}

func TestParseSelectReturnAndDefer(t *testing.T) {
	// A bare `return` inside a case must not swallow the next `case`;
	// `defer` inside a case belongs to the enclosing function.
	f := mustParse(t, `local async function f(): void
  select {
    case local v = ch:recv() then
      defer print("cleanup", v)
      print("got", v)
      return
    case timeout(1.0) then
      print("timeout")
  }
end`)
	lf, ok := f.Block.Stmts[0].(*ast.LocalFuncDef)
	if !ok {
		t.Fatalf("not localfunc %T", f.Block.Stmts[0])
	}
	var sel *ast.SelectStmt
	for _, st := range lf.Func.Body.Stmts {
		if s, ok := st.(*ast.SelectStmt); ok {
			sel = s
		}
	}
	if sel == nil || len(sel.Cases) != 2 {
		t.Fatalf("select lost cases: %+v", lf.Func.Body.Stmts)
	}
	if sel.Cases[0].Block.Ret == nil {
		t.Fatalf("bare return in case body lost")
	}
}
