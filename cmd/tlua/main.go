// Package main 是 tlua 命令行入口：编译、检查、运行、监视、包管理与 bindgen。

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

// Version 是 tlua CLI 的版本号。
const Version = "0.1.0"

func main() {
	// 设计者视角：main 薄到只剩 run+错打印+1，逻辑全下沉 run 供测。障碍是 lsp 子进程：
	// run 内 exit，不返。排查现场：退出码错先查 run。
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	// 设计者视角：run 是子命令总机——空/--help 走 usage，version 直印，余下沉各 cmd，
	// lsp 内 exit。障碍是未知命令：usage+错。候选：表驱 vs switch；选 switch 明。
	// 排查现场：新命令无响应先查此表。
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
	// 设计者视角：usage 一屏列全命令（help 为默认出口，见 run 空/错分支）。障碍是新增
	// 命令忘文档：改 run 必改此。排查现场：帮助旧先查此。
	fmt.Println(`tlua - typed Lua compiler (TypeScript for Lua)

Usage:
  tlua init [--dir .]                 create tlua.json + example
  tlua mod init [--dir .] [--name n]  create tlua.toml package manifest
  tlua mod get <path[@query]>...      add/update deps, resolve, download, lock
  tlua mod fetch                       resolve + download all locked modules
  tlua mod vendor                      copy locked sources into tlua_modules/
  tlua mod list [--indirect]           list locked modules (direct by default)
  tlua mod graph                       print the locked dependency graph
  tlua check [files...] [--watch] [-w] [--pollInterval ms] [--debounce ms]
   tlua build [files...] [--outDir dist] [--target 5.4] [--treeShake] [--watch] [-w]
   tlua run <entry.tl> [-- args...] [--check] [--lua BIN] [--target 5.4]
  tlua watch [files...] [--outDir dist] [--target 5.4] [--treeShake]
  tlua bindgen --header foo.h --out types/foo.d.tl [--shim foo.tl] [--lib CLib] [--so libfoo.so]
  tlua lsp                          language server over stdio
  tlua version                        print version`)
}

func cmdInit(args []string) error {
	// 设计者视角：init 写 tlua.json（strict 全开、root/out 为 . /dist）+ main.tl 示例，
	// 已存即拒（防覆）。障碍是重 init：错护。候选：覆 vs 拒；选拒。不变量：已存不碰。
	// 排查现场：init 无示例先查 main.tl 存在判。
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
	// 设计者视角：配置三阶——FindUp 找 tlua.json（有则 Load、无则 Default），files 覆，
	// CLI 覆（outDir/target 合法 5.4/5.1 方收，map/shake 开）。障碍是非法 target：
	// 早错。候选：CLI 默 vs 覆；选显覆。排查现场：配置不生效先查覆序。
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
	// 设计者视角：check=Load→Check→印诊→错数错（watch 旗转 runWatch）。障碍是零文件：
	// 新 Program 空诊仍报数。候选：错即停 vs 全印；选全印后错。排查现场：watch 未进
	// 先查 wf.enabled。
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	var wf watchFlags
	bindWatchFlags(fs, &wf)
	if err := fs.Parse(args); err != nil {
		return err
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
		fmt.Print(p.FormatDiags())
	}
	if p.HasErrors() {
		return fmt.Errorf("found %d error(s)", len(p.Diags))
	}
	fmt.Printf("checked %d file(s): no errors\n", len(p.Files))
	return nil
}

func cmdBuild(args []string) error {
	// 设计者视角：build=Load→空拒→Check→Emit→印诊+逐行 emitted→错仍错（watch 转增量）。
	// 障碍是空工程：`no .tl files` 早错；障碍是诊错仍发货（TS 义）：Emit 先于 HasErrors。
	// 候选：错停发 vs 先发；选先发。不变量：诊恒印。排查现场：空输出先查 files，错未发
	// 先查序。
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	outDir := fs.String("outDir", "", "output directory")
	target := fs.String("target", "", "lua target: 5.4 or 5.1")
	sourceMap := fs.Bool("sourceMap", false, "emit .lua.map source maps")
	treeShake := fs.Bool("treeShake", false, "drop zero-reference pure local functions")
	var wf watchFlags
	bindWatchFlags(fs, &wf)
	if err := fs.Parse(args); err != nil {
		return err
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
