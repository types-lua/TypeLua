--- concurrent (Go-style CSP on the coroutine scheduler).
--- go + chan<T> + select + WaitGroup + Mutex, written with async/await.

local jobs: chan<integer> = makeChan<integer>(4)
local results: chan<integer> = makeChan<integer>(4)
local wg = WaitGroup.new()

async function worker(id: integer): void
  while true do
    local v, ok = await jobs:recv()
    if not ok then
      wg:done()
      return
    end
    print("worker", id, "did", v)
    await results:send(v * 2)
  end
end

async function main(): void
  -- Fan-out: 2 workers share the job channel.
  wg:add(2)
  spawn worker(1)
  spawn worker(2)
  for i: integer = 1, 6 do
    await jobs:send(i)
  end
  jobs:close()
  -- Fan-in: multiplex results with a timeout guard.
  local sum: integer = 0
  local got: integer = 0
  while got < 6 do
    select {
      case local r = results:recv() then
        sum = sum + r
        got = got + 1
      case timeout(5.0) then
        print("timed out")
        return
    }
  end
  await wg:wait()
  -- 2 * (1 + ... + 6) = 42
  print("sum", sum)
end

await main()
