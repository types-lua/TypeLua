// Package token 定义词法单元（Token）的种类（Kind）、位置（Pos/Span）与文本。
//
// 词法层只分类、不赋义：它把源码切成带面单的块，从不求值数字、从不决定
// 关键字在当前上下文是关键字还是标识符。面单（Span）一旦贴上，后续所有
// 诊断都靠它定位，行列号永远准确。
//
// # Token 的物理布局
//
// 一个 Token 是“种类 + 原文 + 坐标”三元组；坐标再拆成字节偏移与行列号：
//
//	Token
//	├── Kind: Kind        // int 枚举：Lua 全集 + TypeLua 软关键字 + 标点/运算符
//	├── Literal: string   // 源文本原样保留（"0xFF" 就是 "0xFF"，不求值）
//	└── Span: Span
//	    ├── File: string  // 源文件名（多文件诊断自带地址）
//	    ├── Start: Pos
//	    │   ├── Offset: int  // 字节偏移：切分/切片用，O(1)
//	    │   ├── Line: int    // 1 起行号：给人看
//	    │   └── Column: int  // 1 起列号：按 rune 计，中文不跑偏
//	    └── End: Pos          // 半开区间 [Start, End)
//
//	Pos 三字段分工：
//	+--------+--------+--------+
//	| Offset | Line   | Column |
//	| bytes  | 1-based| 1-based|
//	+--------+--------+--------+
//	Offset 给机器（切片、定位），Line/Column 给人（报错展示）；三者同时携带，
//	避免“展示时再算行列”的重复扫描。
//
// # 为什么这样设计
//
//   - Kind 用 int 枚举而非字符串：switch 跳转快、内存紧凑、Kind.String() 只在
//     报错/调试时才调用。Lua 与 TypeLua 扩展混在同一枚举里，扩展部分全是
//     软关键字——词法只贴“疑似关键字”标签，判决权交给语法层，词法保持无状态。
//   - Literal 存原文而非求值结果：词法只分类、不赋义。数字字面量类型、字符串
//     字面量类型都依赖原文；求值推迟到检查器，避免词法层与类型语义耦合。
//   - Span 半开 [Start, End)：长度 = End.Offset-Start.Offset，切片直接可用；
//     空 token 的 Start==End 合法，用于 EOF/合成坐标。
//   - File 全程携带：多文件编译排序诊断时不需要外部映射表，面单自带地址。
//   - Pos.Offset 与 Line/Column 分离：切分走字节偏移 O(1)，展示走行列；Column
//     按 rune 计数，长中文注释列号也不歪。
//   - 软关键字不进词法判定：`type(x)` 与 `type Foo = …` 词法都标 TypeKw，
//     由 parser 按位置决定是调用还是声明——这是“词法无状态、语义全在语法”
//     的全系统解耦。
//
// 明确不做
//   - 不求值字面量、不解析数字/字符串转义为运行时值；
//   - 不做关键字上下文判定（软关键字由 parser 决定）；
//   - 不保存 trivia（注释/空白）与父节点引用；
//   - 不做位置到行列的反查表（Pos 已直接携带）。
//
// 不变量
//   - Kind 的 iota 顺序一旦落盘/缓存即冻结，新增种类只能追加；
//   - Span 为半开区间，Start.Offset <= End.Offset；
//   - Line/Column 为 1 起；Offset 为 0 起的字节偏移；
//   - Literal 与源码切片字节一致（不规范化）。
//
// 模块间协作
//   - 依赖：无内部包（仅标准库 fmt）。
//   - 被依赖：ast、lexer、parser、checker、diag、expand、lsp 等几乎所有包；
//     Span/Pos 是全编译器共享的坐标货币。
package token
