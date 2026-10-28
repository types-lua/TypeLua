// Package lexer 把 TypeLua 源码切分成 Token 流，并附带词法诊断。
//
// 词法层是“质检分拣员”：`0xFF` 切 Number、`type` 贴 TypeKw 疑似签，坏字节 `0xFF` 孤字节记一笔跳过，永不停机。
//
// # 为什么词法永不停机
//
// 一卷源码一次报全：首个 `0xFF` 若停机，后文 10 个错全被藏；记账跳过让 `tlua build` 一次亮全红，Span 仍钉案发点。
//
// # 扫描模型
//
// 游标式单遍扫描：src + off + line + col；peek/advance 按字节推进，Column 按
// rune 计数；长字符串/注释用 advanceBlock 批量推进。数字与字符串存原文
// （Literal），不求值。
//
// # 为什么操作符用最长匹配
//
// `1..2` 切成 `1`+`..`+`2` 而非 `1.`+`.2` 靠最长匹配；`!=` 故意留给 parser 拼 `~=`，发射归一。
//
// 明确不做
//   - 不求值字面量、不决定关键字上下文；
//   - 不做语法恢复（parser 负责）；
//   - 不保存 trivia。
//
// 不变量
//   - Lex 对任意输入都不 panic，坏输入转为诊断；
//   - Token.Span 为半开区间，Offset 单调不减；
//   - 字节偏移与行列号同时携带。
//
// 模块间协作
//   - 依赖：diag、token。
//   - 被依赖：parser（ParseFile 先 Lex）。
package lexer
