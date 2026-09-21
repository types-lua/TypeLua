package lsp

import (
	"strings"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/checker"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/parser"
)

// Open documents: content, parse, check, and the position index.

// ---------- documents ----------

type document struct {
	uri     string
	version int
	content string
	lines   []int // byte offset of each 0-based line start
	file    *ast.File
	diags   []diag.Diagnostic
	index   *checker.SymIndex
}

func uriToName(uri string) string {
	if strings.HasPrefix(uri, "file://") {
		return strings.TrimPrefix(uri, "file://")
	}
	return uri
}

func splitLines(content string) []int {
	lines := []int{0}
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			lines = append(lines, i+1)
		}
	}
	return lines
}

func analyze(uri, content string, version int) *document {
	name := uriToName(uri)
	f, pdiags := parser.ParseFile(name, content)
	diags, idx := checker.CheckFileIndexed(f, checker.DefaultOptions())
	diags = append(append([]diag.Diagnostic{}, pdiags...), diags...)
	return &document{uri: uri, version: version, content: content, lines: splitLines(content), file: f, diags: diags, index: idx}
}
