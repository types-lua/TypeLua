package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// cmdExample implements the cargo-style examples workflow:
//
//	tlua example              list examples with their one-line descriptions
//	tlua example NAME         run examples/NAME/main.tl or examples/NAME.tl
//	tlua example NAME -- args pass arguments to the example
//
// Discovery accepts both layouts so a project can use single files or a
// directory per example, and descriptions come from the first `---` comment.

type exampleEntry struct {
	Name string
	Path string
	Desc string
}

func cmdExample(args []string) error {
	fs := flag.NewFlagSet("example", flag.ContinueOnError)
	check := fs.Bool("check", false, "type-check before running (default: parse only)")
	luaBin := fs.String("lua", "", "external lua interpreter (e.g. lua5.4, luajit); default: embedded golua")
	target := fs.String("target", "", "lua target: 5.4 or 5.1 (default: tlua.json or 5.4)")
	format := fs.String("format", "text", "diagnostic output: text or json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *format != "text" && *format != "json" {
		return fmt.Errorf("invalid --format %q (want text or json)", *format)
	}

	root, err := projectRoot()
	if err != nil {
		return err
	}
	examples := discoverExamples(root)
	if len(examples) == 0 {
		return fmt.Errorf("no examples found (expected examples/NAME.tl or examples/NAME/main.tl)")
	}

	rest := fs.Args()
	if len(rest) == 0 {
		printExamples(examples)
		return nil
	}

	entry, ok := findExample(examples, rest[0])
	if !ok {
		return fmt.Errorf("unknown example %q (run `tlua example` to list them)", rest[0])
	}
	// Run through a root-level wrapper: the project root is on package.path,
	// so examples in subdirectories can import project modules with plain
	// `tlua example`, `--lua lua5.4` and from any working directory.
	rel, err := filepath.Rel(root, entry.Path)
	if err != nil {
		rel = filepath.Base(entry.Path)
	}
	module := strings.ReplaceAll(strings.TrimSuffix(filepath.ToSlash(rel), ".tl"), "/", ".")
	wrapper := filepath.Join(root, "__tlua_example_main.tl")
	source := fmt.Sprintf("require(%q)\n", module)
	return runEntry(wrapper, rest[1:], runOptions{
		check:  *check,
		luaBin: *luaBin,
		target: *target,
		format: *format,
	}, map[string]string{wrapper: source})
}

// projectRoot locates the nearest tlua.json directory (or the cwd) so
// examples resolve the same way regardless of the invocation directory.
func projectRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if cfg := findConfig(cwd); cfg != "" {
		return filepath.Dir(cfg), nil
	}
	return cwd, nil
}

// findConfig is a thin wrapper kept local to avoid exporting config internals.
func findConfig(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	for {
		candidate := filepath.Join(abs, "tlua.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return ""
		}
		abs = parent
	}
}

// discoverExamples scans examples/ for both supported layouts. A directory
// example wins over a same-named single file.
func discoverExamples(root string) []exampleEntry {
	dir := filepath.Join(root, "examples")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	found := map[string]exampleEntry{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			main := filepath.Join(dir, name, "main.tl")
			if _, err := os.Stat(main); err == nil {
				found[name] = exampleEntry{Name: name, Path: main, Desc: describeExample(main)}
			}
			continue
		}
		if !strings.HasSuffix(name, ".tl") || strings.HasSuffix(name, ".d.tl") {
			continue
		}
		base := strings.TrimSuffix(name, ".tl")
		if _, exists := found[base]; exists {
			continue
		}
		path := filepath.Join(dir, name)
		found[base] = exampleEntry{Name: base, Path: path, Desc: describeExample(path)}
	}
	out := make([]exampleEntry, 0, len(found))
	for _, e := range found {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func findExample(examples []exampleEntry, name string) (exampleEntry, bool) {
	for _, e := range examples {
		if e.Name == name {
			return e, true
		}
	}
	return exampleEntry{}, false
}

// describeExample reads the first `---` doc line of an example file.
func describeExample(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "---") {
			break
		}
		text := strings.TrimSpace(strings.TrimPrefix(trimmed, "---"))
		if text != "" {
			return text
		}
	}
	return ""
}

func printExamples(examples []exampleEntry) {
	width := 0
	for _, e := range examples {
		if len(e.Name) > width {
			width = len(e.Name)
		}
	}
	fmt.Printf("examples · %d\n", len(examples))
	for _, e := range examples {
		if e.Desc == "" {
			fmt.Printf("  %s\n", e.Name)
			continue
		}
		fmt.Printf("  %-*s  %s\n", width, e.Name, e.Desc)
	}
	fmt.Println("\nrun one with: tlua example NAME [-- args...]")
}
