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
//
// 设计者视角：document 是 IDE 的“一页纸档案”——uri/version/content/lines 管文本，
// file/diags/index 管语义（AST+诊断+符号索引）。障碍是行列三制混杂：token 用
// 1 起 rune，LSP 用 0 起 UTF-16，内部用 byte offset；lines 表即翻译官。候选：
// 存行串 vs 存偏移；选偏移，offsetAt/toRange 零拷。不变量：lines[0]==0。
// 排查现场：位置漂移先查行表与 UTF-16 换算。

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
	// 设计者视角：uriToName 剥 `file://` 得盘径，供 Span.File 与诊断对齐。障碍是
	// 非 file 协议（untitled）：原样返，诊断仍可显。候选：url 解码 vs 直剥；选直剥，
	// 简单。不变量：非 file 原样。排查现场：诊断文件名怪先查此。
	if strings.HasPrefix(uri, "file://") {
		return strings.TrimPrefix(uri, "file://")
	}
	return uri
}

func splitLines(content string) []int {
	// 设计者视角：splitLines 建行首偏移表，`\n` 后即新行首。障碍是末行无换行、
	// 空文件：首元 0 恒在。候选：strings.Split vs 偏移；选偏移，offsetAt O(行内)。
	// 不变量：首元恒 0。排查现场：行越界先查此表。
	lines := []int{0}
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			lines = append(lines, i+1)
		}
	}
	return lines
}

func analyze(uri, content string, version int) *document {
	// 设计者视角：analyze 是文档的“全量体检”——ParseFile 取 AST+语法诊，再
	// CheckFileIndexed 取类型诊+索引，诊断按 parser 在前拼接。障碍是空 AST：
	// checker 恒可查（nil 宽容），索引恒有。候选：增量 vs 全量；选全量，单文件小、
	// 正确压倒省算。不变量：diags 恒 parser 在前。排查现场：悬停缺失先查 index 是否 nil。
	name := uriToName(uri)
	f, pdiags := parser.ParseFile(name, content)
	diags, idx := checker.CheckFileIndexed(f, checker.DefaultOptions())
	diags = append(append([]diag.Diagnostic{}, pdiags...), diags...)
	return &document{uri: uri, version: version, content: content, lines: splitLines(content), file: f, diags: diags, index: idx}
}
