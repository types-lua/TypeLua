// Package sourcemap 实现 Source Map V3 的编码与 VLQ 基础原语。
//
// 它把生成位置映射回原始源位置，供调试器与 IDE 还原 TypeLua 源码。
// 映射按生成行隐式分组，生成列与源行列均为 0 起；名称索引进 names 表。
//
// # 为什么 statement 粒度
//
// …（略，详见实现；排查先看 Span/诊断）
// 模块间协作
//   - 依赖：无内部包（仅标准库）。
//   - 被依赖：emitter、compiler。
package sourcemap
