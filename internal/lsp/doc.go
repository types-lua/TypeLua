// Package lsp 实现 TypeLua 的语言服务器（LSP）：文档同步、诊断、补全、跳转、引用、悬停。
//
// LSP 包是“问答台”：把 parser/checker 的结果包装成 IDE 可消费的查询，
// 不重复实现语义，只做位置映射与缓存。文档每次变更重新 parse + check，
// 结果缓存在 document 里供后续请求复用。
//
// # 为什么 LSP 复用 parser/checker 而非另写
//
// 语义必须与编译器完全一致；另写一套必然漂移。LSP 只负责把编译器产物
// 映射到 IDE 协议（位置、符号、诊断），不重新发明类型系统。
//
// # 文档模型
//
// document 持有 uri、版本、内容、行偏移表、AST、诊断与符号索引；
// analyze() 跑 ParseFile + CheckFileIndexed，把词法/语法/类型诊断合并。
//
// 明确不做
//   - 不做增量类型检查（每次全量重新检查）；
//   - 不做自有类型系统（复用 checker）；
//   - 不做文件监听（由编辑器/客户端驱动）。
//
// 不变量
//   - 文档状态按版本号单调更新；
//   - 诊断合并顺序：parser 诊断在前，checker 诊断在后；
//   - 位置映射基于行偏移表，列按字节/字符约定与协议对齐。
//
// 模块间协作
//   - 依赖：ast、checker、diag、parser、token、types。
//   - 被依赖：cmd/tlua（lsp 子命令/stdio）。
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
