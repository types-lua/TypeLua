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
	lua := findLua()
	if lua == "" {
		t.Skip("no lua interpreter on PATH")
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
	write("tlua.json", `{"compilerOptions": {"target": "5.4", "strict": true, "rootDir": ".", "outDir": "dist"},
"include": ["**/*.tl"], "exclude": ["dist"]}`)
	write("main.tl", main)
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
	if err != nil {
		t.Fatalf("lua failed: %v\n%s", err, out)
	}
	return string(out)
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
