package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	golua "github.com/arnodel/golua/lib"
	"github.com/arnodel/golua/lib/debuglib"
	rt "github.com/arnodel/golua/runtime"
	"github.com/typelua/typelua/internal/compiler"
)

// cmdRun implements `tlua run`: type-erase + execute without leaving
// build artifacts behind.
//
// Defaults mirror `tlua build` (target 5.4 unless tlua.json/--target says
// otherwise) but type checking is OFF by default (--no-check semantics):
// only parse errors block execution. Pass --check for build-like strictness.
//
// Two runtimes, switched by flag:
//   - embedded golua (default): zero external dependencies, pure Lua only.
//     FFI (require "ffi") and C modules (.so/.dll/.a) are NOT supported
//     here — golua has no Lua C-module loader by design.
//   - external (--lua <bin>): emit to a temp dir and exec the interpreter
//     (lua5.4, luajit, ...). The emitted bootstrap prelude sets
//     package.path/cpath from the script directory, so this works the same
//     as `lua dist/main.lua`. Use this for ffi/C extensions.
//
// runOptions carries the flags shared by `tlua run` and `tlua example`.
type runOptions struct {
	check  bool
	luaBin string
	target string
	format string
}

// runEntry emits and executes one entry file. Both commands share it so
// example runs behave exactly like script runs.
func runEntry(entryArg string, scriptArgs []string, opt runOptions, virtual map[string]string) error {
	entryAbs, err := filepath.Abs(entryArg)
	if err != nil {
		return err
	}
	if strings.HasSuffix(entryAbs, ".d.tl") {
		return fmt.Errorf("cannot run declaration file %s", entryArg)
	}

	tmp, err := os.MkdirTemp("", "tlua-run-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	// Load the full project (like build with no file args) so sibling
	// requires emit alongside the entry and resolve at runtime. Standalone
	// files outside any project fall back to a single-file program.
	cfg, cfgPath, err := loadCfgFor(nil, tmp, opt.target, false, false)
	if err != nil {
		return err
	}
	p := compiler.NewProgram(cfg)
	if err := p.Load(); err != nil {
		return err
	}
	for path, content := range virtual {
		p.AddVirtual(path, content)
	}
	if cfgPath == "" || !hasSource(p, entryAbs) {
		cfg, _, err = loadCfgFor([]string{entryAbs}, tmp, opt.target, false, false)
		if err != nil {
			return err
		}
		p = compiler.NewProgram(cfg)
		if err := p.Load(); err != nil {
			return err
		}
	}
	if len(p.Files) == 0 {
		return fmt.Errorf("no .tl files found")
	}
	if !hasSource(p, entryAbs) {
		return fmt.Errorf("entry %s not found (not covered by tlua.json include)", entryArg)
	}
	if opt.check {
		p.Check()
		if len(p.Diags) > 0 {
			if opt.format == "json" {
				fmt.Print(p.FormatDiagsJSON())
			} else {
				fmt.Print(p.FormatDiags())
			}
		}
		if p.HasErrors() {
			return fmt.Errorf("found %d error(s)", len(p.Diags))
		}
	} else if p.HasErrors() {
		// Parse errors alone block even --no-check runs: broken code
		// cannot be emitted, let alone executed.
		if opt.format == "json" {
			fmt.Print(p.FormatDiagsJSON())
		} else {
			fmt.Print(p.FormatDiags())
		}
		return fmt.Errorf("found %d error(s)", len(p.Diags))
	}
	if _, err := p.Emit(); err != nil {
		return err
	}
	entryLua, ok := compiler.OutputForFile(cfg, entryAbs)
	if !ok {
		return fmt.Errorf("cannot map entry %s to an output", entryArg)
	}
	src, err := os.ReadFile(entryLua)
	if err != nil {
		return fmt.Errorf("cannot read emitted entry: %v", err)
	}
	if opt.luaBin != "" {
		return runExternal(opt.luaBin, entryLua, scriptArgs)
	}
	return runEmbedded(entryLua, src, tmp, scriptArgs)
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	check := fs.Bool("check", false, "type-check before running (default: parse only)")
	luaBin := fs.String("lua", "", "external lua interpreter (e.g. lua5.4, luajit); default: embedded golua")
	target := fs.String("target", "", "lua target: 5.4 or 5.1 (default: tlua.json or 5.4, like build)")
	format := fs.String("format", "text", "diagnostic output: text or json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *format != "text" && *format != "json" {
		return fmt.Errorf("invalid --format %q (want text or json)", *format)
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("usage: tlua run <entry.tl> [-- args...] [--check] [--lua BIN] [--target 5.4|5.1]")
	}
	return runEntry(rest[0], rest[1:], runOptions{
		check:  *check,
		luaBin: *luaBin,
		target: *target,
		format: *format,
	}, nil)
}

func hasSource(p *compiler.Program, abs string) bool {
	clean := filepath.Clean(abs)
	for _, sf := range p.Files {
		if sf.IsDecl {
			continue
		}
		if fp, err := filepath.Abs(sf.Path); err == nil {
			if filepath.Clean(fp) == clean {
				return true
			}
		} else if filepath.Clean(sf.Path) == clean {
			return true
		}
	}
	return false
}

// runEmbedded executes emitted Lua in-process via golua.
func runEmbedded(chunkName string, src []byte, tmp string, scriptArgs []string) error {
	r := rt.New(os.Stdout)
	r.PushContext(rt.RuntimeContextDef{MessageHandler: debuglib.Traceback})
	cleanup := golua.LoadAll(r)
	defer cleanup()
	defer r.Close(nil)

	// arg[0] = script name, arg[1..] = script args (matches PUC Lua).
	argTable := rt.NewTable()
	r.SetTable(argTable, rt.IntValue(0), rt.StringValue(chunkName))
	argVals := make([]rt.Value, len(scriptArgs))
	for i, a := range scriptArgs {
		v := rt.StringValue(a)
		r.SetTable(argTable, rt.IntValue(int64(i+1)), v)
		argVals[i] = v
	}
	r.SetTable(r.GlobalEnv(), rt.StringValue("arg"), rt.TableValue(argTable))

	// Serve the temp-dir outputs (entry + sibling emits + vendored lua)
	// through the standard package.path mechanism.
	setup := fmt.Sprintf("package.path = %s .. \";\" .. (package.path or \"\")",
		luaQuote(tmp+"/?.lua;"+tmp+"/?/init.lua"))
	if err := doChunk(r, "<tlua-run-setup>", []byte(setup), nil); err != nil {
		return err
	}
	return doChunk(r, chunkName, src, argVals)
}

func doChunk(r *rt.Runtime, name string, src []byte, argVals []rt.Value) error {
	clos, err := r.LoadFromSourceOrCode(name, src, "bt", rt.TableValue(r.GlobalEnv()), true)
	if err != nil {
		return fmt.Errorf("error loading %s: %s", name, err)
	}
	if cerr := rt.Call(r.MainThread(), rt.FunctionValue(clos), argVals, rt.NewTerminationWith(nil, 0, false)); cerr != nil {
		return fmt.Errorf("!!! %s", cerr.Error())
	}
	return nil
}

// runExternal executes the emitted entry with an external interpreter.
// The emitted bootstrap prelude resolves sibling/vendor modules from the
// script directory, so no LUA_PATH setup is needed.
func runExternal(luaBin, entryLua string, scriptArgs []string) error {
	bin, err := exec.LookPath(luaBin)
	if err != nil {
		return fmt.Errorf("lua interpreter %q not found: %v", luaBin, err)
	}
	cmd := exec.Command(bin, append([]string{entryLua}, scriptArgs...)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("%s exited with code %d", luaBin, ee.ExitCode())
		}
		return err
	}
	return nil
}

// luaQuote renders s as a Lua double-quoted string literal.
func luaQuote(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, c := range []byte(s) {
		switch c {
		case '"':
			sb.WriteString("\\\"")
		case '\\':
			sb.WriteString("\\\\")
		case '\n':
			sb.WriteString("\\n")
		default:
			sb.WriteByte(c)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}
