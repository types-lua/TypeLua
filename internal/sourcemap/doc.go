// Package sourcemap 实现 Source Map V3 的编码与 VLQ 基础原语。
//
// 它把生成位置映射回原始源位置，供调试器与 IDE 还原 TypeLua 源码。
// 映射按生成行隐式分组，生成列与源行列均为 0 起；名称索引进 names 表。
//
// # 为什么 statement 粒度
//
// 表达式粒度的映射体积大、收益小；语句粒度已能定位到行与列，调试体验
// 与体积的折中。映射始终收集、编码延迟，开销近似为零。
//
// 明确不做
//   - 不做源到生成的反向查询（由 LSP/调试器按需）；
//   - 不做映射合并或重写；
//   - 不持有源文本（由 compiler 传入）。
//
// 不变量
//   - Encode 要求 mappings 按 (GenLine, GenCol) 排序；
//   - AppendVLQ 遵循 base64-VLQ 规范（符号在 LSB）；
//   - 生成列 0 起、源行列 0 起。
//
// 模块间协作
//   - 依赖：无内部包（仅标准库）。
//   - 被依赖：emitter、compiler。
package sourcemap
