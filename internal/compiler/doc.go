// Package compiler 编排 TypeLua 的完整编译流水线：加载、解析、检查、发射与诊断聚合。
//
// 它是“厂长”：`a.tl→a.lua` 按依赖序过 parser/checker/emitter，`*.d.tl` 只查不发，空文件亦发货保形。
//
// # 为什么把调度集中在一个 Program
//
// 教训是调度散则乱（如半个文件已发、另半诊断未聚）：Program 把 Load→Check→Emit 谁先谁后钉死，算法留各包。
//
// # 核心对象
//
//   - SourceFile：一个加载单元（路径、内容、AST、是否声明文件）；
//   - Program：文件集合 + 诊断 + 配置 + 模块短名映射；
//   - Load/LoadSources：并行读取与解析，结果按路径排序，保证确定性；
//   - Check：按依赖顺序检查，未解析的 require 退化为 `any`；
//   - Emit：发射 Lua 与 source map，可选 tree-shaking。
//
// # 为什么声明文件不发货
//
// *.d.tl 是类型契约，只参与检查（ambient scope），不产生 Lua 输出——
// 这是 TypeScript 的 .d.ts 语义在 TypeLua 的对应。
//
// 明确不做
//   - 不实现词法/语法/类型/发射算法（分别由对应包负责）；
//   - 不做文件监听（由 watch 包负责）；
//   - 不做包管理下载（由 mod 包负责）。
//
// 不变量
//   - Files 与 Diags 按路径排序，保证并行加载结果确定；
//   - 声明文件 IsDecl=true，不进入发射列表；
//   - 未解析的 require 退化为 `any`，不中断检查；
//   - 诊断聚合后由 FormatDiags 按“文件-行号”排序。
//
// 模块间协作
//   - 依赖：ast、checker、config、diag、emitter、parser、sourcemap、mod、
//     watch、types。
//   - 被依赖：cmd/tlua、watch。
package compiler
