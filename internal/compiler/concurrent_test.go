package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/typelua/typelua/internal/config"
)

// runConcProject checks, emits, and runs main.tl under a real Lua
// interpreter, returning stdout. Skips when no interpreter is on PATH.
func runConcProject(t *testing.T, main string) string {
	t.Helper()
	out, _ := runConcProjectFiles(t, map[string]string{"main.tl": main})
	return out
}

// runConcProjectFiles is runConcProject for multi-file programs. It
// returns stdout plus the Lua exit error (nil on success), so tests can
// assert on programs that fail at runtime (e.g. send on closed channel).
func runConcProjectFiles(t *testing.T, files map[string]string) (string, error) {
	return runConcProjectFilesTarget(t, "5.4", files)
}

func runConcProjectFilesTarget(t *testing.T, target string, files map[string]string) (string, error) {
	t.Helper()
	lua := findLua()
	if lua == "" {
		t.Skip("no lua interpreter on PATH")
	}
	if target == "5.1" {
		// Prefer a real 5.1 when checking the 5.1 runtime paths.
		for _, c := range []string{"lua5.1", "luajit"} {
			if p, err := exec.LookPath(c); err == nil {
				lua = p
				break
			}
		}
	}
	dir := t.TempDir()
	write := func(name, content string) {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("tlua.json", `{"compilerOptions": {"target": "`+target+`", "strict": true, "rootDir": ".", "outDir": "dist"},
"include": ["**/*.tl"], "exclude": ["dist"]}`)
	for name, content := range files {
		write(name, content)
	}
	cfg, err := config.Load(filepath.Join(dir, "tlua.json"))
	if err != nil {
		t.Fatal(err)
	}
	p := NewProgram(cfg)
	if err := p.Load(); err != nil {
		t.Fatal(err)
	}
	p.Check()
	if p.HasErrors() {
		t.Fatalf("diags:\n%s", p.FormatDiags())
	}
	if _, err := p.Emit(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(lua, "dist/main.lua")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestConcurrentPingPong(t *testing.T) {
	out := runConcProject(t, `local ch: chan<string> = makeChan<string>(1)
async function producer(): void
  await ch:send("hello")
  ch:close()
end
async function consumer(): string
  local v, ok = await ch:recv()
  if ok then return v end
  return "closed"
end
spawn producer()
local r: string = await consumer()
print(r)
`)
	if out != "hello\n" {
		t.Fatalf("got %q", out)
	}
}

func TestConcurrentFanInSelect(t *testing.T) {
	out := runConcProject(t, `local ch: chan<integer> = makeChan<integer>(1)
local mu = Mutex.new()
local wg = WaitGroup.new()
local total: integer = 0
async function worker(id: integer): void
  await ch:send(id * 10)
  wg:done()
end
async function main(): void
  wg:add(2)
  spawn worker(1)
  spawn worker(2)
  local got: integer = 0
  for i = 1, 2 do
    select {
      case local v = ch:recv() then
        await mu:lock()
        total = total + v
        got = got + 1
        mu:unlock()
      case timeout(1.0) then
        print("timeout")
    }
  end
  await wg:wait()
  print("got", got, "total", total)
end
await main()
`)
	if out != "got\t2\ttotal\t30\n" {
		t.Fatalf("got %q", out)
	}
}

func TestConcurrentCloseIterDefault(t *testing.T) {
	out := runConcProject(t, `local ch: chan<string> = makeChan<string>(2)
async function main(): void
  await ch:send("a")
  await ch:send("b")
  ch:close()
  for v in ch:iter() do
    print("iter", v)
  end
  select {
    case local x, ok = ch:recv() then
      print("closed", x, ok)
    default then
      print("default")
  }
  await sleep(0.01)
  print("done")
end
await main()
`)
	want := "iter\ta\niter\tb\nclosed\tnil\tfalse\ndone\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestConcurrentRendezvous(t *testing.T) {
	// Unbuffered channel: sender blocks until receiver arrives.
	out := runConcProject(t, `local ch: chan<integer> = makeChan<integer>()
async function sender(): void
  print("sending")
  await ch:send(42)
  print("sent")
end
async function main(): void
  spawn sender()
  await sleep(0.05)
  print("receiving")
  local v = await ch:recv()
  print("got", v)
end
await main()
`)
	want := "sending\nreceiving\ngot\t42\nsent\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestConcurrentNoRuntimeWhenUnused(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.CompilerOptions.RootDir = "."
	cfg.CompilerOptions.OutDir = filepath.Join(dir, "dist")
	p := NewProgram(cfg)
	p.LoadSources(map[string]string{"a.tl": "local await = 1\nprint(await)\n"})
	p.Check()
	if p.HasErrors() {
		t.Fatalf("diags:\n%s", p.FormatDiags())
	}
	emitted, err := p.Emit()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(emitted[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "__tl_run") {
		t.Fatalf("plain file must not carry the scheduler:\n%s", raw)
	}
}

func TestConcurrentMacroDeferSelectCompose(t *testing.T) {
	// Statement macros carrying awaits, plus defer + bare return inside a
	// select case, all compose.
	out := runConcProject(t, `macro emit_send(ch, v) do
  await ch:send(v)
end
local ch: chan<integer> = makeChan<integer>(2)
async function main(): void
  emit_send(ch, 11)
  emit_send(ch, 22)
  ch:close()
  select {
    case local v = ch:recv() then
      defer print("cleanup", v)
      print("got", v)
      return
    case timeout(1.0) then
      print("timeout")
  }
end
await main()
print("after")
`)
	want := "got\t11\ncleanup\t11\nafter\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestConcurrentModuleReturn(t *testing.T) {
	// A required module with top-level concurrency (scheduler wrapper +
	// `return M`) threads its table through __tl_run (reentrant require).
	out, err := runConcProjectFiles(t, map[string]string{
		"worker.tl": `local M = {}
async function M.double(x: integer): integer
  await sleep(0.01)
  return x * 2
end
spawn M.double(0)
return M
`,
		"main.tl": `import W from "worker"
async function main(): void
  local v: integer = await W.double(21)
  print("answer", v)
end
await main()
`,
	})
	if err != nil {
		t.Fatalf("lua failed: %v\n%s", err, out)
	}
	if out != "answer\t42\n" {
		t.Fatalf("got %q", out)
	}
}

func TestConcurrentSelectSendVsClose(t *testing.T) {
	// A parked select-sender whose channel is closed must fail in the
	// SELECTOR task; the closer itself must succeed.
	out, err := runConcProjectFiles(t, map[string]string{
		"main.tl": `local ch: chan<integer> = makeChan<integer>()
async function sender(): void
  select {
    case ch:send(1) then
      print("sent (bad)")
    case timeout(5.0) then
      print("timeout (bad)")
  }
end
async function main(): void
  spawn sender()
  await sleep(0.05)
  ch:close()
  print("closer ok")
end
await main()
`,
	})
	if err == nil {
		t.Fatalf("expected the selector to fail, got %q", out)
	}
	if !strings.Contains(out, "closer ok") {
		t.Fatalf("closer must succeed, got %q", out)
	}
	if !strings.Contains(out, "send on closed channel") {
		t.Fatalf("selector must report send-on-closed, got %q", out)
	}
}

func TestConcurrentSelectSenderValuePreserved(t *testing.T) {
	// A parked select-sender whose case wins when buffer space frees up
	// must actually deliver its value (it used to be dropped).
	out := runConcProject(t, `local ch: chan<integer> = makeChan<integer>(1)
async function sel_sender(): void
  select {
    case ch:send(99) then
      print("select-sent")
    case timeout(2.0) then
      print("SENDER TIMEOUT (bad)")
  }
end
async function main(): void
  await ch:send(1)
  spawn sel_sender()
  await sleep(0.02)
  local v1 = await ch:recv()
  local v2 = await ch:recv()
  print("v1", v1, "v2", v2)
end
await main()
`)
	want := "v1\t1\tv2\t99\nselect-sent\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestConcurrentMultiTimeoutPicksEarliest(t *testing.T) {
	// With several sleep cases the earliest deadline wins even when it is
	// listed later; the first-listed (5s) case must not fire early.
	out := runConcProject(t, `local ch: chan<integer> = makeChan<integer>()
async function main(): void
  select {
    case timeout(5.0) then
      print("5s fired early (bad)")
    case timeout(0.05) then
      print("short fired")
    case ch:recv() then
      print("recv (bad)")
  }
end
await main()
`)
	if out != "short fired\n" {
		t.Fatalf("got %q", out)
	}
}

func TestConcurrentBreakExitsSelect(t *testing.T) {
	// `break` in a select case leaves the select (Go semantics), not an
	// enclosing loop; a nested loop inside the case keeps its own break.
	out := runConcProject(t, `local ch: chan<integer> = makeChan<integer>(1)
async function main(): void
  local outer: integer = 0
  while outer < 3 do
    outer = outer + 1
    select {
      case ch:recv() then
        print("recv (bad)")
      default then
        break
    }
    print("after select", outer)
  end
  print("done", outer)
  local n: integer = 0
  select {
    case ch:recv() then
      print("bad")
    default then
      while true do
        n = n + 1
        if n >= 2 then
          break
        end
      end
      print("inner broke at", n)
  }
  print("still in task")
end
await main()
`)
	want := "after select\t1\nafter select\t2\nafter select\t3\ndone\t3\ninner broke at\t2\nstill in task\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestConcurrentTryChannelOps(t *testing.T) {
	// trySend/tryRecv never park, are callable from sync code, and agree
	// with the introspection helpers (cap/closed are methods, not fields).
	out := runConcProject(t, `local ch: chan<string> = makeChan<string>(1)
local function sync_probe(): void
  print("tryRecv empty:", ch:tryRecv())
  print("trySend:", ch:trySend("a"))
  print("trySend full:", ch:trySend("b"))
  print("len", ch:len(), "cap", ch:cap(), "closed", ch:closed())
  local v, ok = ch:tryRecv()
  print("tryRecv:", v, ok)
  print("tryRecv empty again:", ch:tryRecv())
  ch:close()
  print("closed now", ch:closed())
  local v2, ok2 = ch:tryRecv()
  print("after close:", v2, ok2)
end
sync_probe()
async function receiver(un: chan<integer>): void
  await sleep(0.01)
  local got = await un:recv()
  print("receiver got", got)
end
async function main(): void
  local un: chan<integer> = makeChan<integer>()
  print("unbuffered trySend:", un:trySend(1))
  spawn receiver(un)
  await sleep(0.03)
  print("unbuffered trySend parked:", un:trySend(7))
end
await main()
`)
	want := "tryRecv empty:\tnil\tfalse\n" +
		"trySend:\ttrue\n" +
		"trySend full:\tfalse\n" +
		"len\t1\tcap\t1\tclosed\tfalse\n" +
		"tryRecv:\ta\ttrue\n" +
		"tryRecv empty again:\tnil\tfalse\n" +
		"closed now\ttrue\n" +
		"after close:\tnil\tfalse\n" +
		"unbuffered trySend:\tfalse\n" +
		"unbuffered trySend parked:\ttrue\n" +
		"receiver got\t7\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestConcurrentCloseWakesAllSelectors(t *testing.T) {
	// Closing wakes every parked selector with (nil, false): `ok` must be
	// exactly false (the resume-arity fix keeps trailing false/error slots).
	out := runConcProject(t, `local N: integer = 6
local ch: chan<integer> = makeChan<integer>(1)
local wg = WaitGroup.new()
local sawFalse: integer = 0
local mu = Mutex.new()
async function waiter(id: integer): void
  select {
    case local v, ok = ch:recv() then
      await mu:lock()
      if v == nil and ok == false then
        sawFalse = sawFalse + 1
      end
      mu:unlock()
    case timeout(1.0) then
      print("waiter", id, "TIMEOUT (bad)")
  }
  wg:done()
end
async function main(): void
  wg:add(N)
  for i: integer = 1, N do
    spawn waiter(i)
  end
  await sleep(0.02)
  ch:close()
  await wg:wait()
  print("sawFalse", sawFalse)
end
await main()
`)
	want := "sawFalse\t6\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestConcurrentStressFanIn(t *testing.T) {
	// Many selectors over shared buffered channels, drained by close:
	// exactly-once delivery and correct sum (exercises pump re-entrancy).
	out := runConcProject(t, `local N: integer = 8
local MSGS: integer = 120
local chans: chan<integer>[] = {}
for i: integer = 1, N do
  chans[i] = makeChan<integer>(2)
end
local wg = WaitGroup.new()
local total: integer = 0
local got: integer = 0
local mu = Mutex.new()
async function worker(id: integer): void
  local mine = chans[id]
  while true do
    select {
      case local v, ok = mine:recv() then
        if not ok then
          wg:done()
          return
        end
        await mu:lock()
        total = total + v
        got = got + 1
        mu:unlock()
      case timeout(2.0) then
        print("worker", id, "TIMEOUT (bad)")
        wg:done()
        return
    }
  end
end
async function main(): void
  wg:add(N)
  for i: integer = 1, N do
    spawn worker(i)
  end
  for m: integer = 1, MSGS do
    local target = (m % N) + 1
    await chans[target]:send(m)
  end
  for i: integer = 1, N do
    chans[i]:close()
  end
  await wg:wait()
  print("got", got, "total", total)
end
await main()
`)
	want := "got\t120\ttotal\t7260\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestConcurrentSpawnAnonymousZeroArg(t *testing.T) {
	// `spawn function() ... end` without a trailing call spawns with no
	// arguments; with a call it passes them.
	out := runConcProject(t, `local wg = WaitGroup.new()
async function main(): void
  wg:add(2)
  spawn function()
    print("anon no args")
    wg:done()
  end
  spawn function(x: integer)
    print("anon arg", x)
    wg:done()
  end(7)
  await wg:wait()
end
await main()
`)
	want := "anon no args\nanon arg\t7\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestConcurrentNestedSelectAndMethod(t *testing.T) {
	// Nested selects, a select in a class async method, and a select
	// inside an anonymous spawn body all compose.
	out := runConcProject(t, `class S {
  name: string,
  constructor(name: string) self.name = name end,
  async run(a: chan<integer>, b: chan<integer>): integer
    select {
      case local v = a:recv() then
        select {
          case local w = b:recv() then
            return v + w
          case timeout(1.0) then
            return -1
        }
      case timeout(1.0) then
        return -2
    }
    return 0
  end,
}
async function main(): void
  local a: chan<integer> = makeChan<integer>(1)
  local b: chan<integer> = makeChan<integer>(1)
  local s = S.new("nested")
  spawn function()
    select {
      case a:send(20) then print("spawn sent a")
      default then print("spawn default (bad)")
    }
  end
  local wg = WaitGroup.new()
  wg:add(1)
  spawn function()
    await b:send(22)
    wg:done()
  end
  local r = await s:run(a, b)
  await wg:wait()
  print("nested result", r, s.name)
end
await main()
`)
	want := "spawn sent a\nnested result\t42\tnested\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestConcurrentIterOnLua51(t *testing.T) {
	// Non-blocking iter (buffered values + close) works on the 5.1 target.
	out, err := runConcProjectFilesTarget(t, "5.1", map[string]string{
		"main.tl": `local ch: chan<string> = makeChan<string>(2)
async function main(): void
  await ch:send("a")
  await ch:send("b")
  ch:close()
  for v in ch:iter() do
    print("iter", v)
  end
  print("drained")
end
await main()
`,
	})
	if err != nil {
		t.Fatalf("lua failed: %v\n%s", err, out)
	}
	if out != "iter\ta\niter\tb\ndrained\n" {
		t.Fatalf("got %q", out)
	}
}

func TestConcurrentIterBlockingOnLua51FailsClearly(t *testing.T) {
	// A blocking iter on 5.1 must fail with guidance, not the cryptic
	// "attempt to yield across C-call boundary".
	out, err := runConcProjectFilesTarget(t, "5.1", map[string]string{
		"main.tl": `local ch: chan<string> = makeChan<string>()
async function main(): void
  for v in ch:iter() do
    print(v)
  end
end
await main()
`,
	})
	if err == nil {
		t.Fatalf("expected a clear runtime error, got %q", out)
	}
	if !strings.Contains(out, "ch:iter() cannot wait on Lua 5.1") {
		t.Fatalf("expected guidance message, got %q", out)
	}
}
