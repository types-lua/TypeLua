package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/typelua/typelua/internal/compiler"
	"github.com/typelua/typelua/internal/config"
	"github.com/typelua/typelua/internal/lsp"
)

// Version is the compiler version printed by `tlua version`.
// Release builds inject the git tag via:
//   go build -ldflags "-X main.Version=<tag without leading v>" ./cmd/tlua
// Local builds keep the fallback below.
var Version = "0.1.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return fmt.Errorf("no command")
	}
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Printf("tlua v%s\n", Version)
		return nil
	case "help", "--help", "-h":
		usage()
		return nil
	case "init":
		return cmdInit(args[1:])
	case "mod":
		return cmdMod(args[1:])
	case "check":
		return cmdCheck(args[1:])
	case "test":
		return cmdTest(args[1:])
	case "build":
		return cmdBuild(args[1:])
	case "run":
		return cmdRun(args[1:])
	case "watch":
		return cmdWatch(args[1:])
	case "bindgen":
		return cmdBindgen(args[1:])
	case "lsp":
		os.Exit(lsp.Run(os.Stdin, os.Stdout, Version))
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Println(`tlua - typed Lua compiler (TypeScript for Lua)

Usage:
  tlua init [--dir .]                 create tlua.json + example
  tlua mod init [--dir .] [--name n]  create tlua.toml package manifest
  tlua mod get <path[@query]>...      add/update deps, resolve, download, lock
  tlua mod fetch                       resolve + download all locked modules
  tlua mod vendor                      copy locked sources into tlua_modules/
  tlua mod list [--indirect]           list locked modules (direct by default)
  tlua mod graph                       print the locked dependency graph
  tlua test [files...] [--filter text] [--format text|json] [--lua BIN] [--target 5.4]
  tlua check [files...] [--format text|json] [--watch] [-w] [--pollInterval ms] [--debounce ms]
   tlua build [files...] [--outDir dist] [--target 5.4] [--treeShake] [--format text|json] [--watch] [-w]
   tlua run <entry.tl> [-- args...] [--check] [--format text|json] [--lua BIN] [--target 5.4]
  tlua watch [files...] [--outDir dist] [--target 5.4] [--treeShake]
  tlua bindgen --header foo.h --out types/foo.d.tl [--shim foo.tl] [--lib CLib] [--so libfoo.so]
  tlua lsp                          language server over stdio
  tlua version                        print version`)
}

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		return err
	}
	cfgPath := filepath.Join(*dir, "tlua.json")
	if _, err := os.Stat(cfgPath); err == nil {
		return fmt.Errorf("tlua.json already exists at %s", cfgPath)
	}
	cfg := `{
  "compilerOptions": {
    "target": "5.4",
    "strict": true,
    "strictNilChecks": true,
    "noImplicitAny": false,
    "rootDir": ".",
    "outDir": "dist"
  },
  "include": ["**/*.tl"],
  "exclude": ["dist"]
}
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		return err
	}
	exPath := filepath.Join(*dir, "main.tl")
	if _, err := os.Stat(exPath); os.IsNotExist(err) {
		ex := `--- TypeLua example: like TypeScript for Lua.
type Point = { x: number, y: number }

local function dist(a: Point, b: Point): number
    local dx: number = a.x - b.x
    local dy: number = a.y - b.y
    return dx * dx + dy * dy
end

local p: Point = { x = 1, y = 2 }
print(dist(p, { x = 4, y = 6 }))
`
		_ = os.WriteFile(exPath, []byte(ex), 0o644)
	}
	fmt.Printf("initialized at %s\n", cfgPath)
	return nil
}

func loadCfgFor(paths []string, outDir, target string, sourceMap, treeShake bool) (*config.Config, string, error) {
	cwd, _ := os.Getwd()
	cfgPath := config.FindUp(cwd)
	var cfg *config.Config
	if cfgPath != "" {
		c, err := config.Load(cfgPath)
		if err != nil {
			return nil, "", fmt.Errorf("bad %s: %v", cfgPath, err)
		}
		cfg = c
	} else {
		cfg = config.Default(cwd)
	}
	if len(paths) > 0 {
		cfg.Files = paths
	}
	if outDir != "" {
		cfg.CompilerOptions.OutDir = outDir
	}
	if target != "" {
		if target != "5.4" && target != "5.1" {
			return nil, "", fmt.Errorf("invalid --target %q (want 5.4 or 5.1)", target)
		}
		cfg.CompilerOptions.Target = target
	}
	if sourceMap {
		cfg.CompilerOptions.SourceMap = true
	}
	if treeShake {
		cfg.CompilerOptions.TreeShake = true
	}
	return cfg, cfgPath, nil
}

func cmdCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	var wf watchFlags
	bindWatchFlags(fs, &wf)
	format := fs.String("format", "text", "diagnostic output: text or json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *format != "text" && *format != "json" {
		return fmt.Errorf("invalid --format %q (want text or json)", *format)
	}
	resolveWatchFlags(&wf)
	cfg, cfgPath, err := loadCfgFor(fs.Args(), "", "", false, false)
	if err != nil {
		return err
	}
	if wf.enabled {
		return runWatch(modeCheck, cfg, cfgPath, cliOverrides{paths: fs.Args()}, wf)
	}
	p := compiler.NewProgram(cfg)
	if err := p.Load(); err != nil {
		return err
	}
	p.Check()
	if len(p.Diags) > 0 {
		if *format == "json" {
			fmt.Print(p.FormatDiagsJSON())
		} else {
			fmt.Print(p.FormatDiags())
		}
	}
	if p.HasErrors() {
		return fmt.Errorf("found %d error(s)", len(p.Diags))
	}
	fmt.Printf("checked %d file(s): no errors\n", len(p.Files))
	return nil
}

func cmdBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	outDir := fs.String("outDir", "", "output directory")
	target := fs.String("target", "", "lua target: 5.4 or 5.1")
	sourceMap := fs.Bool("sourceMap", false, "emit .lua.map source maps")
	treeShake := fs.Bool("treeShake", false, "drop zero-reference pure local functions")
	format := fs.String("format", "text", "diagnostic output: text or json")
	var wf watchFlags
	bindWatchFlags(fs, &wf)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *format != "text" && *format != "json" {
		return fmt.Errorf("invalid --format %q (want text or json)", *format)
	}
	resolveWatchFlags(&wf)
	cfg, cfgPath, err := loadCfgFor(fs.Args(), *outDir, *target, *sourceMap, *treeShake)
	if err != nil {
		return err
	}
	if wf.enabled {
		return runWatch(modeBuild, cfg, cfgPath, cliOverrides{
			paths:        fs.Args(),
			outDir:       *outDir,
			target:       *target,
			sourceMap:    *sourceMap,
			treeShake:    *treeShake,
			hasOutDir:    *outDir != "",
			hasTarget:    *target != "",
			hasSrcMap:    *sourceMap,
			hasTreeShake: *treeShake,
		}, wf)
	}
	p := compiler.NewProgram(cfg)
	if err := p.Load(); err != nil {
		return err
	}
	if len(p.Files) == 0 {
		return fmt.Errorf("no .tl files found")
	}
	p.Check()
	emitted, err := p.Emit()
	if err != nil {
		return err
	}
	if len(p.Diags) > 0 {
		if *format == "json" {
			fmt.Print(p.FormatDiagsJSON())
		} else {
			fmt.Print(p.FormatDiags())
		}
	}
	for _, f := range emitted {
		fmt.Printf("emitted %s\n", f)
	}
	if p.HasErrors() {
		return fmt.Errorf("build completed with %d error(s)", len(p.Diags))
	}
	fmt.Printf("built %d file(s)\n", len(emitted))
	return nil
}
