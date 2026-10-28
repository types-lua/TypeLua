// Package diag 定义编译器诊断（Diagnostic）的数据模型与稳定错误码注册表。
//
// 诊断是“整改单”：Code（稳定编号）+ Message + Span + Severity。编码按车间
// 分段：TL1xxx 词法、TL2xxx 语法、TL3xxx 类型（含 TL33xx 类、TL36xx 条件
// 类型、TL37xx 宏、TL38xx 并发）、TL4xxx 工程/发射。看到前缀就知道去哪
// 个车间查。
//
// # 为什么诊断要带稳定 Code 而非仅文本
//
// 文本会随措辞调整变化，IDE/测试/用户脚本需要稳定标识来匹配；Code 是契约，
// Message 是给人看的解释。Span 全程携带文件与行列，多文件诊断可直接排序。
//
// # 为什么 Span 用 token.Span
//
// 全编译器共享同一套坐标货币，诊断不需要转换坐标；半开区间让定位与切片一致。
//
// 明确不做
//   - 不负责渲染/着色（CLI/LSP 负责）；
//   - 不负责诊断去重（compiler 聚合时处理）；
//   - 不持有源文本。
//
// 不变量
//   - Code 前缀与车间一一对应，新增代码只能追加；
//   - Diagnostic.Error() 返回可读字符串，含文件:行:列；
//   - Severity 仅 Error/Warning 两档。
//
// 模块间协作
//   - 依赖：token、types（消息缩写）。
//   - 被依赖：lexer、parser、checker、expand、compiler、lsp 等所有产生诊断的包。
package diag
