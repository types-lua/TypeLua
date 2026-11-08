--- Built-in test helpers for `tlua test`.
---
--- Test files return a table of named cases:
---
---   import T from "tlua_test"
---   return {
---     ["adds numbers"] = function(): void
---       T.eq(1 + 1, 2)
---     end,
---   }
---
--- `tlua test` injects this module at `<project>/tlua_modules/tlua_test.tl`,
--- so the import type-checks and runs without any setup.
---
--- An assertion raises on failure, so a case stops at its first broken
--- expectation; the runner records the case once and reports its message.

type Failure = {
  module: string,
  case: string,
  message: string,
}

local passedCases: number = 0
local failedCases: number = 0
local checks: number = 0
local currentModule: string = ""
local currentCase: string = ""
local filter: string = ""
local moduleExact: string = ""
local caseExact: string = ""
local json: boolean = false
local failures: Array<Failure> = {}

local function jsonEscape(s: string): string
  return string.gsub(s, "[\\\"\n\r\t]", function(c: string): string
    if c == "\\" then return "\\\\" end
    if c == "\"" then return "\\\"" end
    if c == "\n" then return "\\n" end
    if c == "\r" then return "\\r" end
    return "\\t"
  end)
end

local function show(value: any): string
  if type(value) == "string" then return "\"" .. value .. "\"" end
  return tostring(value)
end

local function label(where: string | nil): string
  local base: string = currentCase
  if where ~= nil then base = base .. " / " .. where end
  return base
end

local function fail(message: string): void
  error(message, 0)
end

local function eq(actual: any, expected: any, where: string | nil): void
  checks = checks + 1
  if actual ~= expected then
    fail(label(where) .. ": expected " .. show(expected) .. ", got " .. show(actual))
  end
end

local function ok(cond: boolean, where: string | nil): void
  checks = checks + 1
  if not cond then
    fail(label(where) .. ": expected true")
  end
end

--- Byte-string equality (same as `eq`, names the intent for binary data).
local function eqBytes(actual: string, expected: string, where: string | nil): void
  eq(actual, expected, where)
end

--- Asserts that `fn` raises; returns the error message ("" when it did not).
local function raises(fn: () -> void, where: string | nil): string
  checks = checks + 1
  local okCall, err = pcall(fn)
  if okCall then
    fail(label(where) .. ": expected an error")
  end
  return tostring(err)
end

--- Fails unconditionally (for "should not reach here" branches).
local function abort(message: string): void
  checks = checks + 1
  fail(label(nil) .. ": " .. message)
end

local function configure(userFilter: string, jsonOutput: boolean, wantModule: string, wantCase: string): void
  filter = userFilter
  json = jsonOutput
  moduleExact = wantModule
  caseExact = wantCase
end

local function matches(module: string, caseName: string): boolean
  if moduleExact ~= "" and module ~= moduleExact then return false end
  if caseExact ~= "" and caseName ~= caseExact then return false end
  if filter == "" then return true end
  if string.find(module, filter, 1, true) ~= nil then return true end
  return string.find(caseName, filter, 1, true) ~= nil
end

local function emitCase(name: string, good: boolean, message: string): void
  if json then
    print(string.format(
      "{\"module\":\"%s\",\"case\":\"%s\",\"ok\":%s,\"message\":\"%s\"}",
      jsonEscape(currentModule), jsonEscape(name), tostring(good), jsonEscape(message)
    ))
  elseif good then
    print("  ✓ " .. currentModule .. " / " .. name)
  else
    print("  ✗ " .. currentModule .. " / " .. name)
    print("      " .. message)
  end
end

--- Runs every function-valued entry of one module's returned table.
local function run(moduleName: string, suite: any): void
  currentModule = moduleName
  local names: string[] = {}
  for name, value in pairs(suite) do
    if type(value) == "function" then
      table.insert(names, name as string)
    end
  end
  table.sort(names)
  for i: number = 1, #names do
    local name: string = names[i]
    if matches(currentModule, name) then
      currentCase = name
      local okCall, err = pcall(suite[name])
      if okCall then
        passedCases = passedCases + 1
        emitCase(name, true, "")
      else
        failedCases = failedCases + 1
        local message: string = tostring(err)
        table.insert(failures, { module = currentModule, case = name, message = message })
        emitCase(name, false, message)
      end
    end
  end
end

--- Prints the summary and returns the failed-case count (exit code hint).
local function finish(): number
  if json then
    print(string.format(
      "{\"summary\":true,\"passed\":%d,\"failed\":%d,\"checks\":%d}",
      passedCases, failedCases, checks
    ))
  else
    print("")
    if failedCases == 0 then
      print(string.format("%d passed, 0 failed (%d checks)", passedCases, checks))
    else
      print(string.format("%d passed, %d failed (%d checks)", passedCases, failedCases, checks))
      for i: number = 1, #failures do
        local f: Failure = failures[i]
        print("  ✗ " .. f.module .. " / " .. f.case)
      end
    end
  end
  return failedCases
end

return {
  configure = configure,
  run = run,
  finish = finish,
  eq = eq,
  eqBytes = eqBytes,
  ok = ok,
  raises = raises,
  fail = abort,
}
