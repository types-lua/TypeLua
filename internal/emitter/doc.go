// Package emitter 把类型擦除后的 AST 渲染成 Lua 源码，并生成 source map。
//
// 发射器是“拆脚手架工”：`local x: integer = 1` 擦成 `local x = 1` 照发（有错亦发）；含 `await` 即包 `__tl_run` 前导。
//
// # 为什么发射器看不到类型
//
// 类型住 checker 符号表、发射器只读 AST：`x as string` 与 `x` 吐同一 Lua，擦除正确由数据流结构保证。
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
