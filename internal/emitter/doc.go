// Package emitter 把类型擦除后的 AST 渲染成 Lua 源码，并生成 source map。
//
// 发射器是“拆脚手架工”：只认图纸（AST），不认整改单（诊断），有错也发射
// （TypeScript 语义）。它遍历 AST，输出 Lua 文本与语句粒度的映射；对使用
// 协作并发（spawn/await/select/通道）的文件自动加上调度器前导与
// `__tl_run` 包装。
//
// # 为什么发射器看不到类型
//
// 类型信息只住在 checker 的符号表里，发射器只读 AST，因此它不可能把类型
// 错误带进最终 Lua，也不可能“顺手”按类型生成不同代码。类型擦除的正确性
// 由数据流结构保证，而不是靠约定。
//
// # Source map
//
// 映射按语句粒度、rune 列记录；始终收集，编码只在调用方需要时发生，
// 开销近似为零。并发文件的调度器前导与包装在发射时注入。
//
// # Tree-shaking
//
// 可选的 ShakeFile 在并发检测之前运行，因此被移除代码里唯一的 await 会
// 让该文件不触发调度器前导——顺序是有意的，避免死代码拖入运行时。
//
// 明确不做
//   - 不做类型检查或语义分析；
//   - 不改写 AST 除 tree-shaking 这一显式步骤；
//   - 不决定文件输出路径（compiler 负责）。
//
// 不变量
//   - 发射结果仅依赖 AST 与 Options，不依赖诊断；
//   - source map 的 generated 列为 0 起的 rune 偏移；
//   - 并发检测为真时必须注入调度器前导与 `__tl_run` 包装。
//
// 模块间协作
//   - 依赖：ast、shake、sourcemap。
//   - 被依赖：compiler；测试直接调用 EmitFile。
package emitter
