-- TypeLua debug agent: runs an emitted Lua program under a line/call hook and
-- talks to `tlua dap` over stderr (prefix `##tlua-dap `), leaving stdout for
-- the program's own output.
--
-- Config (dofile(arg[1])):
--   entry       absolute path of the emitted entry
--   maps        [luaFile][genLine] = { sourceFile, sourceLine }
--   breakpoints [luaFile][genLine] = true
--   stopOnEntry boolean
--
-- Commands (stdin, tab separated):
--   continue | next | stepin | stepout | quit
--   stack <id>
--   vars <id> <level>
--   bp <luaFile> <line,line,...>

local cfg = dofile(arg[1])

local maps = cfg.maps or {}
local bp = {}

local function esc(s)
  return (string.gsub(s, '[%z\1-\31\\"]', function(c)
    if c == '"' then return '\\"' end
    if c == "\\" then return "\\\\" end
    if c == "\n" then return "\\n" end
    if c == "\r" then return "\\r" end
    if c == "\t" then return "\\t" end
    return string.format("\\u%04x", string.byte(c))
  end))
end

local function encode(v)
  local t = type(v)
  if t == "string" then return '"' .. esc(v) .. '"' end
  if t == "number" or t == "boolean" then return tostring(v) end
  if t == "nil" then return "null" end
  if t == "table" then
    local n = #v
    if n > 0 then
      local parts = {}
      for i = 1, n do parts[i] = encode(v[i]) end
      return "[" .. table.concat(parts, ",") .. "]"
    end
    local parts = {}
    for k, val in pairs(v) do
      parts[#parts + 1] = encode(tostring(k)) .. ":" .. encode(val)
    end
    if #parts == 0 then return "[]" end
    return "{" .. table.concat(parts, ",") .. "}"
  end
  return "null"
end

local function emit(obj)
  io.stderr:write("##tlua-dap " .. encode(obj) .. "\n")
  io.stderr:flush()
end

local function stripSource(s)
  if type(s) ~= "string" then return "" end
  if string.sub(s, 1, 1) == "@" then return string.sub(s, 2) end
  return s
end

local function mapped(source, line)
  local byLine = maps[source]
  if byLine == nil then return nil end
  return byLine[line]
end

local function isBreakpoint(source, line)
  local set = bp[source]
  return set ~= nil and set[line] == true
end

for file, lines in pairs(cfg.breakpoints or {}) do
  local set = {}
  for line, on in pairs(lines) do
    if on then set[line] = true end
  end
  bp[file] = set
end

local depth = 1
local stepping = "continue"
if cfg.stopOnEntry then stepping = "in" end
local stepDepth = 0
local stepFile = ""
local stepLine = 0

local function readCommand()
  local line = io.read("*l")
  if line == nil then return nil, nil end
  local tab = string.find(line, "\t", 1, true)
  if tab == nil then return line, "" end
  return string.sub(line, 1, tab - 1), string.sub(line, tab + 1)
end

local function sendStack(id)
  local frames = {}
  local level = 2
  while level < 200 do
    local info = debug.getinfo(level, "Sln")
    if info == nil then break end
    local source = stripSource(info.source)
    local pos = mapped(source, info.currentline)
    if pos ~= nil then
      frames[#frames + 1] = {
        id = level,
        name = info.name or ("frame " .. tostring(level)),
        file = pos[1],
        line = pos[2],
      }
    end
    level = level + 1
  end
  emit({ type = "response", id = id, ok = true, frames = frames })
end

local function sendVars(id, level)
  local vars = {}
  local i = 1
  while true do
    local name, value = debug.getlocal(level, i)
    if name == nil then break end
    if string.sub(name, 1, 1) ~= "(" then
      vars[#vars + 1] = { name = name, value = tostring(value), type = type(value) }
    end
    i = i + 1
  end
  local j = -1
  while true do
    local name, value = debug.getlocal(level, j)
    if name == nil then break end
    vars[#vars + 1] = { name = name, value = tostring(value), type = type(value) }
    j = j - 1
  end
  emit({ type = "response", id = id, ok = true, vars = vars })
end

local function stop(reason, info)
  local source = stripSource(info.source)
  local pos = mapped(source, info.currentline) or { source, info.currentline }
  stepDepth = depth
  stepFile = pos[1]
  stepLine = pos[2]
  stepping = "continue"
  emit({ type = "stopped", reason = reason, file = pos[1], line = pos[2] })

  while true do
    local kind, rest = readCommand()
    if kind == nil then os.exit(0) end
    if kind == "continue" then
      stepping = "continue"
      return
    elseif kind == "next" then
      stepping = "over"
      return
    elseif kind == "stepin" then
      stepping = "in"
      return
    elseif kind == "stepout" then
      stepping = "out"
      return
    elseif kind == "stack" then
      sendStack(tonumber(rest) or 0)
    elseif kind == "vars" then
      local idText, levelText = string.match(rest, "^(%S+)%s+(%S+)$")
      sendVars(tonumber(idText) or 0, tonumber(levelText) or 2)
    elseif kind == "bp" then
      local file, lines = string.match(rest, "^(.-)\t(.*)$")
      if file ~= nil then
        local set = {}
        for n in string.gmatch(lines, "%d+") do set[tonumber(n)] = true end
        bp[file] = set
      end
    elseif kind == "quit" then
      os.exit(0)
    end
  end
end

local function hook(ev, line)
  if ev == "call" then
    depth = depth + 1
    return
  end
  if ev == "return" then
    depth = depth - 1
    if depth < 1 then depth = 1 end
    return
  end
  local info = debug.getinfo(2, "Sln")
  if info == nil then return end
  local source = stripSource(info.source)
  local pos = mapped(source, info.currentline)
  if pos == nil then return end

  if isBreakpoint(source, info.currentline) then
    stop("breakpoint", info)
    return
  end

  if stepping == "in" then
    if pos[1] ~= stepFile or pos[2] ~= stepLine or depth > stepDepth then
      stop("step", info)
    end
  elseif stepping == "over" then
    if depth <= stepDepth and (pos[1] ~= stepFile or pos[2] ~= stepLine) then
      stop("step", info)
    end
  elseif stepping == "out" then
    if depth < stepDepth then
      stop("step", info)
    end
  end
end

debug.sethook(hook, "lcr", 0)

local chunk, loadErr = loadfile(cfg.entry)
if chunk == nil then
  emit({ type = "error", message = "cannot load " .. tostring(cfg.entry) .. ": " .. tostring(loadErr) })
  os.exit(1)
end

local ok, runErr = xpcall(chunk, debug.traceback)
debug.sethook()
if not ok then
  emit({ type = "error", message = tostring(runErr) })
  os.exit(1)
end
emit({ type = "exited", code = 0 })
os.exit(0)
