package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/typelua/typelua/internal/mod"
)

func cmdMod(args []string) error {
	// 设计者视角：mod 二级总机——空指用法，余下沉 init/get/fetch/vendor/list/graph。
	// 障碍是二级 help：此处仅列名，细 help 各子命令。排查现场：未知子命令先查表。
	if len(args) == 0 {
		return fmt.Errorf("usage: tlua mod <init|get|fetch|vendor|list|graph>")
	}
	switch args[0] {
	case "init":
		return cmdModInit(args[1:])
	case "get":
		return cmdModGet(args[1:])
	case "fetch":
		return cmdModFetch(args[1:])
	case "vendor":
		return cmdModVendor(args[1:])
	case "list":
		return cmdModList(args[1:])
	case "graph":
		return cmdModGraph(args[1:])
	default:
		return fmt.Errorf("unknown mod command %q (want init|get|fetch|vendor|list|graph)", args[0])
	}
}

func modCache() (*mod.Cache, error) {
	// 设计者视角：DefaultDir+NewCache 一步备缓存（含离线旗）。障碍是家缺：错透。
	// 排查现场：缓存错先查 env。
	dir, err := mod.DefaultDir()
	if err != nil {
		return nil, err
	}
	return mod.NewCache(dir), nil
}

func cmdModInit(args []string) error {
	// 设计者视角：mod init 写 tlua.toml（名空取目录基），已存拒。障碍是 name 空：
	// Base 兜。排查现场：名错先查 Base。
	fs := flag.NewFlagSet("mod init", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	name := fs.String("name", "", "module name (default: directory base)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	abs, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return err
	}
	mp := filepath.Join(abs, mod.ManifestName)
	if _, err := os.Stat(mp); err == nil {
		return fmt.Errorf("%s already exists at %s", mod.ManifestName, mp)
	}
	nm := *name
	if nm == "" {
		nm = filepath.Base(abs)
	}
	m := mod.NewManifest(nm)
	if err := os.WriteFile(mp, []byte(mod.EncodeManifest(m)), 0o644); err != nil {
		return err
	}
	fmt.Printf("initialized %s\n", mp)
	return nil
}

func loadModProject() (*mod.Project, error) {
	// 设计者视角：cwd 上溯载项目（manifest 必+lock 可）。障碍是无项目：错指 init。
	// 排查现场：目录错先查 cwd。
	cwd, _ := os.Getwd()
	return mod.LoadProject(cwd)
}

func cmdModGet(args []string) error {
	// 设计者视角：get=快照旧 lock→mod.Get 解下→diff 打印（+ 新增、~ 升级，direct 标）。
	// 障碍是无 spec 全升：见 mod.Get。排查现场：未升级先查 spec。
	fs := flag.NewFlagSet("mod get", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := loadModProject()
	if err != nil {
		return err
	}
	cache, err := modCache()
	if err != nil {
		return err
	}
	before := map[string]mod.LockEntry{}
	if p.Lock != nil {
		for _, e := range p.Lock.Entries {
			before[e.Path] = e
		}
	}
	l, err := mod.Get(context.Background(), p, cache, fs.Args())
	if err != nil {
		return err
	}
	for _, e := range mod.SortedEntries(l) {
		old, ok := before[e.Path]
		mark := "(indirect)"
		if e.Direct {
			mark = "(direct)"
		}
		switch {
		case !ok:
			fmt.Printf("+ %s %s %s\n", e.Path, e.Version, mark)
		case old.Version != e.Version || old.Commit != e.Commit:
			fmt.Printf("~ %s %s -> %s %s\n", e.Path, old.Version, e.Version, mark)
		}
	}
	return nil
}

func cmdModFetch(args []string) error {
	// 设计者视角：fetch=重解+验 tag 不漂+下载（见 mod.Fetch），逐行 `= path ver`。
	// 障碍是多余参：拒。排查现场：漂移错先查 lock。
	fs := flag.NewFlagSet("mod fetch", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("usage: tlua mod fetch")
	}
	p, err := loadModProject()
	if err != nil {
		return err
	}
	cache, err := modCache()
	if err != nil {
		return err
	}
	l, err := mod.Fetch(context.Background(), p, cache)
	if err != nil {
		return err
	}
	for _, e := range mod.SortedEntries(l) {
		fmt.Printf("= %s %s\n", e.Path, e.Version)
	}
	return nil
}

func cmdModVendor(args []string) error {
	// 设计者视角：vendor 全量重铺 tlua_modules（见 Project.Vendor）。障碍是多余参拒。
	// 排查现场：vendor 残留先查全清。
	fs := flag.NewFlagSet("mod vendor", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("usage: tlua mod vendor")
	}
	p, err := loadModProject()
	if err != nil {
		return err
	}
	cache, err := modCache()
	if err != nil {
		return err
	}
	if err := p.Vendor(context.Background(), cache); err != nil {
		return err
	}
	fmt.Printf("vendored %d module(s) into %s/\n", len(p.Lock.Entries), mod.VendorDir)
	return nil
}

func cmdModList(args []string) error {
	// 设计者视角：list 按径印 lock（默认仅 direct，--indirect 全印）。障碍是无 lock：
	// 指 fetch。排查现场：空先查 lock。
	fs := flag.NewFlagSet("mod list", flag.ContinueOnError)
	indirect := fs.Bool("indirect", false, "include indirect dependencies")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("usage: tlua mod list [--indirect]")
	}
	p, err := loadModProject()
	if err != nil {
		return err
	}
	if p.Lock == nil {
		return fmt.Errorf("no %s (run `tlua mod fetch`)", mod.LockName)
	}
	for _, e := range mod.SortedEntries(p.Lock) {
		if !e.Direct && !*indirect {
			continue
		}
		if e.Direct {
			fmt.Printf("%s %s\n", e.Path, e.Version)
		} else {
			fmt.Printf("%s %s (indirect)\n", e.Path, e.Version)
		}
	}
	return nil
}

func cmdModGraph(args []string) error {
	// 设计者视角：graph 离线画依赖（Nodes 按径、Edges 逐印，未解标 unresolved）。
	// 障碍是未下载：ModuleGraph 指 fetch。排查现场：边缺先查树 present。
	fs := flag.NewFlagSet("mod graph", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("usage: tlua mod graph")
	}
	p, err := loadModProject()
	if err != nil {
		return err
	}
	cache, err := modCache()
	if err != nil {
		return err
	}
	g, err := mod.ModuleGraph(context.Background(), p, cache)
	if err != nil {
		return err
	}
	byPath := map[string]mod.LockEntry{}
	for _, n := range g.Nodes {
		byPath[n.Path] = n
	}
	for _, n := range g.Nodes {
		fmt.Printf("%s@%s\n", n.Path, n.Version)
		for _, dep := range g.Edges[n.Path] {
			if target, ok := byPath[dep]; ok {
				fmt.Printf("  %s@%s\n", dep, target.Version)
			} else {
				fmt.Printf("  %s (unresolved)\n", dep)
			}
		}
	}
	return nil
}
