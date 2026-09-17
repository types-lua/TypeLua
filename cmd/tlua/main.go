package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/typelua/typelua/internal/compiler"
	"github.com/typelua/typelua/internal/config"
)

const Version = "0.1.0"

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
	case "check":
		return cmdCheck(args[1:])
	case "build":
		return cmdBuild(args[1:])
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Println(`tlua - typed Lua compiler (TypeScript for Lua)

Usage:
  tlua init [--dir .]                 create tlua.json + example
  tlua check [files...]               type-check only
  tlua build [files...] [--outDir dist] [--target 5.4]
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

func loadCfgFor(paths []string, outDir, target string) (*config.Config, error) {
	cwd, _ := os.Getwd()
	cfgPath := config.FindUp(cwd)
	var cfg *config.Config
	if cfgPath != "" {
		c, err := config.Load(cfgPath)
		if err != nil {
			return nil, fmt.Errorf("bad %s: %v", cfgPath, err)
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
			return nil, fmt.Errorf("invalid --target %q (want 5.4 or 5.1)", target)
		}
		cfg.CompilerOptions.Target = target
	}
	return cfg, nil
}

func cmdCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadCfgFor(fs.Args(), "", "")
	if err != nil {
		return err
	}
	p := compiler.NewProgram(cfg)
	if err := p.Load(); err != nil {
		return err
	}
	p.Check()
	if len(p.Diags) > 0 {
		fmt.Print(p.FormatDiags())
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
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadCfgFor(fs.Args(), *outDir, *target)
	if err != nil {
		return err
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
		fmt.Print(p.FormatDiags())
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
