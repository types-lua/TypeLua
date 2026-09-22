package compiler

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/typelua/typelua/internal/checker"
	"github.com/typelua/typelua/internal/mod"
	"github.com/typelua/typelua/internal/sourcemap"
)

// Runtime module bootstrap: compiled programs that require modules must
// run without manual LUA_PATH setup. Every emitted file of such a program
// gets this prelude (once per process, guarded). It does two things:
//
//  1. Prepend <script-dir>/?.lua and <script-dir>/?/init.lua to
//     package.path, so requires resolving to sibling outputs just work.
//  2. Append a package.searchers (5.2+) / package.loaders (5.1) entry that
//     serves dotted vendor layouts (<script-dir>/tlua_modules/<dotted
//     path>.lua and .../init.lua), which plain path templates cannot reach
//     (Lua converts dots to slashes before template substitution).
//
// Dotted names expand to candidates the same way the compiler resolves
// them (all-slashes first, then progressively dot-preserving), and the
// searcher runs after the standard loaders, so in-project files keep
// precedence over vendored ones — exactly like check-time resolution.
//
// The prelude is Lua 5.1-safe (no goto, no //, no bitwise ops, no <const>)
// and defensive: missing debug/package libraries degrade instead of
// erroring. User LUA_PATH entries keep working: we prepend/append, never
// replace. The `__tl_` prefix is reserved (see SPEC §12); never use it.
const modPathPrelude = `if not __tl_modpath_done then
  __tl_modpath_done = true
  local __tl_src = ""
  if debug and debug.getinfo then
    -- Direct call (NOT via pcall: that would shift getinfo one level up
    -- into pcall's C frame). Level 1 here is always this chunk itself.
    local __tl_info = debug.getinfo(1, "S")
    if type(__tl_info) == "table" and type(__tl_info.source) == "string" then
      __tl_src = __tl_info.source
    end
  end
  if __tl_src:sub(1, 1) == "@" then
    __tl_src = __tl_src:sub(2)
  end
  local __tl_dir = __tl_src:match("^(.*[/\\])") or "./"
  if package then
    package.path = __tl_dir .. "?.lua;" .. __tl_dir .. "?/init.lua;" .. (package.path or "")
    local __tl_searchers = package.searchers or package.loaders
    if __tl_searchers then
      table.insert(__tl_searchers, function(__tl_name)
        local __tl_seg = {}
        for __tl_s in __tl_name:gmatch("[^%.]+") do
          __tl_seg[#__tl_seg + 1] = __tl_s
        end
        local __tl_tried = ""
        for __tl_k = 1, #__tl_seg do
          local __tl_c = table.concat(__tl_seg, ".", 1, __tl_k)
          if __tl_k < #__tl_seg then
            __tl_c = __tl_c .. "/" .. table.concat(__tl_seg, "/", __tl_k + 1, #__tl_seg)
          end
          for __tl_j = 1, 2 do
            local __tl_base = __tl_dir
            if __tl_j == 2 then
              __tl_base = __tl_dir .. "tlua_modules/"
            end
            local __tl_f = __tl_base .. __tl_c .. ".lua"
            local __tl_ld = loadfile(__tl_f)
            if __tl_ld then
              return __tl_ld
            end
            __tl_tried = __tl_tried .. "\n\tno file '" .. __tl_f .. "'"
            __tl_f = __tl_base .. __tl_c .. "/init.lua"
            __tl_ld = loadfile(__tl_f)
            if __tl_ld then
              return __tl_ld
            end
            __tl_tried = __tl_tried .. "\n\tno file '" .. __tl_f .. "'"
          end
        end
        return __tl_tried
      end)
    end
  end
end
`

// preludeLines is the generated-line shift the prelude causes (1-based
// mappings move down by this much). Counted, not hardcoded.
func preludeLines() int {
	return strings.Count(modPathPrelude, "\n")
}

// NeedsModPath reports whether the program needs the runtime bootstrap:
// any file requires a module, or any file is itself vendored.
func NeedsModPath(p *Program) bool {
	vendor := filepath.Clean(filepath.Join(p.Cfg.Dir, mod.VendorDir)) + string(os.PathSeparator)
	for _, sf := range p.Files {
		if sf.IsDecl || sf.File == nil {
			continue
		}
		if len(checker.RequiredModules(sf.File)) > 0 {
			return true
		}
		if strings.HasPrefix(filepath.Clean(sf.Path)+string(os.PathSeparator), vendor) {
			return true
		}
	}
	return false
}

// PrependPrelude prepends the module bootstrap to emitted Lua and shifts
// source-map generated lines down accordingly. Mappings are copied, never
// mutated in place.
func PrependPrelude(out string, mappings []sourcemap.Mapping) (string, []sourcemap.Mapping) {
	shift := preludeLines()
	moved := make([]sourcemap.Mapping, len(mappings))
	for i, m := range mappings {
		m.GenLine += shift
		moved[i] = m
	}
	return modPathPrelude + out, moved
}
