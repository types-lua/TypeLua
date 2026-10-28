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

// cmdBindgen 从 C 头文件子集生成 .d.tl 类型契约（可选类型化 .tl shim）：
// `tlua bindgen --header foo.h --out types/foo.d.tl [--shim foo.tl] [--lib CLib] [--so libfoo.so] [--check]`。
//
// 设计者视角：读头→Generate 契约→必校验（.d.tl/空/--check 皆验，stdout 错走 stderr
// 保管道纯）→落盘/out 空即 stdout；shim 按需（so 空推导）另验另落。障碍是生成坏：
// 校验拦之不落盘。候选：验/不验；选必验保契约可析。排查现场：管道脏先查校验分支。
func cmdBindgen(args []string) error {
	fs := flag.NewFlagSet("bindgen", flag.ContinueOnError)
	header := fs.String("header", "", "input C header file (required)")
	out := fs.String("out", "", "output .d.tl file (default: stdout)")
	shim := fs.String("shim", "", "output .tl loader shim (typed require target, e.g. mylib.tl)")
	so := fs.String("so", "", "sibling shared library loaded by the shim (default: lib<base>.so)")
	lib := fs.String("lib", "CLib", "interface name for functions/defines")
	check := fs.Bool("check", false, "verify generated output parses without errors")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*header) == "" {
		return fmt.Errorf("tlua bindgen: --header is required (e.g. --header mylib.h --out types/mylib.d.tl --shim mylib.tl)")
	}
	raw, err := os.ReadFile(*header)
	if err != nil {
		return fmt.Errorf("tlua bindgen: cannot read header: %v", err)
	}
	got := bindgen.Generate(string(raw), *lib)
	if *check || strings.HasSuffix(*out, ".d.tl") || *out == "" {
		// 发射契约时始终校验；stdout 仍静默校验
		//（错误走 stderr，输出保持纯净以便管道）。
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
	} else {
		if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(*out, []byte(got), 0o644); err != nil {
			return err
		}
		fmt.Printf("generated %s (lib %s)\n", *out, *lib)
	}
	if strings.TrimSpace(*shim) == "" {
		return nil
	}
	soName := strings.TrimSpace(*so)
	if soName == "" {
		soName = bindgen.DefaultSoName(*header)
	}
	shimGot := bindgen.GenerateShim(string(raw), filepath.Base(*header), *lib, soName)
	if _, pdiags := parser.ParseFile(*shim, shimGot); len(pdiags) > 0 {
		var sb strings.Builder
		for _, d := range pdiags {
			sb.WriteString(d.String())
			sb.WriteString("\n")
		}
		return fmt.Errorf("tlua bindgen: generated shim has parse errors:\n%s", sb.String())
	}
	if err := os.MkdirAll(filepath.Dir(*shim), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(*shim, []byte(shimGot), 0o644); err != nil {
		return err
	}
	fmt.Printf("generated %s (loads %s as %s)\n", *shim, soName, *lib)
	return nil
}
