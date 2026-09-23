package diag

import (
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var updateDocs = flag.Bool("update", false, "rewrite docs/11-diagnostics.md from the registry")

// repoRoot is the module root (tests run in internal/diag).
func repoRoot() string { return filepath.Join("..", "..") }

// testConsts parses codes.go and returns declared constant name -> value
// for every spec whose value looks like a diagnostic code.
func testConsts(t *testing.T) map[string]string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "codes.go", nil, 0)
	if err != nil {
		t.Fatalf("parse codes.go: %v", err)
	}
	out := map[string]string{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				val, err := strconv.Unquote(lit.Value)
				if err != nil {
					continue
				}
				if Code(val).Valid() {
					out[name.Name] = val
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no code constants found in codes.go")
	}
	return out
}

// TestCodeTableMatchesConstants is the anti-drift guard: every constant
// must have exactly one table row (with the same name), and the table
// must not contain codes without a constant.
func TestCodeTableMatchesConstants(t *testing.T) {
	consts := testConsts(t)
	seen := map[Code]string{}
	for _, d := range codeTable {
		got, ok := consts[d.name]
		if !ok {
			t.Errorf("table row %s (%s): no matching constant %s in codes.go", d.code, d.desc, d.name)
			continue
		}
		if got != string(d.code) {
			t.Errorf("constant %s = %q, table says %q", d.name, got, d.code)
		}
		if prev, dup := seen[d.code]; dup {
			t.Errorf("duplicate code %s: constants %s and %s", d.code, prev, d.name)
		}
		seen[d.code] = d.name
	}
	for name, val := range consts {
		if _, ok := codeIndex[Code(val)]; !ok {
			t.Errorf("constant %s = %q is missing from codeTable", name, val)
		}
	}
	if len(seen) != len(codeTable) {
		t.Errorf("table has %d rows but %d unique codes", len(codeTable), len(seen))
	}
}

func TestCodeFormatAndCategory(t *testing.T) {
	for _, d := range codeTable {
		if !d.code.Valid() {
			t.Errorf("%s: malformed code", d.code)
		}
		if d.name == "" || d.name[0] < 'A' || d.name[0] > 'Z' {
			t.Errorf("%s: constant name %q must be exported", d.code, d.name)
		}
		if d.desc == "" {
			t.Errorf("%s: empty description", d.code)
		}
		if got := categoryOf(string(d.code)); got != d.cat {
			t.Errorf("%s: category %s does not match numeric range (%s)", d.code, d.cat, got)
		}
		if d.cat == CatUnknown {
			t.Errorf("%s: CatUnknown is reserved for unregistered codes", d.code)
		}
	}
}

func TestCodesSortedUnique(t *testing.T) {
	codes := Codes()
	if len(codes) != len(codeTable) {
		t.Fatalf("Codes() length %d, table %d", len(codes), len(codeTable))
	}
	for i := 1; i < len(codes); i++ {
		if codes[i-1] >= codes[i] {
			t.Fatalf("Codes() not strictly ascending at %d: %s >= %s", i, codes[i-1], codes[i])
		}
	}
	// Table itself should be pre-sorted (keeps diffs readable).
	for i := 1; i < len(codeTable); i++ {
		if codeTable[i-1].code >= codeTable[i].code {
			t.Errorf("codeTable unsorted at %d: %s >= %s", i, codeTable[i-1].code, codeTable[i].code)
		}
	}
}

func TestCodeLookupAndHelpers(t *testing.T) {
	c, ok := Lookup("TL3801")
	if !ok || c != AwaitOutsideAsync {
		t.Fatalf("Lookup(TL3801) = %v %v", c, ok)
	}
	if c.Description() == "" || c.Name() != "AwaitOutsideAsync" || c.Category() != CatConcurrency {
		t.Fatalf("metadata mismatch: %q %q %v", c.Description(), c.Name(), c.Category())
	}
	if _, ok := Lookup("TL9999"); ok {
		t.Fatalf("unknown code must not resolve")
	}
	if Code("TL3999").Known() || !Code("TL3999").Valid() {
		t.Fatalf("unknown-but-valid code misclassified")
	}
	if Code("TL3999").Category() != CatChecker {
		t.Fatalf("unknown TL3xxx should fall back to checker: %v", Code("TL3999").Category())
	}
	if Code("TL9999").Category() != CatUnknown {
		t.Fatalf("code outside known ranges should be unknown: %v", Code("TL9999").Category())
	}
	if Code("nope").Category() != CatUnknown || Code("nope").Valid() {
		t.Fatalf("malformed code misclassified")
	}
	if Code("TL801").Valid() || Code("TL12345").Valid() {
		t.Fatalf("wrong-length code must be invalid")
	}
	if Code("TL38xx").Valid() {
		t.Fatalf("non-digit code must be invalid")
	}
}

// TestNoRawCodeLiterals forbids bare "TLnnnn" strings outside the diag
// package: call sites and tests must use the typed constants, so a typo
// cannot silently invent a code. The registry package itself is exempt
// (codes.go declares the wire spellings; these tests parse them).
func TestNoRawCodeLiterals(t *testing.T) {
	pat := regexp.MustCompile(`"TL[0-9]{4}"`)
	root := repoRoot()
	skipDir := filepath.Join("internal", "diag")
	var bad []string
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if info.Name() == ".git" || info.Name() == "testdata" {
					return filepath.SkipDir
				}
				if rel, relErr := filepath.Rel(root, path); relErr == nil && rel == skipDir {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if loc := pat.FindIndex(raw); loc != nil {
				bad = append(bad, rel)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Fatalf("raw TLnnnn literals outside codes.go (use diag constants): %v", bad)
	}
}

const docHeader = "# 11 · 诊断码参考：TLnnnn 全表\n" +
	"\n" +
	"【一句话】编译器的每一条诊断都有一个稳定编号：`TL` 加四位数字，按工序分段——`TL1xxx` 词法、`TL2xxx` 语法、`TL3xxx` 检查（`TL37xx` 宏、`TL38xx` 并发）、`TL4xxx` 工程。\n" +
	"\n" +
	"注册表在 `internal/diag/codes.go`：每个码一个语义常量（调用点只准用常量，禁止裸字符串）、一个分类、一句描述。本文件由注册表生成，请勿手改；改完注册表后运行：\n" +
	"\n" +
	"```bash\n" +
	"go generate ./internal/diag\n" +
	"```\n" +
	"\n"

func docPath() string { return filepath.Join(repoRoot(), "docs", "11-diagnostics.md") }

// TestDiagnosticsDocInSync keeps the generated reference doc in sync with
// the registry. docs/ is local working notes (gitignored), so the check
// only applies when that directory exists; a fresh clone skips it.
func TestDiagnosticsDocInSync(t *testing.T) {
	if _, err := os.Stat(filepath.Join(repoRoot(), "docs")); err != nil {
		t.Skip("docs/ not present (local working notes)")
	}
	want := docHeader + "\n" + MarkdownTable()
	if *updateDocs {
		if err := os.MkdirAll(filepath.Dir(docPath()), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(docPath(), []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(docPath())
	if err != nil {
		t.Fatalf("read docs (run `go generate ./internal/diag`): %v", err)
	}
	if string(raw) != want {
		t.Fatalf("docs/11-diagnostics.md is stale; run `go generate ./internal/diag`")
	}
}
