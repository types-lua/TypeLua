package main

import (
	"bytes"
	_ "embed"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	golua "github.com/arnodel/golua/lib"
	"github.com/arnodel/golua/lib/debuglib"
	rt "github.com/arnodel/golua/runtime"
	"github.com/typelua/typelua/internal/compiler"
)

// tlua test: discover test files, type-check them, then run their named
// cases in the embedded interpreter.
//
// Convention (zero configuration):
//   - files under tests/ or test/
//   - files whose base name ends with _test.tl or .test.tl
//
// Each test file returns a table of case functions:
//
//	import T from "tlua_test"
//	return {
//	  ["adds numbers"] = function(): void T.eq(1+1, 2) end,
//	}
//
// The helper module is injected at <project>/tlua_modules/tlua_test.tl, so
// the import resolves for the checker and the runtime without installation.

//go:embed tlua_test.tl
var tluaTestSource string

// testRunnerSource is written as <project>/__tlua_test_main.tl and requires
// every discovered test module through its dotted project path.
const testRunnerHeader = `local T = require("tlua_test")
T.configure(%s, %s)
`

const testRunnerFooter = `
local failed = T.finish()
print("##tlua-test-result " .. tostring(failed))
`

func cmdTest(args []string) error {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	filter := fs.String("filter", "", "only run cases whose module or case name contains this text")
	format := fs.String("format", "text", "output format: text or json")
	luaBin := fs.String("lua", "", "external lua interpreter (e.g. lua5.4, luajit); default: embedded golua")
	target := fs.String("target", "", "lua target: 5.4 or 5.1 (default: tlua.json or 5.4)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *format != "text" && *format != "json" {
		return fmt.Errorf("invalid --format %q (want text or json)", *format)
	}
	if *target != "" && *target != "5.4" && *target != "5.1" {
		return fmt.Errorf("invalid --target %q (want 5.4 or 5.1)", *target)
	}

	cfg, _, err := loadCfgFor(nil, "", *target, false, false)
	if err != nil {
		return err
	}

	files, err := discoverTests(cfg.Dir, fs.Args())
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no test files found (looked for tests/**/*.tl, *_test.tl, *.test.tl)")
	}

	tmp, err := os.MkdirTemp("", "tlua-test-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	cfg.CompilerOptions.OutDir = tmp
	p := compiler.NewProgram(cfg)
	if err := p.Load(); err != nil {
		return err
	}
	// Load the whole project so library sources are checked and emitted
	// alongside the tests; append any test outside include/exclude.
	for _, f := range files {
		if !programHasFile(p, f) {
			data, err := os.ReadFile(f)
			if err != nil {
				return fmt.Errorf("cannot read test file %s: %v", f, err)
			}
			p.AddVirtual(f, string(data))
		}
	}
	p.AddVirtual(filepath.Join(cfg.Dir, "tlua_modules", "tlua_test.tl"), tluaTestSource)
	p.AddVirtual(filepath.Join(cfg.Dir, "__tlua_test_main.tl"),
		buildTestRunner(cfg.Dir, files, *filter, *format == "json"))

	p.Check()
	if len(p.Diags) > 0 {
		if *format == "json" {
			fmt.Print(p.FormatDiagsJSON())
		} else {
			fmt.Print(p.FormatDiags())
		}
	}
	if p.HasErrors() {
		return fmt.Errorf("found %d error(s) in test files", len(p.Diags))
	}
	if _, err := p.Emit(); err != nil {
		return err
	}

	entry, ok := compiler.OutputForFile(cfg, filepath.Join(cfg.Dir, "__tlua_test_main.tl"))
	if !ok {
		return fmt.Errorf("cannot map the test runner to an output")
	}
	src, err := os.ReadFile(entry)
	if err != nil {
		return fmt.Errorf("cannot read the emitted test runner: %v", err)
	}

	if *format != "json" {
		fmt.Printf("tlua test · %d file(s)\n", len(files))
	}
	var out string
	var failed int
	if *luaBin != "" {
		out, failed, err = runTestsExternal(*luaBin, entry)
	} else {
		out, failed, err = runTestsEmbedded(entry, src, tmp, []string{*filter, *format})
	}
	for _, line := range strings.SplitAfter(out, "\n") {
		if line == "" || strings.HasPrefix(line, "##tlua-test-result ") {
			continue
		}
		fmt.Print(line)
	}
	if err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("%d test case(s) failed", failed)
	}
	return nil
}

// programHasFile reports whether the loaded program already contains path
// (compiler paths may be relative to the config directory).
func programHasFile(p *compiler.Program, path string) bool {
	want, err := filepath.Abs(path)
	if err != nil {
		want = path
	}
	want = filepath.Clean(want)
	for _, sf := range p.Files {
		got, err := filepath.Abs(sf.Path)
		if err != nil {
			got = sf.Path
		}
		if filepath.Clean(got) == want {
			return true
		}
	}
	return false
}

// discoverTests expands explicit arguments (files or directories) or applies
// the zero-config convention. Results are absolute, sorted and deduplicated.
func discoverTests(root string, args []string) ([]string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	add := func(path string) {
		abs, err := filepath.Abs(path)
		if err != nil || seen[abs] {
			return
		}
		seen[abs] = true
		out = append(out, abs)
	}

	if len(args) > 0 {
		for _, arg := range args {
			abs, err := filepath.Abs(arg)
			if err != nil {
				return nil, err
			}
			info, err := os.Stat(abs)
			if err != nil {
				return nil, fmt.Errorf("test path %s: %v", arg, err)
			}
			if info.IsDir() {
				if err := walkTests(abs, add); err != nil {
					return nil, err
				}
			} else {
				add(abs)
			}
		}
		sort.Strings(out)
		return out, nil
	}

	if err := walkTests(rootAbs, add); err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// walkTests walks dir and adds files that match the test convention.
func walkTests(dir string, add func(string)) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		name := info.Name()
		if info.IsDir() {
			switch name {
			case ".git", "node_modules", "dist", "tlua_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".tl") || strings.HasSuffix(name, ".d.tl") {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			rel = name
		}
		slash := filepath.ToSlash(rel)
		underTests := strings.HasPrefix(slash, "tests/") || strings.HasPrefix(slash, "test/")
		named := strings.HasSuffix(name, "_test.tl") || strings.HasSuffix(name, ".test.tl")
		if underTests || named {
			add(path)
		}
		return nil
	})
}

// buildTestRunner emits the small TypeLua entry that requires every test
// module and hands it to the helper. Module names are project-relative
// dotted paths, which the runtime resolves through package.path.
func buildTestRunner(root string, files []string, filter string, jsonOut bool) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(testRunnerHeader, luaQuote(filter), strconv.FormatBool(jsonOut)))
	for _, f := range files {
		rel, err := filepath.Rel(root, f)
		if err != nil {
			rel = filepath.Base(f)
		}
		rel = filepath.ToSlash(rel)
		rel = strings.TrimSuffix(rel, ".tl")
		module := strings.ReplaceAll(rel, "/", ".")
		fmt.Fprintf(&sb, "do\n  local __m = require(%q)\n  T.run(%q, __m)\nend\n", module, module)
	}
	sb.WriteString(testRunnerFooter)
	return sb.String()
}

// runTestsExternal executes the emitted runner with an external interpreter,
// capturing output so the failure marker works the same way as in-process.
func runTestsExternal(bin, entry string) (string, int, error) {
	path, err := exec.LookPath(bin)
	if err != nil {
		return "", 0, fmt.Errorf("lua interpreter %q not found: %v", bin, err)
	}
	cmd := exec.Command(path, entry)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	runErr := cmd.Run()
	out := buf.String()
	failed := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "##tlua-test-result ") {
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "##tlua-test-result ")))
			if err == nil {
				failed = n
			}
		}
	}
	if runErr != nil {
		if failed == 0 {
			return out, 0, fmt.Errorf("%s failed: %v", bin, runErr)
		}
	}
	return out, failed, nil
}

// runTestsEmbedded executes the emitted runner in-process and returns its
// captured output plus the failed-case count. The marker line keeps the
// count machine-readable without relying on os.exit (which would bypass
// temp-dir cleanup).
func runTestsEmbedded(chunkName string, src []byte, tmp string, scriptArgs []string) (string, int, error) {
	var buf bytes.Buffer
	r := rt.New(&buf)
	r.PushContext(rt.RuntimeContextDef{MessageHandler: debuglib.Traceback})
	cleanup := golua.LoadAll(r)
	defer cleanup()
	defer r.Close(nil)

	argTable := rt.NewTable()
	r.SetTable(argTable, rt.IntValue(0), rt.StringValue(chunkName))
	argVals := make([]rt.Value, len(scriptArgs))
	for i, a := range scriptArgs {
		v := rt.StringValue(a)
		r.SetTable(argTable, rt.IntValue(int64(i+1)), v)
		argVals[i] = v
	}
	r.SetTable(r.GlobalEnv(), rt.StringValue("arg"), rt.TableValue(argTable))

	setup := fmt.Sprintf("package.path = %s .. \";\" .. (package.path or \"\")",
		luaQuote(tmp+"/?.lua;"+tmp+"/?/init.lua"))
	if err := doChunk(r, "<tlua-test-setup>", []byte(setup), nil); err != nil {
		return buf.String(), 0, err
	}
	if err := doChunk(r, chunkName, src, argVals); err != nil {
		return buf.String(), 0, err
	}

	failed := 0
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.HasPrefix(line, "##tlua-test-result ") {
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "##tlua-test-result ")))
			if err == nil {
				failed = n
			}
		}
	}
	return buf.String(), failed, nil
}
