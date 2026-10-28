package emitter

import (
	"strconv"

	"github.com/typelua/typelua/internal/ast"
)

// Cooperative CSP runtime (Go-style, single-threaded scheduler).
//
// Model: tasks are coroutines on one ready queue; channels/mutexes/
// waitgroups/timers park tasks and wake them. `async`/`await` erase (plain
// functions; blocking runtime calls yield by themselves), `spawn <call>`
// becomes `__tl_spawn(function() <call> end)`, `select` becomes an
// `__tl_select(specs, hasDefault)` poll/park plus an if-chain.
//
// prelude 兼容 5.1（无 goto、无 //、无位运算、无 <const>）且幂等
// （`if not __tl_run`），因此多文件程序可各自携带。用户 API 全局：
// makeChan、sleep、timeout、WaitGroup、Mutex。
// Everything else uses the `__tl_` prefix (reserved, see SPEC §12).
const concurrentPrelude = `if not __tl_run then
  __tl_ready = {}
  __tl_timers = {}
  __tl_select_waiters = {}
  __tl_running = nil
  __tl_task_count = 0
  __tl_pump_dirty = false
  __tl_fallback_clock = 0
  -- Lua 5.1 (and LuaJIT) cannot yield out of a generic-for iterator call.
  __tl_iter_can_wait = true
  if _VERSION == "Lua 5.1" then
    __tl_iter_can_wait = false
  end
  local function __tl_now()
    if os then
      if os.clock then
        return os.clock()
      end
      if os.time then
        return os.time()
      end
    end
    -- Degraded sandbox without clocks: a call counter still makes timers
    -- (and the idle spin) terminate instead of hanging forever.
    __tl_fallback_clock = __tl_fallback_clock + 1
    return __tl_fallback_clock
  end
  local __tl_unpack = table.unpack or unpack
  function __tl_spawn(fn)
    local t = {waiting = false, done = false, resume_args = nil, resume_n = 0, wake_err = nil}
    __tl_task_count = __tl_task_count + 1
    t.co = coroutine.create(function()
      fn()
      t.done = true
      __tl_task_count = __tl_task_count - 1
    end)
    __tl_ready[#__tl_ready + 1] = t
    return t
  end
  local function __tl_dequeue(q, rec)
    for i = 1, #q do
      if q[i] == rec then
        table.remove(q, i)
        return true
      end
    end
    return false
  end
  local function __tl_unpark_select(sw)
    for i = #__tl_select_waiters, 1, -1 do
      if __tl_select_waiters[i] == sw then
        table.remove(__tl_select_waiters, i)
        break
      end
    end
    for _, e in ipairs(sw.entries) do
      if e.kind == "recv" then
        __tl_dequeue(e.ch.recvq, e)
      elseif e.kind == "send" then
        __tl_dequeue(e.ch.sendq, e)
      end
    end
    for i = #__tl_timers, 1, -1 do
      if __tl_timers[i].select_waiter == sw then
        table.remove(__tl_timers, i)
      end
    end
  end
  local function __tl_wake_task(t, ...)
    if t.done or not t.waiting then
      return
    end
    t.waiting = false
    -- Capture the value count explicitly: {nil, false} has no reliable
    -- length border, so unpacking by # would drop trailing false/error slots.
    t.resume_args = {...}
    t.resume_n = select("#", ...)
    __tl_ready[#__tl_ready + 1] = t
  end
  local function __tl_wake_select(sw, idx, v, ok, errmsg)
    if sw.done then
      return
    end
    sw.done = true
    __tl_pump_dirty = true
    __tl_unpark_select(sw)
    __tl_wake_task(sw.task, idx, v, ok, errmsg)
  end
  local function __tl_peek_recv(ch)
    if ch.head <= ch.tail then
      return true, ch.buf[ch.head], true
    end
    if ch.isclosed then
      return true, nil, false
    end
    if #ch.sendq > 0 then
      return true, ch.sendq[1].val, true
    end
    return false
  end
  local function __tl_peek_send(ch)
    if ch.isclosed then
      return "closed"
    end
    if ch.head <= ch.tail then
      -- Buffered value present: space iff under capacity.
      if ch.tail - ch.head + 1 < ch.capacity then
        return true
      end
    elseif ch.capacity > 0 then
      return true
    end
    if #ch.recvq > 0 then
      return true
    end
    return false
  end
  local function __tl_admit_sender(ch)
    while #ch.sendq > 0 do
      local s = table.remove(ch.sendq, 1)
      if s.sel then
        if not s.sel.done then
          -- A parked select-sender whose case wins: its value must land
          -- in the freed buffer slot (dropping it would lose data).
          ch.tail = ch.tail + 1
          ch.buf[ch.tail] = s.val
          __tl_wake_select(s.sel, s.case_idx, nil, nil)
          return true
        end
      elseif not s.task.done and s.task.waiting then
        ch.tail = ch.tail + 1
        ch.buf[ch.tail] = s.val
        __tl_wake_task(s.task)
        return true
      end
    end
    return false
  end
  local function __tl_do_recv(ch)
    if ch.head <= ch.tail then
      local v = ch.buf[ch.head]
      ch.buf[ch.head] = nil
      ch.head = ch.head + 1
      __tl_admit_sender(ch)
      __tl_pump_selects()
      return v, true
    end
    if ch.isclosed then
      return nil, false
    end
    if #ch.sendq > 0 then
      local s = table.remove(ch.sendq, 1)
      if s.sel then
        if not s.sel.done then
          local idx = s.case_idx
          local v = s.val
          __tl_wake_select(s.sel, idx, nil, nil)
          __tl_pump_selects()
          return v, true
        end
        return __tl_do_recv(ch)
      end
      if not s.task.done then
        __tl_wake_task(s.task)
        __tl_pump_selects()
        return s.val, true
      end
      return __tl_do_recv(ch)
    end
    error("internal error: channel not ready")
  end
  local function __tl_do_send(ch, v)
    if ch.isclosed then
      error("send on closed channel")
    end
    while #ch.recvq > 0 do
      local r = table.remove(ch.recvq, 1)
      if r.sel then
        if not r.sel.done then
          local idx = r.case_idx
          __tl_wake_select(r.sel, idx, v, true)
          __tl_pump_selects()
          return
        end
      elseif not r.task.done and r.task.waiting then
        __tl_wake_task(r.task, v, true)
        __tl_pump_selects()
        return
      end
    end
    if ch.capacity > 0 then
      ch.tail = ch.tail + 1
      ch.buf[ch.tail] = v
      __tl_pump_selects()
      return
    end
    error("internal error: channel not ready")
  end
  -- Waking a parked selector: run the pump non-reentrantly and keep
  -- re-scanning while wakes happen (a wake can make other waiters ready).
  -- Each pass uses a snapshot, so unparking mid-pass is safe.
  local __tl_pumping = false
  function __tl_pump_selects()
    if __tl_pumping then
      return
    end
    __tl_pumping = true
    repeat
      __tl_pump_dirty = false
      local snapshot = {}
      for i = 1, #__tl_select_waiters do
        snapshot[i] = __tl_select_waiters[i]
      end
      for _, sw in ipairs(snapshot) do
        if not sw.done then
          for ci, g in ipairs(sw.cases) do
            local woke = false
            if g.kind == "recv" then
              if __tl_peek_recv(g.ch) then
                local v, ok = __tl_do_recv(g.ch)
                __tl_wake_select(sw, ci, v, ok)
                woke = true
              end
            elseif g.kind == "send" then
              local st = __tl_peek_send(g.ch)
              if st == true then
                __tl_do_send(g.ch, g.val)
                __tl_wake_select(sw, ci, nil, nil)
                woke = true
              elseif st == "closed" then
                -- Attribute the failure to the selector itself (raising
                -- here would blame whatever task happened to pump).
                __tl_wake_select(sw, ci, nil, nil, "send on closed channel")
                woke = true
              end
            elseif g.kind == "sleep" then
              -- Per-case absolute deadline: with several sleep cases the
              -- earliest one must not fire the first case listed.
              if __tl_now() >= g.at then
                __tl_wake_select(sw, ci, nil, nil)
                woke = true
              end
            end
            if woke then
              break
            end
          end
        end
      end
    until not __tl_pump_dirty
    __tl_pumping = false
  end
  local function __tl_require_task(what)
    local self = __tl_running
    if self == nil then
      error(what .. " outside async task (call it with 'await' inside 'async', or from the main chunk)")
    end
    return self
  end
  function __tl_chan_send(ch, v)
    local st = __tl_peek_send(ch)
    if st == true then
      __tl_do_send(ch, v)
      return
    elseif st == "closed" then
      error("send on closed channel")
    end
    local self = __tl_require_task("channel send")
    self.waiting = true
    local rec = {task = self, kind = "send", val = v, sel = nil}
    ch.sendq[#ch.sendq + 1] = rec
    coroutine.yield()
    if self.wake_err ~= nil then
      local m = self.wake_err
      self.wake_err = nil
      error(m)
    end
  end
  function __tl_chan_recv(ch)
    local ready, v, ok = __tl_peek_recv(ch)
    if ready then
      return __tl_do_recv(ch)
    end
    local self = __tl_require_task("channel receive")
    self.waiting = true
    local rec = {task = self, kind = "recv", sel = nil}
    ch.recvq[#ch.recvq + 1] = rec
    coroutine.yield()
    local a = self.resume_args or {}
    self.resume_args = nil
    return a[1], a[2]
  end
  function __tl_chan_close(ch)
    if ch.isclosed then
      error("close of closed channel")
    end
    ch.isclosed = true
    while #ch.recvq > 0 do
      local r = table.remove(ch.recvq, 1)
      if r.sel then
        -- Select waiters observe close via the pump (nil, false).
      elseif not r.task.done and r.task.waiting then
        __tl_wake_task(r.task, nil, false)
      end
    end
    while #ch.sendq > 0 do
      local s = table.remove(ch.sendq, 1)
      if s.sel then
        -- Select senders on a closed channel fail when polled.
      elseif not s.task.done and s.task.waiting then
        s.task.wake_err = "send on closed channel"
        __tl_wake_task(s.task)
      end
    end
    __tl_pump_selects()
  end
  function __tl_select(specs, has_default)
    local self = __tl_require_task("select")
    local deadline = nil
    for _, g in ipairs(specs) do
      if g.kind == "sleep" then
        g.at = __tl_now() + (tonumber(g.after) or 0)
        if deadline == nil or g.at < deadline then
          deadline = g.at
        end
      end
    end
    -- Fast poll in case order.
    for ci, g in ipairs(specs) do
      if g.kind == "recv" then
        local ready = __tl_peek_recv(g.ch)
        if ready then
          local v, ok = __tl_do_recv(g.ch)
          return ci, v, ok
        end
      elseif g.kind == "send" then
        local st = __tl_peek_send(g.ch)
        if st == true then
          __tl_do_send(g.ch, g.val)
          return ci, nil, nil
        elseif st == "closed" then
          error("send on closed channel")
        end
      elseif g.kind == "sleep" then
        if __tl_now() >= g.at then
          return ci, nil, nil
        end
      end
    end
    if has_default then
      return 0, nil, nil
    end
    local sw = {task = self, cases = specs, deadline = deadline or math.huge, done = false, entries = {}}
    if deadline ~= nil then
      __tl_timers[#__tl_timers + 1] = {wake = deadline, select_waiter = sw}
    end
    for ci, g in ipairs(specs) do
      if g.kind == "recv" then
        local e = {task = self, kind = "recv", sel = sw, case_idx = ci, ch = g.ch}
        g.ch.recvq[#g.ch.recvq + 1] = e
        sw.entries[#sw.entries + 1] = e
      elseif g.kind == "send" then
        local e = {task = self, kind = "send", val = g.val, sel = sw, case_idx = ci, ch = g.ch}
        g.ch.sendq[#g.ch.sendq + 1] = e
        sw.entries[#sw.entries + 1] = e
      end
    end
    __tl_select_waiters[#__tl_select_waiters + 1] = sw
    self.waiting = true
    coroutine.yield()
    local a = self.resume_args or {}
    self.resume_args = nil
    if a[4] ~= nil then
      error(a[4])
    end
    return a[1], a[2], a[3]
  end
  function __tl_sleep(sec)
    local self = __tl_require_task("sleep")
    self.waiting = true
    __tl_timers[#__tl_timers + 1] = {wake = __tl_now() + (tonumber(sec) or 0), task = self}
    coroutine.yield()
  end
  local __tl_chan_mt = {__index = {
    send = function(self, v) return __tl_chan_send(self, v) end,
    recv = function(self) return __tl_chan_recv(self) end,
    -- Non-blocking variants (never park, callable from sync code):
    -- trySend returns false when the channel is full (unbuffered: no
    -- waiting receiver); tryRecv returns nil, false when nothing is
    -- buffered (and nil, false on a drained closed channel too).
    trySend = function(self, v)
      local st = __tl_peek_send(self)
      if st == true then
        __tl_do_send(self, v)
        return true
      elseif st == "closed" then
        error("send on closed channel")
      end
      return false
    end,
    tryRecv = function(self)
      local ready = __tl_peek_recv(self)
      if ready then
        return __tl_do_recv(self)
      end
      return nil, false
    end,
    close = function(self) return __tl_chan_close(self) end,
    len = function(self)
      if self.head <= self.tail then
        return self.tail - self.head + 1
      end
      return 0
    end,
    cap = function(self) return self.capacity end,
    closed = function(self) return self.isclosed end,
    iter = function(self)
      -- On Lua 5.1 a yield cannot cross the generic-for iterator call, so
      -- a blocking iterator is impossible there: drain what is buffered
      -- (or the closed signal) and fail with guidance instead of letting
      -- the VM raise "attempt to yield across C-call boundary".
      return function()
        local ready = __tl_peek_recv(self)
        if not ready and __tl_iter_can_wait == false then
          error("ch:iter() cannot wait on Lua 5.1 (yield cannot cross the for-loop iterator); use 'while true do select { case local v, ok = ch:recv() then ... } end'")
        end
        if ready then
          local v, ok = __tl_do_recv(self)
          if ok then
            return v
          end
          return nil
        end
        local v, ok = __tl_chan_recv(self)
        if ok then
          return v
        end
        return nil
      end
    end,
  }}
  function makeChan(cap)
    local c = tonumber(cap) or 0
    if c < 0 then
      error("makeChan: capacity must be >= 0")
    end
    local ch = {buf = {}, head = 1, tail = 0, capacity = c, isclosed = false, sendq = {}, recvq = {}}
    setmetatable(ch, __tl_chan_mt)
    return ch
  end
  function sleep(sec) return __tl_sleep(sec) end
  function timeout(sec) return __tl_sleep(sec) end
  local __tl_wg_mt = {__index = {
    add = function(self, n)
      self.count = self.count + (tonumber(n) or 0)
      if self.count < 0 then
        error("WaitGroup counter went negative")
      end
    end,
    done = function(self)
      self.count = self.count - 1
      if self.count < 0 then
        error("WaitGroup counter went negative")
      end
      if self.count == 0 then
        while #self.waitq > 0 do
          local t = table.remove(self.waitq, 1)
          __tl_wake_task(t)
        end
      end
    end,
    wait = function(self)
      if self.count <= 0 then
        return
      end
      local cur = __tl_require_task("WaitGroup wait")
      cur.waiting = true
      self.waitq[#self.waitq + 1] = cur
      coroutine.yield()
    end,
  }}
  WaitGroup = {new = function()
    local wg = {count = 0, waitq = {}}
    setmetatable(wg, __tl_wg_mt)
    return wg
  end}
  local __tl_mu_mt = {__index = {
    lock = function(self)
      if not self.locked then
        self.locked = true
        return
      end
      local cur = __tl_require_task("Mutex lock")
      cur.waiting = true
      self.waitq[#self.waitq + 1] = cur
      coroutine.yield()
    end,
    unlock = function(self)
      if not self.locked then
        error("unlock of unlocked mutex")
      end
      while #self.waitq > 0 do
        local t = table.remove(self.waitq, 1)
        if not t.done and t.waiting then
          __tl_wake_task(t)
          return
        end
      end
      self.locked = false
    end,
    tryLock = function(self)
      if not self.locked then
        self.locked = true
        return true
      end
      return false
    end,
  }}
  Mutex = {new = function()
    local mu = {locked = false, waitq = {}}
    setmetatable(mu, __tl_mu_mt)
    return mu
  end}
  function __tl_run(main_fn)
    if __tl_running ~= nil then
      return main_fn()
    end
    local main_rets = nil
    local main_task = {waiting = false, done = false, resume_args = nil, resume_n = 0, wake_err = nil}
    main_task.co = coroutine.create(function()
      local function pack(...) return {n = select("#", ...), ...} end
      main_rets = pack(main_fn())
      main_task.done = true
      __tl_task_count = __tl_task_count - 1
    end)
    __tl_task_count = __tl_task_count + 1
    __tl_ready[#__tl_ready + 1] = main_task
    while true do
      local now = __tl_now()
      -- Collect fired timers first: pumping can remove other entries
      -- (a woken select unparks its timers), so mutating while scanning
      -- would corrupt the reverse iteration.
      local fired = nil
      for i = #__tl_timers, 1, -1 do
        if now >= __tl_timers[i].wake then
          local tm = table.remove(__tl_timers, i)
          if fired == nil then
            fired = {}
          end
          fired[#fired + 1] = tm
        end
      end
      if fired ~= nil then
        for _, tm in ipairs(fired) do
          if tm.task ~= nil then
            __tl_wake_task(tm.task)
          end
        end
      end
      __tl_pump_selects()
      local t = table.remove(__tl_ready, 1)
      if t == nil then
        local unfinished = __tl_task_count
        if unfinished <= 0 and #__tl_timers == 0 then
          break
        end
        if #__tl_timers > 0 then
          local next_wake = __tl_timers[1].wake
          for i = 2, #__tl_timers do
            if __tl_timers[i].wake < next_wake then
              next_wake = __tl_timers[i].wake
            end
          end
          while __tl_now() < next_wake do end
        else
          error("deadlock: all tasks blocked (no ready tasks, no timers)")
        end
      else
        __tl_running = t
        local n = t.resume_n or 0
        local ok, err = coroutine.resume(t.co, __tl_unpack(t.resume_args or {}, 1, n))
        t.resume_args = nil
        t.resume_n = 0
        __tl_running = nil
        if not ok then
          error(err)
        end
        if coroutine.status(t.co) == "dead" then
          t.done = true
        elseif t.waiting then
          -- Parked: stays out of the ready queue until woken.
        else
          -- Bare yield (user coroutine.yield): requeue cooperatively.
          __tl_ready[#__tl_ready + 1] = t
        end
      end
    end
    if main_rets == nil then
      return nil
    end
    return __tl_unpack(main_rets, 1, main_rets.n)
  end
end
`

// asyncFunc is true when fn is an async literal (for future use).
func (e *emitter) emitSpawn(n *ast.SpawnStmt) {
	e.pad()
	e.write("__tl_spawn(function() ")
	e.emitExpr(n.Call)
	e.write(" end)")
	e.nl()
}

func (e *emitter) emitSelect(n *ast.SelectStmt) {
	e.tmpCount++
	tag := strconv.Itoa(e.tmpCount)
	e.pad()
	e.write("local __tl_specs_" + tag + " = {")
	e.nl()
	for _, sc := range n.Cases {
		e.pad()
		if sc.Recv != nil {
			e.write("    {kind = \"recv\", ch = (")
			e.emitExpr(sc.Recv.Obj)
			e.write(")},")
			e.nl()
		} else if sc.Send != nil {
			e.write("    {kind = \"send\", ch = (")
			e.emitExpr(sc.Send.Obj)
			e.write("), val = (")
			if len(sc.Send.Args) > 0 {
				e.emitExpr(sc.Send.Args[0])
			} else {
				e.write("nil")
			}
			e.write(")},")
			e.nl()
		} else if sc.Timeout != nil {
			e.write("    {kind = \"sleep\", after = (")
			e.emitExpr(sc.Timeout)
			e.write(")},")
			e.nl()
		}
	}
	e.pad()
	e.write("}")
	e.nl()
	e.pad()
	hasDef := "false"
	if n.Default != nil {
		hasDef = "true"
	}
	e.write("local __tl_idx_" + tag + ", __tl_v_" + tag + ", __tl_ok_" + tag +
		" = __tl_select(__tl_specs_" + tag + ", " + hasDef + ")")
	e.nl()
	// `repeat ... until true` gives `break` inside a case Go-like
	// semantics: it leaves the select, not an enclosing loop (a nested
	// loop inside the case still owns its own `break`).
	e.pad()
	e.write("repeat")
	e.nl()
	first := true
	for i, sc := range n.Cases {
		e.pad()
		if first {
			e.write("    if")
			first = false
		} else {
			e.write("    elseif")
		}
		e.write(" __tl_idx_" + tag + " == " + strconv.Itoa(i+1) + " then")
		e.nl()
		if sc.Recv != nil && len(sc.RecvVars) > 0 {
			e.pad()
			if sc.RecvLocal {
				e.write("        local ")
			} else {
				e.write("        ")
			}
			for j, v := range sc.RecvVars {
				if j > 0 {
					e.write(", ")
				}
				e.write(v.Name)
			}
			e.write(" = __tl_v_" + tag)
			if len(sc.RecvVars) > 1 {
				e.write(", __tl_ok_" + tag)
			}
			e.nl()
		}
		e.emitBlock(sc.Block, e.indent+2)
	}
	e.pad()
	if n.Default != nil {
		if first {
			// Only default: no condition needed, but keep shape uniform.
			e.write("    if true then")
			e.nl()
		} else {
			e.write("    else")
			e.nl()
		}
		e.emitBlock(n.Default, e.indent+2)
		e.pad()
		e.write("    end")
		e.nl()
	} else {
		e.write("    end")
		e.nl()
	}
	e.pad()
	e.write("until true")
	e.nl()
}

// NeedsConcurrency reports whether a file uses cooperative concurrency:
// spawn/await/select nodes, or the channel/timer/sync user globals.
func NeedsConcurrency(f *ast.File) bool {
	w := &concWalker{}
	if f.Block != nil {
		w.block(f.Block)
	}
	return w.found
}

type concWalker struct {
	found bool
}

func (w *concWalker) block(b *ast.Block) {
	if b == nil || w.found {
		return
	}
	for _, st := range b.Stmts {
		w.stmt(st)
		if w.found {
			return
		}
	}
	if b.Ret != nil {
		for _, e := range b.Ret.Exprs {
			w.expr(e)
			if w.found {
				return
			}
		}
	}
}

func (w *concWalker) stmt(st ast.Stmt) {
	if w.found {
		return
	}
	switch n := st.(type) {
	case *ast.SpawnStmt:
		w.found = true
	case *ast.SelectStmt:
		w.found = true
	case *ast.ExprStmt:
		w.expr(n.E)
	case *ast.AssignStmt:
		for _, t := range n.Targets {
			w.expr(t)
		}
		for _, v := range n.Values {
			w.expr(v)
		}
	case *ast.LocalAssign:
		for _, v := range n.Values {
			w.expr(v)
		}
	case *ast.LocalFuncDef:
		w.expr(n.Func)
	case *ast.FuncDefStmt:
		w.expr(n.Func)
	case *ast.DoBlock:
		w.block(n.Block)
	case *ast.IfStmt:
		w.expr(n.Cond)
		w.block(n.Then)
		for _, ei := range n.Elseifs {
			w.expr(ei.Cond)
			w.block(ei.Block)
		}
		w.block(n.Else)
	case *ast.WhileStmt:
		w.expr(n.Cond)
		w.block(n.Block)
	case *ast.RepeatStmt:
		w.block(n.Block)
		w.expr(n.Cond)
	case *ast.NumericFor:
		w.expr(n.Start)
		w.expr(n.End)
		if n.Step != nil {
			w.expr(n.Step)
		}
		w.block(n.Block)
	case *ast.GenericFor:
		for _, x := range n.Exprs {
			w.expr(x)
		}
		w.block(n.Block)
	case *ast.EnumDecl:
		for _, m := range n.Members {
			if m.Value != nil {
				w.expr(m.Value)
			}
		}
	case *ast.ClassDecl:
		for _, m := range n.Members {
			if m.Init != nil {
				w.expr(m.Init)
			}
			w.block(m.Body)
		}
	case *ast.DeferStmt:
		w.expr(n.Call)
	case *ast.ImportStmt, *ast.EmptyStmt, *ast.BreakStmt, *ast.GotoStmt,
		*ast.LabelStmt, *ast.TypeAliasDecl, *ast.InterfaceDecl, *ast.MacroDef:
	}
}

func (w *concWalker) expr(x ast.Expr) {
	if w.found || x == nil {
		return
	}
	switch n := x.(type) {
	case *ast.AwaitExpr:
		w.found = true
	case *ast.Ident:
		switch n.Name {
		case "makeChan", "sleep", "timeout", "WaitGroup", "Mutex":
			w.found = true
		}
	case *ast.FuncLit:
		w.block(n.Body)
	case *ast.ParenExpr:
		w.expr(n.E)
	case *ast.TableLit:
		for _, f := range n.Fields {
			if f.KeyX != nil {
				w.expr(f.KeyX)
			}
			w.expr(f.Val)
		}
	case *ast.IndexExpr:
		w.expr(n.Obj)
		w.expr(n.Key)
	case *ast.FieldExpr:
		w.expr(n.Obj)
	case *ast.CallExpr:
		if id, ok := n.Func.(*ast.Ident); ok {
			switch id.Name {
			case "makeChan", "sleep", "timeout":
				w.found = true
				return
			}
		}
		w.expr(n.Func)
		for _, a := range n.Args {
			w.expr(a)
		}
	case *ast.MethodCallExpr:
		w.expr(n.Obj)
		for _, a := range n.Args {
			w.expr(a)
		}
	case *ast.BinOpExpr:
		w.expr(n.L)
		w.expr(n.R)
	case *ast.UnOpExpr:
		w.expr(n.X)
	case *ast.CastExpr:
		w.expr(n.E)
	case *ast.NonNullExpr:
		w.expr(n.E)
	}
}
