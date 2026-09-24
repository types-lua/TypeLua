package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/typelua/typelua/internal/bindgen"
	"github.com/typelua/typelua/internal/parser"
)

// cmdBindgen generates a .d.tl type contract from a C header subset:
// `tlua bindgen --header foo.h --out types/foo.d.tl [--lib CLib] [--check]`.
func cmdBindgen(args []string) error {
	fs := flag.NewFlagSet("bindgen", flag.ContinueOnError)
	header := fs.String("header", "", "input C header file (required)")
	out := fs.String("out", "", "output .d.tl file (default: stdout)")
	lib := fs.String("lib", "CLib", "interface name for functions/defines")
	check := fs.Bool("check", false, "verify generated output parses without errors")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*header) == "" {
		return fmt.Errorf("tlua bindgen: --header is required (e.g. --header mylib.h --out types/mylib.d.tl)")
	}
	raw, err := os.ReadFile(*header)
	if err != nil {
		return fmt.Errorf("tlua bindgen: cannot read header: %v", err)
	}
	got := bindgen.Generate(string(raw), *lib)
	if *check || strings.HasSuffix(*out, ".d.tl") || *out == "" {
		// Always validate when emitting a contract; stdout still validates
		// silently (errors go to stderr, output stays pure for piping).
		name := *out
		if name == "" {
			name = "bindgen.d.tl"
		}
		if _, pdiags := parser.ParseFile(name, got); len(pdiags) > 0 {
			var sb strings.Builder
			for _, d := range pdiags {
				sb.WriteString(d.String())
				sb.WriteString("\n")
			}
			return fmt.Errorf("tlua bindgen: generated output has parse errors:\n%s", sb.String())
		}
	}
	if *out == "" {
		fmt.Print(got)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(*out, []byte(got), 0o644); err != nil {
		return err
	}
	fmt.Printf("generated %s (lib %s)\n", *out, *lib)
	return nil
}
