package checker

import (
	"testing"
)

func TestConcurrencyOK(t *testing.T) {
	expectOK(t, `
local ch: chan<string> = makeChan<string>(1)
async function producer(): void
  await ch:send("hi")
  ch:close()
end
async function consumer(): string
  local v, ok = await ch:recv()
  if ok then return v end
  return "closed"
end
spawn producer()
local r: string = await consumer()
`)
	expectOK(t, `
local ch: chan<integer> = makeChan<integer>()
async function worker(n: integer): void print(n) end
async function f(): void
  spawn worker(1)
  select {
    case local v = ch:recv() then
      print(v)
    case ch:send(2) then
      print("s")
    case timeout(1.0) then
      print("t")
    default then
      print("d")
  }
end
`)
	expectOK(t, `
local wg = WaitGroup.new()
local mu = Mutex.new()
async function f(): void
  wg:add(1)
  await mu:lock()
  mu:unlock()
  wg:done()
  await wg:wait()
  await sleep(0.1)
end
`)
	// chan invariance: same-elem channels assign.
	expectOK(t, `
local a: chan<integer> = makeChan<integer>()
local b: chan<integer> = a
`)
	// select assign form to existing vars.
	expectOK(t, `
local ch: chan<integer> = makeChan<integer>()
local v: integer = 0
local ok: boolean = false
async function f(): void
  select {
    case v, ok = ch:recv() then
      print(v)
  }
end
`)
	// async class methods.
	expectOK(t, `
class W {
  id: integer,
  constructor(id: integer) self.id = id end,
  async run(ch: chan<integer>): void
    await ch:send(self.id)
  end,
}
local w = W.new(1)
local ch: chan<integer> = makeChan<integer>()
spawn w:run(ch)
`)
}

func TestConcurrencyErr(t *testing.T) {
	// await outside async.
	expectErr(t, `
local function f(): number
  local x = await sleep(1)
  return 1
end
`)
	// select outside async.
	expectErr(t, `
local ch: chan<integer> = makeChan<integer>()
local function f(): void
  select {
    case local v = ch:recv() then
      print(v)
  }
end
`)
	// makeChan without explicit T.
	expectErr(t, `local ch = makeChan()`)
	expectErr(t, `local ch = makeChan(2)`)
	// chan arity.
	expectErr(t, `local ch: chan = makeChan<string>()`)
	// chan invariance: different elems don't mix.
	expectErr(t, `
local a: chan<integer> = makeChan<integer>()
local b: chan<string> = a
`)
	// send wrong element type.
	expectErr(t, `
local ch: chan<integer> = makeChan<integer>()
async function f(): void
  await ch:send("nope")
end
`)
	// recv on non-channel.
	expectErr(t, `
local notchan: integer = 1
async function f(): void
  select {
    case local v = notchan:recv() then
      print(v)
  }
end
`)
	// case local with 3 vars.
	expectErr(t, `
local ch: chan<integer> = makeChan<integer>()
async function f(): void
  select {
    case local a, b, c = ch:recv() then
      print(a)
  }
end
`)
	// timeout needs a number.
	expectErr(t, `
async function f(): void
  select {
    case timeout("soon") then
      print(1)
  }
end
`)
}
