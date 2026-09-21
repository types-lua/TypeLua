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
	dir, err := mod.DefaultDir()
	if err != nil {
		return nil, err
	}
	return mod.NewCache(dir), nil
}

func cmdModInit(args []string) error {
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
	cwd, _ := os.Getwd()
	return mod.LoadProject(cwd)
}

func cmdModGet(args []string) error {
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
